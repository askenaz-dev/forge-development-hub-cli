// Package portability enforces the portable vs. non-portable distinction
// defined in the skill-portability spec.
//
// The single public entry point is Lint(bundle) []Finding. The installer
// calls Lint pre-write; later changes (publish flow, CI gates) call the
// same function so there is exactly one definition of portability.
package portability

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/forge/fdh/pkg/bundle"
)

// PortableAllowlist is the exact set of frontmatter keys permitted in a
// portable skill. Any other key fails the lint.
var PortableAllowlist = map[string]struct{}{
	"name":           {},
	"description":    {},
	"license":        {},
	"metadata":       {},
	"compatibility":  {},
	"portable":       {},
	"installed_from": {},
	"version":        {},
}

// Finding records one rule failure.
type Finding struct {
	RuleID   string
	Severity string // currently always "error"
	Path     string // relative-to-bundle path of the file where the issue was found
	Line     int    // 1-based line number; 0 if not applicable
	Offset   int    // 0-based byte offset within Path; 0 if not applicable
	Message  string
}

// LintOptions configures Lint.
type LintOptions struct {
	// KnownAgentIDs is used to validate the compatibility allowlist for
	// non-portable skills. If empty, compatibility entries are not
	// cross-checked against the adapter map (the lint still verifies the
	// list is non-empty).
	KnownAgentIDs []string
}

// Lint runs every rule against b and returns the findings. A skill with no
// findings is safe to install given the requested options.
func Lint(b *bundle.Bundle, opts LintOptions) []Finding {
	if b == nil {
		return []Finding{{
			RuleID:   "PORT000",
			Severity: "error",
			Message:  "bundle is nil",
		}}
	}

	var findings []Finding

	doc := b.SkillMD

	portable := doc.IsPortable()

	if portable {
		findings = append(findings, lintPortableFrontmatter(doc, b)...)
		findings = append(findings, lintBodySubstitutions(doc, b)...)
		findings = append(findings, lintDynamicInjection(doc, b)...)
	}

	findings = append(findings, lintCompatibility(doc, b, opts.KnownAgentIDs)...)

	return findings
}

