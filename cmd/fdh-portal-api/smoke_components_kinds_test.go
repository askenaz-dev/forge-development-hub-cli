//go:build smoke

// Subprocess smoke test guarding the UI catalog API (`/api/v1/components`)
// against a silent regression to "skills only". The portal frontend renders
// its /rules, /agents, /hooks tabs by calling
// `GET /api/v1/components?kind=<kind>`; if a deploy ever serves a stale image
// or a stale hub checkout, those tabs go empty while the unit suite (which
// runs against in-process fixtures) stays green. This test starts the real
// `fdh-portal-api` binary against the four-kind hub fixture and asserts every
// non-skill kind view returns at least one component.
//
// Run with:
//
//	go test -tags=smoke ./cmd/fdh-portal-api/...
//
// Reuses the helpers (repoRoot, buildBinary, pickFreePort, waitForReady,
// getOK, httpResult) defined in smoke_subprocess_test.go — same package, same
// build tag.
package main_test

import (
	"bytes"
	"encoding/json"
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/testutil"
)

// componentsPage mirrors the shape handleListComponents emits:
// {"items": [ {kind, namespace, name, ...}, ... ], "next_cursor": ...}.
type componentsPage struct {
	Items      []map[string]any `json:"items"`
	NextCursor any              `json:"next_cursor"`
}

func TestSmoke_PortalAPI_ServesAllKinds(t *testing.T) {
	root := repoRoot(t)
	bin := buildBinary(t, root)
	port := pickFreePort(t)
	addr := "127.0.0.1:" + port
	base := "http://" + addr

	env := []string{
		"FDH_PORTAL_API_ADDR=" + addr,
		"FDH_PORTAL_HUB_PATH=" + testutil.HubFixturePath(),
		// LoadConfig requires at least one registry source; point the Git
		// path at an empty tempdir so nothing tries to clone.
		"FDH_PORTAL_REGISTRY_LOCAL_PATH=" + t.TempDir(),
		"FDH_PORTAL_REFRESH_INTERVAL=60s",
	}
	for _, kv := range os.Environ() {
		k := strings.SplitN(kv, "=", 2)[0]
		switch strings.ToUpper(k) {
		case "PATH", "SYSTEMROOT", "TEMP", "TMP":
			env = append(env, kv)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	waitForReady(t, base, 10*time.Second)
	client := &http.Client{Timeout: 5 * time.Second}

	// The fixture hub (internal/testutil/fixtures/hub) declares exactly one
	// component of each kind. Every non-skill kind MUST be served — this is
	// the regression guard for the "skills only" symptom.
	for _, kind := range []string{"skill", "rule", "agent", "hook"} {
		page := getComponentsByKind(t, client, base, kind)
		require.GreaterOrEqualf(t, len(page.Items), 1,
			"GET /api/v1/components?kind=%s must return >=1 item (fixture has one of each kind); "+
				"an empty result means the portal collapsed to skills-only", kind)
		for _, item := range page.Items {
			require.Equalf(t, kind, item["kind"],
				"kind filter leaked a %v into the %s view", item["kind"], kind)
		}
	}

	// A kind absent from the hub is not an error: filtering by a valid kind
	// with no components returns 200 + empty items, never a 4xx/5xx. (The
	// fixture has every kind, so we assert the *shape* here, not emptiness:
	// an unfiltered list returns all four kinds.)
	all := getComponentsByKind(t, client, base, "")
	seen := map[string]bool{}
	for _, item := range all.Items {
		if k, ok := item["kind"].(string); ok {
			seen[k] = true
		}
	}
	for _, kind := range []string{"skill", "rule", "agent", "hook"} {
		require.Truef(t, seen[kind], "unfiltered catalog must include kind %q", kind)
	}
}

// getComponentsByKind GETs /api/v1/components (optionally filtered by kind)
// and decodes the page. An empty kind requests the unfiltered catalog.
func getComponentsByKind(t *testing.T, client *http.Client, base, kind string) componentsPage {
	t.Helper()
	url := base + "/api/v1/components"
	if kind != "" {
		url += "?kind=" + kind
	}
	res := getOK(t, client, url)
	var page componentsPage
	dec := json.NewDecoder(bytes.NewReader(res.body))
	require.NoErrorf(t, dec.Decode(&page), "decoding %s", url)
	return page
}
