// build-fixture-registry is a small developer utility that creates a
// spec-compliant registry on disk, useful for local E2E testing of the
// installer without needing a real Git remote.
//
// Usage:
//
//	go run ./scripts/build-fixture-registry <dest-dir>
//
// The destination directory is created if it does not exist. Existing
// content is overwritten on a per-file basis.
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/falabella/fdh/pkg/bundle"
)

type skillSpec struct {
	Namespace   string
	Name        string
	Version     string
	Description string
	OwnerTeam   string
	Tags        []string
	Files       map[string]string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: build-fixture-registry <dest-dir>")
		os.Exit(2)
	}
	dest := os.Args[1]
	if err := os.MkdirAll(dest, 0o755); err != nil {
		die(err)
	}

	specs := buildSeedSkills()

	idx := []map[string]any{}
	for _, s := range specs {
		skillDir := filepath.Join(dest, "skills", s.Namespace, s.Name)
		versionDir := filepath.Join(skillDir, "versions", s.Version)
		bundleDir := filepath.Join(versionDir, "bundle")
		mustMkdir(bundleDir)

		for rel, content := range s.Files {
			p := filepath.Join(bundleDir, filepath.FromSlash(rel))
			mustMkdir(filepath.Dir(p))
			must(os.WriteFile(p, []byte(content), 0o644))
		}

		// Load + Hash on the publish-side slot (which is named "bundle/"
		// per the registry spec). Validate is intentionally skipped here
		// because Validate enforces the *consumer-side* rule that the
		// directory name matches the skill name; on the publish side the
		// slot is always called "bundle/". GitRegistry.FetchBundle renames
		// the extracted directory to the skill name before the consumer
		// validates it.
		b, err := bundle.Load(bundleDir)
		must(err)
		hash, err := b.Hash()
		must(err)

		tarPath := filepath.Join(versionDir, "bundle.tar.gz")
		must(writeTarGz(tarPath, bundleDir, "bundle"))

		must(os.WriteFile(filepath.Join(versionDir, "bundle.sha256"),
			[]byte(hash+"  bundle.tar.gz\n"), 0o644))

		must(os.WriteFile(filepath.Join(versionDir, "changelog.md"),
			[]byte("Initial fixture release.\n"), 0o644))

		must(os.WriteFile(filepath.Join(versionDir, "scan-report.json"),
			[]byte(`{"status":"pass","findings":[]}`), 0o644))

		manifest := map[string]any{
			"schema_version": 1,
			"namespace":      s.Namespace,
			"name":           s.Name,
			"description":    s.Description,
			"owner_team":     s.OwnerTeam,
			"tags":           s.Tags,
			"latest":         s.Version,
			"versions": []map[string]any{
				{
					"version":      s.Version,
					"content_hash": hash,
					"published_at": "2026-05-22T12:00:00Z",
					"published_by": "fixture@local",
					"scan_status":  "pass",
				},
			},
		}
		mb, _ := json.MarshalIndent(manifest, "", "  ")
		must(os.WriteFile(filepath.Join(skillDir, "manifest.json"), mb, 0o644))

		must(os.WriteFile(filepath.Join(skillDir, "README.md"),
			[]byte("# "+s.Name+"\n\n"+s.Description+"\n"), 0o644))

		idx = append(idx, map[string]any{
			"namespace":      s.Namespace,
			"name":           s.Name,
			"description":    s.Description,
			"owner_team":     s.OwnerTeam,
			"tags":           s.Tags,
			"latest_version": s.Version,
			"latest_hash":    hash,
			"scan_status":    "pass",
		})

		fmt.Printf("published %s/%s@%s  hash=%s\n", s.Namespace, s.Name, s.Version, hash[:12])
	}

	indexBytes, _ := json.MarshalIndent(map[string]any{
		"schema_version": 1,
		"registry":       "file://" + filepath.ToSlash(dest),
		"skills":         idx,
	}, "", "  ")
	must(os.WriteFile(filepath.Join(dest, "index.json"), indexBytes, 0o644))

	fmt.Printf("\nRegistry built at %s\n", dest)
	fmt.Printf("Try:\n  fdh config set registry.local_path %q\n", dest)
	fmt.Printf("  fdh doctor\n  fdh search owasp\n  fdh install security/owasp-review\n")
}

