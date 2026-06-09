package portalapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slogDiscard returns a logger that drops everything — keeps test output clean.
func slogDiscard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestGuardrail_TelemetryNeverWritesCatalogConfig is the §12.1 privacy guardrail:
// no telemetry code path may mutate hub/registry.yaml, hub/harnesses.yaml, or any
// component directory ("code = source of truth"). We point the server's HubPath
// at a sentinel hub tree, drive the telemetry ingest + activation + admin-read +
// aggregate paths, and assert every catalog file is byte-for-byte unchanged.
func TestGuardrail_TelemetryNeverWritesCatalogConfig(t *testing.T) {
	hub := t.TempDir()
	// A minimal catalog CONFIG surface the guardrail protects.
	files := map[string]string{
		filepath.Join(hub, "hub", "registry.yaml"):                "schema_version: 2\nregistry: forge-development-hub\n",
		filepath.Join(hub, "hub", "harnesses.yaml"):               "harnesses: []\n",
		filepath.Join(hub, "skills", "design-system", "SKILL.md"): "---\nname: design-system\n---\nbody\n",
	}
	for p, content := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	before := snapshotTree(t, hub)

	// A server whose telemetry uses the in-memory fake (no DB needed) and whose
	// HubPath is the protected tree.
	fs := newFakeStore()
	s := &Server{
		telemetry:     fs,
		ingestLimiter: newIngestLimiter(time.Minute, 1000),
		logger:        slogDiscard(),
	}
	s.cfg.HubPath = hub

	ctx := context.Background()

	// 1) Ingest a telemetry event.
	iw := httptest.NewRecorder()
	s.handlePostTelemetry(iw, httptest.NewRequest(http.MethodPost, "/api/v1/telemetry",
		strings.NewReader(`{"event":"install","kind":"skill","namespace":"forge","name":"design-system","os":"linux","locale":"en"}`)))
	require.Equal(t, http.StatusAccepted, iw.Code)

	// 2) Ingest an activation event.
	aw := httptest.NewRecorder()
	s.handlePostActivation(aw, httptest.NewRequest(http.MethodPost, "/api/v1/activation",
		strings.NewReader(`{"step":"done","wizard_session_id":"s1","locale":"en","os":"linux"}`)))
	require.Equal(t, http.StatusOK, aw.Code)

	// 3) Run an aggregation/retention pass (no-op on the fake, but exercises the path).
	_ = fs.Aggregate(ctx, time.Hour)

	// Every catalog CONFIG file must be untouched.
	after := snapshotTree(t, hub)
	assert.Equal(t, before, after, "telemetry paths must not mutate any catalog CONFIG file")
}

// snapshotTree returns a map of relative path -> content for every file under
// root, so two snapshots can be compared for any mutation/addition/deletion.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	require.NoError(t, err)
	return out
}

// TestGuardrail_OrdinalElection pins the lowest-ordinal aggregation election
// (design D6): only ordinal 0 (or an undeterminable/local hostname) is elected;
// any non-zero StatefulSet ordinal is de-elected so aggregates are not
// double-written across replicas.
func TestGuardrail_OrdinalElection(t *testing.T) {
	cases := []struct {
		podName string
		elected bool
	}{
		{"fdh-portal-api-0", true},
		{"fdh-portal-api-1", false},
		{"fdh-portal-api-10", false},
		{"laptop", true},    // no ordinal suffix → single/local → elect
		{"some-host", true}, // trailing non-numeric → undeterminable → elect
		{"", true},          // empty → undeterminable → elect
	}
	for _, c := range cases {
		t.Run(c.podName, func(t *testing.T) {
			t.Setenv("POD_NAME", c.podName)
			t.Setenv("FDH_TELEMETRY_AGGREGATE", "") // no override
			got := isLowestOrdinalReplica()
			assert.Equal(t, c.elected, got, "election for pod %q", c.podName)
		})
	}

	// Explicit override wins over the hostname heuristic.
	t.Setenv("POD_NAME", "fdh-portal-api-3")
	t.Setenv("FDH_TELEMETRY_AGGREGATE", "1")
	assert.True(t, isLowestOrdinalReplica(), "override=1 forces election even on ordinal 3")
	t.Setenv("FDH_TELEMETRY_AGGREGATE", "0")
	assert.False(t, isLowestOrdinalReplica(), "override=0 forces de-election even on ordinal 0-ish")
}