// lintPortableFrontmatter enforces the allowlist. Each disallowed key
// produces one Finding citing the key name.
func lintPortableFrontmatter(doc bundle.SkillMDDoc, b *bundle.Bundle) []Finding {
	if !doc.HasFrontmatter {
		return nil
	}
	var keys []string
	for k := range doc.Raw {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic finding order
	var findings []Finding
	for _, k := range keys {
		if _, ok := PortableAllowlist[k]; ok {
			continue
		}
		findings = append(findings, Finding{
			RuleID:   "PORT100",
			Severity: "error",
			Path:     "SKILL.md",
			Message:  fmt.Sprintf("frontmatter key %q is not portable; restricted to %s", k, allowlistList()),
		})
	}
	return findings
}

func allowlistList() string {
	var out []string
	for k := range PortableAllowlist {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// Body-substitution regexes. The "currency safe" $0..$9 rule is implemented
// by requiring the next character to NOT be a digit or "." (so $0.00 doesn't
// trip, but $0 alone or $0/$0 does).
var (
	rxArguments      = regexp.MustCompile(`\$ARGUMENTS(\[\d+\])?`)
	rxClaudeSession  = regexp.MustCompile(`\$\{CLAUDE_SESSION_ID\}`)
	rxClaudeSkillDir = regexp.MustCompile(`\$\{CLAUDE_SKILL_DIR\}`)
	// $0..$9 followed by anything that is not a digit or '.', or by end-of-string.
	rxPositional = regexp.MustCompile(`\$[0-9]([^0-9.]|$)`)
)

// Dynamic context-injection markers (Claude Code only).
var (
	// Inline form: !`<cmd>` recognized at start of line or after whitespace.
	rxInlineInjectStart = regexp.MustCompile("(?m)^!`[^`]+`")
	rxInlineInjectWS    = regexp.MustCompile("\\s!`[^`]+`")
	// Fenced form: ```! at start of a line.
	rxFencedInject = regexp.MustCompile("(?m)^```!")
)

func lintBodySubstitutions(doc bundle.SkillMDDoc, b *bundle.Bundle) []Finding {
	var findings []Finding
	type rule struct {
		id      string
		rx      *regexp.Regexp
		message string
	}
	rules := []rule{
		{"PORT200", rxArguments, "uses Claude-only substitution $ARGUMENTS"},
		{"PORT201", rxClaudeSession, "uses Claude-only substitution ${CLAUDE_SESSION_ID}"},
		{"PORT202", rxClaudeSkillDir, "uses Claude-only substitution ${CLAUDE_SKILL_DIR}"},
		{"PORT203", rxPositional, "uses Claude-only positional substitution $0..$9"},
	}
	for _, r := range rules {
		for _, hit := range findAllLine(doc.Body, r.rx) {
			findings = append(findings, Finding{
				RuleID:   r.id,
				Severity: "error",
				Path:     "SKILL.md",
				Line:     hit.Line,
				Offset:   hit.Offset,
				Message:  r.message,
			})
		}
	}
	return findings
}

func lintDynamicInjection(doc bundle.SkillMDDoc, b *bundle.Bundle) []Finding {
	var findings []Finding
	for _, hit := range findAllLine(doc.Body, rxInlineInjectStart) {
		findings = append(findings, Finding{
			RuleID:   "PORT300",
			Severity: "error",
			Path:     "SKILL.md",
			Line:     hit.Line,
			Offset:   hit.Offset,
			Message:  "uses Claude-only inline dynamic context injection (!`cmd`)",
		})
	}
	for _, hit := range findAllLine(doc.Body, rxInlineInjectWS) {
		findings = append(findings, Finding{
			RuleID:   "PORT300",
			Severity: "error",
			Path:     "SKILL.md",
			Line:     hit.Line,
			Offset:   hit.Offset,
			Message:  "uses Claude-only inline dynamic context injection (!`cmd`)",
		})
	}
	for _, hit := range findAllLine(doc.Body, rxFencedInject) {
		findings = append(findings, Finding{
			RuleID:   "PORT301",
			Severity: "error",
			Path:     "SKILL.md",
			Line:     hit.Line,
			Offset:   hit.Offset,
			Message:  "uses Claude-only fenced dynamic context injection (```!)",
		})
	}
	return findings
}

func lintCompatibility(doc bundle.SkillMDDoc, _ *bundle.Bundle, known []string) []Finding {
	var findings []Finding
	if doc.IsPortable() {
		return findings // compatibility is optional in portable skills
	}
	if len(doc.Compatibility) == 0 {
		findings = append(findings, Finding{
			RuleID:   "PORT400",
			Severity: "error",
			Path:     "SKILL.md",
			Message:  "non-portable skill (portable: false) must declare a non-empty compatibility list",
		})
		return findings
	}
	if len(known) == 0 {
		return findings
	}
	knownSet := map[string]struct{}{}
	for _, k := range known {
		knownSet[k] = struct{}{}
	}
	for _, c := range doc.Compatibility {
		if _, ok := knownSet[c]; !ok {
			findings = append(findings, Finding{
				RuleID:   "PORT401",
				Severity: "error",
				Path:     "SKILL.md",
				Message:  fmt.Sprintf("compatibility entry %q is not a known agent (known: %s)", c, strings.Join(known, ", ")),
			})
		}
	}
	return findings
}

// Hit is one regex match annotated with line / offset coordinates.
type Hit struct {
	Line   int
	Offset int
}

// findAllLine returns one Hit per regex match in body, with 1-based line and
// 0-based byte offset coordinates.
func findAllLine(body []byte, rx *regexp.Regexp) []Hit {
	matches := rx.FindAllIndex(body, -1)
	if len(matches) == 0 {
		return nil
	}
	// Build a line-start table so we can convert byte offset → line.
	var lineStarts []int
	lineStarts = append(lineStarts, 0)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	pos := 0
	for scanner.Scan() {
		pos += len(scanner.Bytes()) + 1 // +1 for the newline scanner consumed
		lineStarts = append(lineStarts, pos)
	}
	hits := make([]Hit, 0, len(matches))
	for _, m := range matches {
		line := sort.SearchInts(lineStarts, m[0]+1) // last index <= m[0]
		if line < 1 {
			line = 1
		}
		hits = append(hits, Hit{Line: line, Offset: m[0]})
	}
	return hits
}

// HasErrors reports whether any finding is severity=error.
func HasErrors(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == "error" {
			return true
		}
	}
	return false
}

// Format renders findings as a human-readable string.
func Format(findings []Finding) string {
	if len(findings) == 0 {
		return "portability: no findings"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "portability: %d finding(s):\n", len(findings))
	for _, f := range findings {
		if f.Line > 0 {
			fmt.Fprintf(&sb, "  - [%s] %s:%d: %s\n", f.RuleID, f.Path, f.Line, f.Message)
		} else {
			fmt.Fprintf(&sb, "  - [%s] %s: %s\n", f.RuleID, f.Path, f.Message)
		}
	}
	return sb.String()
}