func mustMkdir(p string) {
	must(os.MkdirAll(p, 0o755))
}

func must(err error) {
	if err != nil {
		die(err)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "fatal:", err)
	os.Exit(1)
}

// buildSeedSkills returns one portable skill per SDLC phase from the
// installer-core spec appendix:
//
//   1. requirements   - user-story-generation
//   2. architecture   - adr-generation
//   3. development    - pr-description-writer
//   4. code-review    - code-review-checklist
//   5. testing        - unit-test-generation
//   6. security       - owasp-quick-review
//   7. cicd           - release-notes-generation
//   8. operations     - runbook-template
//
// Every skill is `portable: true` by default — the bodies use only
// agent-neutral prose (no $ARGUMENTS, no Claude-only frontmatter) so the
// portability lint passes and they install to all four supported agents.
// Teams should treat these as starting points and customize ownership,
// tags, and prose to their domain.
func buildSeedSkills() []skillSpec {
	return []skillSpec{
		// 1. requirements
		{
			Namespace: "requirements", Name: "user-story-generation", Version: "1.0.0",
			Description: "Turn a feature request into well-formed user stories with explicit acceptance criteria.",
			OwnerTeam:   "product-platform", Tags: []string{"agile", "user-story", "acceptance-criteria"},
			Files: map[string]string{
				"SKILL.md": `---
name: user-story-generation
description: Turn a feature request into well-formed user stories with explicit acceptance criteria.
license: MIT
metadata:
  author: product-platform
  sdlc_phase: requirements
---

# User-story generation

When asked to break a feature request into user stories, follow this structure
for each story:

- Title: a single imperative sentence under 10 words.
- Narrative: "As a <role>, I want <capability>, so that <outcome>." Each
  clause is required and concrete; reject vague roles ("user" by itself) or
  outcomes ("be better").
- Acceptance criteria: between three and seven scenarios in Given/When/Then
  form. Each scenario MUST be independently testable.
- Non-goals: a short list of things the story explicitly does NOT cover.
- Open questions: anything the author needs the requester to clarify before
  estimation.

After drafting, verify each story against this checklist:
- INVEST: Independent, Negotiable, Valuable, Estimable, Small, Testable.
- No implementation detail leaks (no "use Postgres", "add a Redis cache").
- Every acceptance criterion maps to observable behaviour, not internal state.
`,
				"references/example.md": "# Example\n\nAs a regional buyer, I want to filter the daily promotions feed by inventory level, so that I never advertise an item we cannot ship.\n",
			},
		},
		// 2. architecture
		{
			Namespace: "architecture", Name: "adr-generation", Version: "1.0.0",
			Description: "Draft an architecture decision record (ADR) for a design choice and its alternatives.",
			OwnerTeam:   "architecture-guild", Tags: []string{"adr", "architecture", "decision-record"},
			Files: map[string]string{
				"SKILL.md": `---
name: adr-generation
description: Draft an architecture decision record (ADR) for a design choice and its alternatives.
license: MIT
metadata:
  author: architecture-guild
  sdlc_phase: architecture
---

# ADR generation

Produce an ADR in markdown using these sections, in order:

- Title: ADR-<NNNN> — short imperative phrase.
- Status: one of Proposed, Accepted, Superseded, Deprecated.
- Context: 2-4 paragraphs on the forces at play. What problem? What
  constraints? Who is affected?
- Decision: 1-2 paragraphs stating exactly what was decided. Use the
  active voice ("We will use ...").
- Consequences: positive and negative outcomes the decision creates, plus
  obligations it imposes on future work.
- Alternatives considered: at least two, each with a one-paragraph
  description and an explicit rejection reason.
- References: links to related ADRs, RFCs, vendor docs.

Quality bar before publishing:
- A reviewer who reads ONLY the Decision section can understand what was
  chosen.
- The Alternatives section is balanced (each option steel-manned), not a
  straw-man comparison.
- Consequences explicitly name the team or system that inherits each cost.
`,
				"references/template.md": "# ADR-NNNN — <title>\n\nStatus: Proposed\n\n## Context\n\n## Decision\n\n## Consequences\n\n## Alternatives considered\n\n## References\n",
			},
		},
		// 3. development
		{
			Namespace: "development", Name: "pr-description-writer", Version: "1.0.0",
			Description: "Compose a clear pull-request description from a diff: summary, rationale, test plan, risks.",
			OwnerTeam:   "dx-platform", Tags: []string{"pr", "review", "communication"},
			Files: map[string]string{
				"SKILL.md": `---
name: pr-description-writer
description: Compose a clear pull-request description from a diff. Produces summary, rationale, test plan, and risks.
license: MIT
metadata:
  author: dx-platform
  sdlc_phase: development
---

# Pull-request description writer

Use the structure below for every non-trivial pull request:

## Summary
One sentence stating what changes for the user or downstream system.
Do not narrate code mechanics.

## Why
Two to four sentences on the underlying motivation. Reference issue
trackers by ID, not by URL.

## What changed
Bulleted list grouped by area (api, ui, infra, docs, tests). Each bullet
is one short clause. Highlight breaking changes with **BREAKING**.

## Test plan
A checkbox list. Each item is a concrete, repeatable verification step a
reviewer can run. Include negative cases where they matter.

## Risks and rollback
- Risks: what could go wrong and who will notice first.
- Rollback: the exact sequence to revert if it does.

## Out of scope
A short list of related work this PR deliberately does NOT do, with
references to the follow-up tickets.

Anti-patterns:
- "Refactor stuff" — too vague, rewrite.
- A test plan that says "ran the existing tests" — list the new
  observable behaviour instead.
- Hidden behaviour changes packaged inside "small refactor" PRs.
`,
			},
		},
		// 4. code-review
		{
			Namespace: "code-review", Name: "checklist", Version: "1.0.0",
			Description: "Standardized code review checklist used by every Falabella reviewer.",
			OwnerTeam:   "dx-platform", Tags: []string{"review", "quality", "checklist"},
			Files: map[string]string{
				"SKILL.md": `---
name: checklist
description: Standardized code review checklist used by every Falabella reviewer.
license: MIT
metadata:
  author: dx-platform
  sdlc_phase: code-review
---

# Code-review checklist

Walk the diff once for each concern below. If a concern doesn't apply,
say so explicitly in the review comment rather than skipping silently.

## Correctness
- Inputs are validated at the boundary (request handler, public API).
- Error returns are checked at every call site.
- Concurrent access is either obviously single-writer or guarded.
- Off-by-one and edge cases (empty input, max input, nil) covered.

## Readability
- Names express intent, not type.
- Functions are small enough to be understood without scrolling.
- Comments explain WHY, not WHAT. Delete a comment that paraphrases code.

## Testing
- New behaviour has a test that fails without the change.
- Test names describe behaviour, not implementation.
- No flakiness sources: time.Sleep, network without retry, randomness without seed.

## Convention adherence
- Logging format matches the project's logging convention.
- Public API additions follow the project's naming and versioning rules.
- Migration scripts are reversible or have a documented forward-only justification.

## Security
- Secrets are not introduced in source.
- User input is escaped at the point of rendering, not at storage.
- Authorization is enforced at every layer that needs it (don't trust the UI).

Approval bar:
- Approve only when the above are addressed AND CI is green.
- A single "looks good" without comments is rarely a useful review.
`,
			},
		},
		// 5. testing
		{
			Namespace: "testing", Name: "unit-test-generation", Version: "1.0.0",
			Description: "Generate unit tests that target observable behavior with high signal and low brittleness.",
			OwnerTeam:   "qa-platform", Tags: []string{"unit-test", "tdd"},
			Files: map[string]string{
				"SKILL.md": `---
name: unit-test-generation
description: Generate unit tests that target observable behavior with high signal and low brittleness.
license: MIT
metadata:
  author: qa-platform
  sdlc_phase: testing
---

# Unit-test generation

When generating tests for a function or method, follow this method:

1. State the function's contract in one sentence (inputs, outputs, side effects).
2. List the equivalence classes of inputs. Cover at minimum:
   - the happy path
   - boundary values (empty, max, min, zero)
   - failure modes the function is documented to return
3. For each class, write one test. Each test:
   - has a name that describes the behavior, not the input ("rejects empty username", not "test 4")
   - sets up only what is needed (avoid shared fixtures across unrelated tests)
   - asserts ONLY what is observable from the contract; do not assert internal state
4. Skip tests for trivial getters/setters and for type-system-enforced invariants.
5. Add ONE property-based test for any function operating on numeric ranges or strings.

Quality bar:
- A failing test names the exact contract violation.
- No test depends on the order of any other test.
- Tests run in under one second collectively for typical pure functions.
`,
				"references/anti-patterns.md": "# Anti-patterns\n\n- Asserting on log output\n- Mocking the system under test\n- Tests that pass only when run in a specific order\n",
			},
		},
		// 6. security
		{
			Namespace: "security", Name: "owasp-quick-review", Version: "1.0.0",
			Description: "Run an OWASP top-10 sweep over a change set and report findings with severity.",
			OwnerTeam:   "appsec", Tags: []string{"owasp", "security", "review"},
			Files: map[string]string{
				"SKILL.md": `---
name: owasp-quick-review
description: Run an OWASP top-10 sweep over a change set and report findings with severity.
license: MIT
metadata:
  author: appsec
  sdlc_phase: security
---

# OWASP quick review

For every change in scope, check the categories below in order. For each
finding produce: severity (low/medium/high/critical), exact file and line,
short description, suggested remediation.

1. Broken access control — every endpoint enforces authorization;
   horizontal/vertical privilege escalation paths considered.
2. Cryptographic failures — TLS in transit, well-known algorithms at
   rest, key material managed by the platform (not embedded in source).
3. Injection — parameterized queries, validated user input on every
   path that reaches a sink (SQL, OS exec, LDAP, NoSQL, XPath).
4. Insecure design — sensitive workflows include audit logging, secure
   defaults, and explicit fail-closed behaviour.
5. Security misconfiguration — default credentials removed; admin
   endpoints not exposed without authn; CORS scoped narrowly.
6. Vulnerable components — declared dependencies advanced past known
   CVEs; transitive dependencies bounded.
7. Auth and identity failures — session lifetimes bounded; MFA where
   risk warrants; secure cookie flags set.
8. Software/data integrity failures — supply chain (signed artifacts,
   verified plugins); CI/CD steps require approvals for prod.
9. Logging and monitoring — every authn event logged; PII redacted in
   logs; alerts on anomalous behaviour.
10. Server-side request forgery — every URL fetched from user input
    routed through an allowlist.

When no findings exist in a category, say "no concerns" explicitly so the
reviewer can distinguish "checked and clean" from "not checked".
`,
				"references/severity-rubric.md": "# Severity rubric\n\n- critical: exploitable in production right now\n- high: exploitable with one missing safeguard\n- medium: defense-in-depth gap\n- low: minor hardening opportunity\n",
			},
		},
		// 7. cicd
		{
			Namespace: "cicd", Name: "release-notes-generation", Version: "1.0.0",
			Description: "Compose release notes from a list of merged PRs grouped by category.",
			OwnerTeam:   "platform-engineering", Tags: []string{"release", "changelog"},
			Files: map[string]string{
				"SKILL.md": `---
name: release-notes-generation
description: Compose release notes from a list of merged PRs grouped by category.
license: MIT
metadata:
  author: platform-engineering
  sdlc_phase: cicd
---

# Release-notes generation

Given a list of merged PRs (titles, descriptions, labels), produce release
notes in this exact structure:

## Headline
One sentence summarising the most user-impacting change.

## Highlights
Two to five bullets, each a single sentence describing a user-visible
improvement. No internal jargon, no PR numbers in this section.

## Changes by category
Group every PR under one of:
- Added
- Changed
- Fixed
- Removed
- Security
- Deprecated

For each entry: "<verb> <subject> (#<pr-number>, @<author>)". Order entries
within a category by user impact, not by merge time.

## Upgrade notes
Steps a user must take to move to this release. If none, write
"No action required." explicitly. Call out any breaking changes here even
if they appear under "Changed" above.

## Known issues
A short list of open issues users should be aware of, each with a link.

Quality bar:
- Marketing tone is OK in Highlights, technical tone elsewhere.
- A user reading ONLY Headline + Upgrade notes can decide whether to upgrade.
- Every PR in the input list appears under exactly one category.
`,
			},
		},
		// 8. operations
		{
			Namespace: "operations", Name: "runbook-template", Version: "1.0.0",
			Description: "Author or update a service runbook that on-call engineers can follow under pressure.",
			OwnerTeam:   "sre", Tags: []string{"runbook", "on-call", "operations"},
			Files: map[string]string{
				"SKILL.md": `---
name: runbook-template
description: Author or update a service runbook that on-call engineers can follow under pressure.
license: MIT
metadata:
  author: sre
  sdlc_phase: operations
---

# Runbook template

Every service-level runbook must contain the sections below in this order.
On-call engineers read top-to-bottom under stress; do not bury actionable
information.

## Service summary
One paragraph: what the service does, who owns it, the SLO it commits to,
and the SLO it currently meets.

## On-call expectations
- Pager response time
- Escalation path (primary -> secondary -> service owner -> incident manager)
- Communication channels (the chat room or bridge to join)

## Symptoms and triage
A table mapping alert names to first-response actions. Each row:
- Alert: the exact alert string the on-call sees in pager text.
- First check: the single command or dashboard URL to look at.
- If green: a follow-up question to narrow scope.
- If red: a one-line summary of the most likely cause and the section to jump to.

## Common incidents
For each incident archetype:
- Detection: how it manifests
- Diagnosis: the queries, logs, or dashboards that confirm it
- Mitigation: the immediate action (failover, scale, restart, disable feature)
- Resolution: the fix that closes the underlying cause
- Postmortem trigger: when this incident requires a postmortem

## Standard operations
- Deploy / rollback
- Scale up / down
- Toggle feature flags
- Rotate credentials
Each operation lists the exact command, its expected output, and the
fast-path rollback.

## Known limitations
Things the service deliberately does not handle, with the workaround.

## Last-resort contacts
A short list. Test every contact quarterly.

Quality bar:
- Anyone with platform familiarity but no prior context on this service
  can mitigate a P1 with this runbook alone.
- Every command in the runbook has been executed within the last 90 days.
- No section says "ask the team" without naming a fallback.
`,
				"references/postmortem-template.md": "# Postmortem template\n\n## Summary\n\n## Timeline\n\n## Root cause\n\n## What went well\n\n## What didn't\n\n## Action items\n",
			},
		},
	}
}

func writeTarGz(outPath, srcDir, prefix string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return tw.WriteHeader(&tar.Header{Name: prefix + "/", Mode: 0o755, Typeflag: tar.TypeDir})
		}
		name := prefix + "/" + filepath.ToSlash(rel)
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		hdr.ModTime = info.ModTime()
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		file, err := os.Open(p)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(tw, file)
		return err
	})
}
