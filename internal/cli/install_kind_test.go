package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/testutil"
	"github.com/forge/fdh/pkg/registry"
)

func kindReg(t *testing.T) registry.Registry {
	t.Helper()
	root := t.TempDir()
	testutil.BuildKindRegistry(t, root, []testutil.ComponentSpec{
		{
			Kind: "skill", Namespace: "dx", Name: "spec-flow", Version: "1.0.0",
			Description: "Spec flow.", OwnerTeam: "dx",
			Files: map[string]string{"SKILL.md": testutil.FixtureSKILLMD("spec-flow", "Spec flow.")},
		},
		{
			Kind: "rule", Namespace: "dx", Name: "no-console-log", Version: "1.0.0",
			Description: "No console.log.", OwnerTeam: "dx",
			Files: map[string]string{"RULE.md": "---\nname: no-console-log\n---\n\nbody\n"},
		},
		{
			// Same name as the rule but a different kind ⇒ ambiguous.
			Kind: "hook", Namespace: "dx", Name: "no-console-log", Version: "1.0.0",
			Description: "Hook clash.", OwnerTeam: "dx",
			Files: map[string]string{"HOOK.md": "---\nname: no-console-log\n---\n\nbody\n"},
		},
	})
	return &registry.GitRegistry{LocalPath: root, SkipFetch: true}
}

func TestResolveComponentKind_FlagWins(t *testing.T) {
	kind, err := resolveComponentKind(context.Background(), kindReg(t), "rule", "dx", "no-console-log")
	require.NoError(t, err)
	assert.Equal(t, "rule", kind)
}

func TestResolveComponentKind_FlagValidated(t *testing.T) {
	_, err := resolveComponentKind(context.Background(), kindReg(t), "banana", "dx", "x")
	require.Error(t, err)
}

func TestResolveComponentKind_InferredUnique(t *testing.T) {
	kind, err := resolveComponentKind(context.Background(), kindReg(t), "", "dx", "spec-flow")
	require.NoError(t, err)
	assert.Equal(t, "skill", kind)
}

func TestResolveComponentKind_AmbiguousNeedsFlag(t *testing.T) {
	_, err := resolveComponentKind(context.Background(), kindReg(t), "", "dx", "no-console-log")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}

func TestResolveComponentKind_NotFoundDefaultsSkill(t *testing.T) {
	// Unknown name falls back to skill so the downstream manifest read
	// produces the precise not-found diagnostic.
	kind, err := resolveComponentKind(context.Background(), kindReg(t), "", "dx", "does-not-exist")
	require.NoError(t, err)
	assert.Equal(t, "skill", kind)
}
