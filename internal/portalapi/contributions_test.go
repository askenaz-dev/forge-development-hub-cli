package portalapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitContrib runs a git command in dir with deterministic author/committer
// identity, allowing per-commit overrides via extraEnv (e.g. GIT_AUTHOR_EMAIL).
func gitContrib(t *testing.T, dir string, extraEnv []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func writeContribFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// commitAs stages all changes and commits them authored by the given email at a
// fixed date, so attribution and last_commit ordering are deterministic.
func commitAs(t *testing.T, hub, email, date, msg string) {
	t.Helper()
	gitContrib(t, hub, nil, "add", "-A")
	gitContrib(t, hub, []string{
		"GIT_AUTHOR_EMAIL=" + email,
		"GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_DATE=" + date,
	}, "commit", "-qm", msg)
}

// newContribServer builds a Server pointed at a hub git fixture. The fixture
// has commits by two authors across several component directories so the
// derivation has something real to attribute.
func newContribServer(t *testing.T) *Server {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	hub := t.TempDir()
	gitContrib(t, hub, nil, "init", "-q")

	// Root commit: dev@example.com creates a skill.
	writeContribFile(t, filepath.Join(hub, "skills", "design-system", "SKILL.md"),
		"---\nname: design-system\nversion: 0.1.0\n---\nbody\n")
	commitAs(t, hub, "dev@example.com", "2026-01-01T00:00:00Z", "feat(design-system): add skill")

	// dev@example.com also authors a rule.
	writeContribFile(t, filepath.Join(hub, "rules", "no-console", "RULE.md"),
		"---\nname: no-console\nversion: 0.1.0\n---\nbody\n")
	commitAs(t, hub, "dev@example.com", "2026-02-01T00:00:00Z", "feat(no-console): add rule")

	// dev@example.com revisits the skill (second touching commit → count 2).
	writeContribFile(t, filepath.Join(hub, "skills", "design-system", "SKILL.md"),
		"---\nname: design-system\nversion: 0.2.0\n---\nbody changed\n")
	commitAs(t, hub, "dev@example.com", "2026-03-01T00:00:00Z", "feat(design-system): expand skill")

	// other@example.com authors an agent (must not bleed into dev's list).
	writeContribFile(t, filepath.Join(hub, "agents", "triage", "AGENT.md"),
		"---\nname: triage\nversion: 0.1.0\n---\nbody\n")
	commitAs(t, hub, "other@example.com", "2026-04-01T00:00:00Z", "feat(triage): add agent")

	// A non-component change (registry edit) must attribute to nothing.
	writeContribFile(t, filepath.Join(hub, "hub", "registry.yaml"), "schema_version: 2\n")
	commitAs(t, hub, "dev@example.com", "2026-05-01T00:00:00Z", "chore: touch registry")

	t.Setenv("FDH_PORTAL_HUB_PATH", hub)
	t.Setenv("FDH_PORTAL_REFRESH_INTERVAL", "60s")
	cfg, err := LoadConfig()
	require.NoError(t, err)
	srv, err := New(cfg, BuildInfo{Version: "test"})
	require.NoError(t, err)
	return srv
}

// TestContributionsFor_MatchingEmail proves a matching author email yields the
// right components, with per-directory commit counts and the newest authoring
// time as last_commit, ordered most-recent-first.
func TestContributionsFor_MatchingEmail(t *testing.T) {
	s := newContribServer(t)

	got := s.contributionsFor("dev@example.com")

	byName := map[string]Contribution{}
	for _, c := range got {
		byName[c.Kind+"/"+c.Name] = c
	}
	require.Len(t, got, 2, "dev authored exactly the skill and the rule; the registry-only commit attributes to nothing")
	require.Contains(t, byName, "skill/design-system")
	require.Contains(t, byName, "rule/no-console")
	assert.NotContains(t, byName, "agent/triage", "the agent was authored by another email")

	assert.Equal(t, 2, byName["skill/design-system"].CommitCount, "two commits touched the skill dir")
	assert.Equal(t, 1, byName["rule/no-console"].CommitCount)
	assert.Equal(t, "2026-03-01T00:00:00Z", byName["skill/design-system"].LastCommit, "newest touching commit time")
	assert.Equal(t, "2026-02-01T00:00:00Z", byName["rule/no-console"].LastCommit)

	// Most-recent-activity-first ordering.
	require.Equal(t, "skill/design-system", got[0].Kind+"/"+got[0].Name)
}

// TestContributionsFor_CaseInsensitiveTrimmed proves the email match is
// case-insensitive and trims surrounding whitespace.
func TestContributionsFor_CaseInsensitiveTrimmed(t *testing.T) {
	s := newContribServer(t)
	got := s.contributionsFor("  DEV@Example.COM ")
	require.Len(t, got, 2, "match must be case-insensitive and trimmed")
}

// TestContributionsFor_NonMatchingAndEmpty proves a non-matching email and an
// empty email both yield an empty (non-nil) slice, never an error.
func TestContributionsFor_NonMatchingAndEmpty(t *testing.T) {
	s := newContribServer(t)

	none := s.contributionsFor("nobody@example.com")
	require.NotNil(t, none)
	assert.Empty(t, none)

	empty := s.contributionsFor("   ")
	require.NotNil(t, empty)
	assert.Empty(t, empty)
}

// TestContributionsFor_MemoizesOnHead proves the derivation memoizes by the hub
// HEAD commit hash: a second call without a HEAD change reuses the cached index
// (no re-walk), and the cache invalidates only when HEAD advances.
func TestContributionsFor_MemoizesOnHead(t *testing.T) {
	s := newContribServer(t)

	// First call populates the cache.
	_ = s.contributionsFor("dev@example.com")
	s.contribMu.Lock()
	first := s.contribCache
	s.contribMu.Unlock()
	require.NotNil(t, first, "first call must populate the cache")
	firstHead := first.head

	// Second call with no HEAD change must return the SAME cached index pointer
	// (proving it did not re-walk and rebuild).
	_ = s.contributionsFor("other@example.com")
	s.contribMu.Lock()
	second := s.contribCache
	s.contribMu.Unlock()
	require.Same(t, first, second, "no HEAD change → same memoized index, no re-walk")

	// Advance HEAD with a new commit, then confirm the cache is recomputed
	// (a different index instance keyed on the new HEAD).
	hub := s.cfg.HubPath
	writeContribFile(t, filepath.Join(hub, "hooks", "guard", "HOOK.md"),
		"---\nname: guard\nversion: 0.1.0\n---\nbody\n")
	commitAs(t, hub, "dev@example.com", "2026-06-01T00:00:00Z", "feat(guard): add hook")

	got := s.contributionsFor("dev@example.com")
	s.contribMu.Lock()
	third := s.contribCache
	s.contribMu.Unlock()
	require.NotSame(t, first, third, "HEAD advanced → cache recomputed")
	assert.NotEqual(t, firstHead, third.head, "cache key tracks the new HEAD")
	assert.Len(t, got, 3, "the new hook now appears for dev")
}
