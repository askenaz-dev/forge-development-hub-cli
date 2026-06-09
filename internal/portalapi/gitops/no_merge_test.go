package gitops

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClientInterface_HasNoMergePath is the reviewer-style guard for the
// security spine (design D3): the bot is propose-only. It asserts the Client
// interface exposes NO method whose name implies merging/landing a change, so a
// merge path cannot exist anywhere a composer could reach. A reviewer greps for
// a merge path and this test fails the build if one is ever added.
func TestClientInterface_HasNoMergePath(t *testing.T) {
	ct := reflect.TypeOf((*Client)(nil)).Elem()

	forbidden := []string{"merge", "squash", "rebase", "land", "approve", "review", "push", "forcepush"}
	for i := 0; i < ct.NumMethod(); i++ {
		name := strings.ToLower(ct.Method(i).Name)
		for _, bad := range forbidden {
			assert.NotContains(t, name, bad,
				"Client must expose no %q-like method (propose-only, design D3); found %s", bad, ct.Method(i).Name)
		}
	}

	// Positive assertion: the propose-only primitive set is exactly present.
	expected := []string{
		"Enabled", "DefaultBranch", "DefaultBranchSHA", "BranchExists",
		"CreateBranch", "GetFile", "CommitFiles", "FindOpenPR", "OpenPR",
	}
	for _, m := range expected {
		_, ok := ct.MethodByName(m)
		assert.True(t, ok, "Client must expose %s", m)
	}
	assert.Equal(t, len(expected), ct.NumMethod(),
		"Client must expose exactly the propose-only primitives and nothing else")
}

// TestComposers_RecordOnlyProposePrimitives proves that across every composer,
// the fake records only branch/commit/PR primitives — never a merge call (there
// is none to record). It is the behavioral complement to the interface guard.
func TestComposers_RecordOnlyProposePrimitives(t *testing.T) {
	allowed := map[string]struct{}{
		"DefaultBranch": {}, "DefaultBranchSHA": {}, "BranchExists": {},
		"CreateBranch": {}, "GetFile": {}, "CommitFiles": {}, "FindOpenPR": {}, "OpenPR": {},
	}

	// Run one of each composer and inspect the recorded call log.
	t.Run("import", func(t *testing.T) {
		f := seedEditFakes()
		dir := writeValidSkillBundle(t, "card-grid", "A grid.")
		_, err := ComposeImport(context.Background(), f, "skill", "card-grid", dir, ImportMeta{}, testRequestor, nil)
		assertOnlyAllowed(t, f.calls, allowed)
		_ = err
	})
	t.Run("harness", func(t *testing.T) {
		f := seedEditFakes()
		_, _ = ComposeHarness(context.Background(), f, "frontend-team", HarnessEdit{AddRules: []string{"no-any-cast"}}, testRequestor)
		assertOnlyAllowed(t, f.calls, allowed)
	})
	t.Run("curate", func(t *testing.T) {
		f := seedEditFakes()
		yes := true
		_, _ = ComposeCurate(context.Background(), f, "skill", "tech-stack", CurateAction{SetDefault: &yes}, testRequestor)
		assertOnlyAllowed(t, f.calls, allowed)
	})
}

func assertOnlyAllowed(t *testing.T, calls []string, allowed map[string]struct{}) {
	t.Helper()
	for _, c := range calls {
		_, ok := allowed[c]
		assert.True(t, ok, "composer invoked a non-propose primitive %q", c)
	}
}
