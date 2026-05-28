package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/cli"
)

// Golden-file tests pin the JSON shape of each command's --json output.
// The actual VALUES vary per install (hashes, timestamps), so we compare
// the SHAPE (the set of keys) rather than the byte content.
//
// Any change to a JSON tag in cli/*.go will fail one of these tests and
// must be reviewed: the JSON shape is part of the CLI's public contract.

func keysOf(t *testing.T, v any) map[string]bool {
	t.Helper()
	buf, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(buf, &m))
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func TestGolden_InstallResultShape(t *testing.T) {
	r := cli.InstallResult{}
	keys := keysOf(t, r)
	want := []string{"skill", "namespace", "name", "version", "content_hash", "scope", "registry", "target_agents", "writes"}
	for _, k := range want {
		assert.True(t, keys[k], "InstallResult missing JSON key %q (locked by golden test)", k)
	}
}

func TestGolden_InstallWriteInfoShape(t *testing.T) {
	r := cli.InstallWriteInfo{}
	keys := keysOf(t, r)
	want := []string{"path", "agents"}
	for _, k := range want {
		assert.True(t, keys[k], "InstallWriteInfo missing JSON key %q", k)
	}
}

func TestGolden_ListedSkillShape(t *testing.T) {
	r := cli.ListedSkill{}
	keys := keysOf(t, r)
	want := []string{"skill", "namespace", "name", "version", "source", "scope", "path", "target_agents", "content_hash"}
	for _, k := range want {
		assert.True(t, keys[k], "ListedSkill missing JSON key %q", k)
	}
}

func TestGolden_DoctorReportShape(t *testing.T) {
	r := cli.DoctorReport{}
	keys := keysOf(t, r)
	want := []string{"installer_version", "home_dir", "registry", "agents", "issues"}
	for _, k := range want {
		assert.True(t, keys[k], "DoctorReport missing JSON key %q", k)
	}
}

func TestGolden_RegistryHealthShape(t *testing.T) {
	// Existing fields are stable; new fields (kind, transport) are
	// optional/additive — they only appear in the JSON when set.
	stable := cli.RegistryHealth{Configured: true, Source: "x", Reachable: true}
	keys := keysOf(t, stable)
	for _, k := range []string{"configured", "source", "reachable"} {
		assert.True(t, keys[k], "RegistryHealth missing stable JSON key %q", k)
	}
	assert.False(t, keys["kind"], "kind must be omitempty when zero")
	assert.False(t, keys["transport"], "transport must be omitempty when zero")

	// When set, the new fields surface under their documented names.
	full := cli.RegistryHealth{
		Configured: true, Source: "x", Reachable: true,
		Kind: "http", Transport: "http v1",
	}
	keysFull := keysOf(t, full)
	assert.True(t, keysFull["kind"])
	assert.True(t, keysFull["transport"])
}

func TestGolden_SearchHitShape(t *testing.T) {
	r := cli.SearchHit{}
	keys := keysOf(t, r)
	want := []string{"namespace", "name", "description", "owner_team", "latest_version", "latest_hash", "scan_status"}
	for _, k := range want {
		assert.True(t, keys[k], "SearchHit missing JSON key %q", k)
	}
}
