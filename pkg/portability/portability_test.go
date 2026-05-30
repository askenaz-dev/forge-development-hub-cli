package portability_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/bundle"
	"github.com/forge/fdh/pkg/portability"
)

func mkBundle(t *testing.T, name, skillMD string) *bundle.Bundle {
	t.Helper()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644))
	b, err := bundle.Load(dir)
	require.NoError(t, err)
	return b
}

func ruleIDs(findings []portability.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.RuleID)
	}
	return out
}

func TestLint_PortableHappyPath(t *testing.T) {
	b := mkBundle(t, "ok", `---
name: ok
description: portable skill should pass
license: MIT
metadata:
  author: team-x
---
Plain body, nothing fancy.
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Empty(t, findings)
}

func TestLint_PortableFrontmatterAllowedToolsRejected(t *testing.T) {
	b := mkBundle(t, "claudish", `---
name: claudish
description: tries to use a Claude-only field
allowed-tools: Read Grep
---
body
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT100")
}

func TestLint_PortableFrontmatterDisableModelInvocationRejected(t *testing.T) {
	b := mkBundle(t, "claudish", `---
name: claudish
description: tries to use a Claude-only field
disable-model-invocation: true
---
body
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT100")
}

func TestLint_PortableFrontmatterContextForkRejected(t *testing.T) {
	b := mkBundle(t, "claudish", `---
name: claudish
description: x
context: fork
---
body
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT100")
}

func TestLint_PortableFrontmatterWhenToUseRejected(t *testing.T) {
	b := mkBundle(t, "claudish", `---
name: claudish
description: x
when_to_use: |
  some triggers
---
body
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT100")
}

func TestLint_PortableMetadataAllowed(t *testing.T) {
	b := mkBundle(t, "with-metadata", `---
name: with-metadata
description: portable with metadata block
metadata:
  author: someone
  version: "1.0"
---
body
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Empty(t, findings)
}

func TestLint_PortableInstalledFromAllowed(t *testing.T) {
	b := mkBundle(t, "post-install", `---
name: post-install
description: simulated post-install
installed_from: https://reg.internal/security/owasp@1.0.0
---
body
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Empty(t, findings)
}

func TestLint_PortableVersionAllowed(t *testing.T) {
	b := mkBundle(t, "versioned", `---
name: versioned
description: portable with a SemVer version
version: 0.1.0
---
body
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Empty(t, findings)
}

func TestLint_ArgumentsInBodyRejected(t *testing.T) {
	b := mkBundle(t, "argy", `---
name: argy
description: uses $ARGUMENTS in body
---
Run task $ARGUMENTS now.
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT200")
}

func TestLint_ClaudeSkillDirInBodyRejected(t *testing.T) {
	b := mkBundle(t, "skillvar", `---
name: skillvar
description: uses Claude env var
---
The script lives at ${CLAUDE_SKILL_DIR}/scripts/run.sh.
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT202")
}

func TestLint_PositionalArgs(t *testing.T) {
	b := mkBundle(t, "pos", `---
name: pos
description: uses $0 positional
---
Fix issue $0 with priority $1.
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT203")
}

func TestLint_CurrencyNotFalseFlagged(t *testing.T) {
	b := mkBundle(t, "money", `---
name: money
description: mentions currency, should not trigger
---
The total is $0.00 plus tax. The discount is $5.50.
`)
	findings := portability.Lint(b, portability.LintOptions{})
	// Currency-like patterns must not trigger PORT203.
	for _, f := range findings {
		assert.NotEqual(t, "PORT203", f.RuleID, "false positive on currency: %+v", f)
	}
}

func TestLint_InlineDynamicInjectionRejected(t *testing.T) {
	b := mkBundle(t, "inj", "---\nname: inj\ndescription: uses !`diff`\n---\nDiff: !`git diff HEAD`\n")
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT300")
}

func TestLint_FencedDynamicInjectionRejected(t *testing.T) {
	b := mkBundle(t, "fence", "---\nname: fence\ndescription: uses fenced injection\n---\n```!\nnode --version\n```\n")
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT301")
}

func TestLint_ConservativeArgumentsInsideFencedTextStillRejected(t *testing.T) {
	// Per the spec, the conservative position is intentional for v1:
	// if $ARGUMENTS appears anywhere in the body, the lint fails.
	b := mkBundle(t, "doc", "---\nname: doc\ndescription: documents the syntax\n---\nExample (do not use): `$ARGUMENTS` interpolation.\n")
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT200")
}

func TestLint_NonPortableMissingCompatibility(t *testing.T) {
	b := mkBundle(t, "noncompat", `---
name: noncompat
description: forgot compatibility list
portable: false
---
body
`)
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Contains(t, ruleIDs(findings), "PORT400")
}

func TestLint_NonPortableUnknownAgentInCompatibility(t *testing.T) {
	b := mkBundle(t, "noncompat", `---
name: noncompat
description: refers to nonexistent agent
portable: false
compatibility:
  - claude-code
  - jetbrains-junie
---
body
`)
	findings := portability.Lint(b, portability.LintOptions{
		KnownAgentIDs: []string{"claude-code", "copilot", "codex", "opencode"},
	})
	assert.Contains(t, ruleIDs(findings), "PORT401")
}

func TestLint_NonPortableKnownCompatibilityPasses(t *testing.T) {
	b := mkBundle(t, "claudish", `---
name: claudish
description: Claude-only skill
portable: false
compatibility:
  - claude-code
allowed-tools: Read Grep
---
The script is at ${CLAUDE_SKILL_DIR}.
Run $ARGUMENTS.
`)
	findings := portability.Lint(b, portability.LintOptions{
		KnownAgentIDs: []string{"claude-code", "copilot", "codex", "opencode"},
	})
	// Non-portable skills are not subject to the portable allowlist or the
	// substitution / injection rules — only compatibility rules apply.
	for _, f := range findings {
		assert.Equal(t, "PORT401", f.RuleID, "unexpected finding in non-portable skill: %+v", f)
	}
}

func TestLint_FindingsCarryLineNumbers(t *testing.T) {
	b := mkBundle(t, "linenums", "---\nname: linenums\ndescription: line numbering\n---\nfirst line\nsecond line uses $ARGUMENTS here\nthird line\n")
	findings := portability.Lint(b, portability.LintOptions{})
	require.NotEmpty(t, findings)
	var argFinding *portability.Finding
	for i, f := range findings {
		if f.RuleID == "PORT200" {
			argFinding = &findings[i]
			break
		}
	}
	require.NotNil(t, argFinding)
	assert.Equal(t, 2, argFinding.Line, "expected line 2 for $ARGUMENTS occurrence")
}

func TestLint_NilBundle(t *testing.T) {
	findings := portability.Lint(nil, portability.LintOptions{})
	require.Len(t, findings, 1)
	assert.Equal(t, "PORT000", findings[0].RuleID)
}

func TestHasErrorsAndFormat(t *testing.T) {
	f := []portability.Finding{
		{RuleID: "PORT200", Severity: "error", Path: "SKILL.md", Line: 4, Message: "x"},
	}
	assert.True(t, portability.HasErrors(f))
	out := portability.Format(f)
	assert.Contains(t, out, "PORT200")
	assert.Contains(t, out, "SKILL.md:4")
}
