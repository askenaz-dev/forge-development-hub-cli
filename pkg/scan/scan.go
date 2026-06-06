// Package scan implements `fdh scan` — a deterministic, rule-based
// audit of skills/rules/agents/hooks for secrets, hook command-
// injection patterns, and other security smells.
//
// All detectors are pure regex/heuristic; no LLM is invoked in the
// critical path. The Severity enum is `info | warning | error`; only
// `error` findings cause a non-zero exit.
package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Severity classifies findings.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Finding is one detection result.
type Finding struct {
	Severity Severity `json:"severity"`
	Rule     string   `json:"rule"`
	File     string   `json:"file"`
	Line     int      `json:"line,omitempty"`
	Message  string   `json:"message"`
	Snippet  string   `json:"snippet,omitempty"`
}

// Result bundles findings.
type Result struct {
	Findings []Finding `json:"findings"`
}

// HasError reports whether the result contains any `error` finding.
func (r *Result) HasError() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Registry scan_status enum values (capability portal-scan-status). These are
// the verdicts the producer/portal record per component version.
const (
	StatusPass = "pass" // scanned, no blocking findings
	StatusWarn = "warn" // scanned, non-blocking findings
	StatusFail = "fail" // scanned, blocking findings
	StatusNone = "none" // not scanned / no result (e.g. scan errored)
)

// Verdict maps a scan Result to the registry scan_status enum: any `error`
// finding → "fail"; otherwise any `warning` → "warn"; otherwise "pass".
// `info` findings are advisory and do not downgrade a clean verdict.
func Verdict(r *Result) string {
	status := StatusPass
	for _, f := range r.Findings {
		switch f.Severity {
		case SeverityError:
			return StatusFail
		case SeverityWarning:
			status = StatusWarn
		}
	}
	return status
}

// DirStatus scans dir and returns its scan_status verdict. A scan that cannot
// run returns ("none", err) so callers can record "none" without aborting the
// surrounding build/refresh.
func DirStatus(dir string) (string, error) {
	res, err := Scan([]string{dir})
	if err != nil {
		return StatusNone, err
	}
	return Verdict(res), nil
}

// Detector is one named scan rule.
type Detector struct {
	Rule     string
	Severity Severity
	RE       *regexp.Regexp
	Message  string
}

// detectors is the canonical rule set.
var detectors = []Detector{
	{Rule: "secret/github-token", Severity: SeverityError, RE: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`), Message: "GitHub personal-access-token pattern detected"},
	{Rule: "secret/aws-key", Severity: SeverityError, RE: regexp.MustCompile(`AKIA[0-9A-Z]{16}`), Message: "AWS access key pattern detected"},
	{Rule: "secret/jwt", Severity: SeverityWarning, RE: regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), Message: "JWT pattern detected"},
	{Rule: "secret/url-cred", Severity: SeverityError, RE: regexp.MustCompile(`https?://[^/:@\s]+:[^@\s]+@`), Message: "URL with embedded credentials detected"},
	{Rule: "hook/curl-pipe-sh", Severity: SeverityError, RE: regexp.MustCompile(`curl[^\n]*\|\s*sh`), Message: "Hook contains `curl … | sh` pattern (command injection)"},
	{Rule: "hook/eval", Severity: SeverityWarning, RE: regexp.MustCompile(`\beval\s*\(`), Message: "Hook contains `eval(` call"},
}

// allowlistDirective is the inline marker that suppresses findings on
// the same line: `# fdh:allow secret/github-token`.
var allowlistRE = regexp.MustCompile(`fdh:allow\s+(\S+)`)

// Scan walks paths and returns findings for every text file under
// them. Symbol links, binary files, and managed-marker sidecars are
// skipped.
func Scan(paths []string) (*Result, error) {
	res := &Result{Findings: []Finding{}}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return res, fmt.Errorf("stat %s: %w", p, err)
		}
		if info.IsDir() {
			err := filepath.WalkDir(p, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil //nolint:nilerr
				}
				if d.IsDir() {
					return nil
				}
				if shouldSkip(path) {
					return nil
				}
				scanFile(path, res)
				return nil
			})
			if err != nil {
				return res, err
			}
		} else {
			if !shouldSkip(p) {
				scanFile(p, res)
			}
		}
	}
	return res, nil
}

func shouldSkip(path string) bool {
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".fdh-managed.yaml") || base == ".skill-version" || strings.HasPrefix(base, ".skill-version-") {
		return true
	}
	if base == ".skill-meta.yaml" {
		return true
	}
	// Skip obvious binary extensions.
	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".pdf", ".zip", ".tar", ".gz", ".tgz", ".bin", ".exe":
		return true
	}
	return false
}

func scanFile(path string, res *Result) {
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if !looksLikeText(body) {
		return
	}
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		allowed := allowlistMatches(line)
		for _, det := range detectors {
			if !det.RE.MatchString(line) {
				continue
			}
			if _, skip := allowed[det.Rule]; skip {
				continue
			}
			res.Findings = append(res.Findings, Finding{
				Severity: det.Severity,
				Rule:     det.Rule,
				File:     path,
				Line:     i + 1,
				Message:  det.Message,
				Snippet:  obfuscate(line),
			})
		}
	}
}

func allowlistMatches(line string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range allowlistRE.FindAllStringSubmatch(line, -1) {
		if len(m) >= 2 {
			out[m[1]] = struct{}{}
		}
	}
	return out
}

func looksLikeText(b []byte) bool {
	// Very small heuristic: reject if more than 5% bytes are NUL.
	if len(b) == 0 {
		return true
	}
	nul := 0
	for _, c := range b {
		if c == 0 {
			nul++
		}
	}
	return nul*20 < len(b)
}

// obfuscate redacts the matched secret region for safe display.
// MVP: replace any run of 8+ non-space characters with first-3-stars.
func obfuscate(line string) string {
	re := regexp.MustCompile(`\S{8,}`)
	return re.ReplaceAllStringFunc(line, func(s string) string {
		if len(s) <= 6 {
			return s
		}
		return s[:3] + strings.Repeat("*", len(s)-6) + s[len(s)-3:]
	})
}
