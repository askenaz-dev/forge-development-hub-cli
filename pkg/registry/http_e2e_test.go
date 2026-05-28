//go:build e2e

// Package registry's HTTP E2E test exercises the full wire-protocol flow
// (Index → Manifest → FetchBundle → Search) against an httptest.Server
// serving a real fixture registry tree. Per-method unit tests live in
// http_test.go; this file demonstrates the path a CLI command takes from
// "user runs fdh install" through to a verified extracted bundle.
//
// Gated behind `go:build e2e` so `task test` stays focused on unit tests
// and `task e2e` runs the full suite.
package registry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/testutil"
	"github.com/forge/fdh/pkg/registry"
)

func TestE2E_HTTP_FullFlow(t *testing.T) {
	root := t.TempDir()
	testutil.BuildRegistry(t, root, []testutil.SkillSpec{
		{
			Namespace:   "security",
			Name:        "owasp-quick-review",
			Version:     "1.0.0",
			Description: "OWASP top-10 quick review.",
			OwnerTeam:   "appsec",
			Tags:        []string{"owasp", "security"},
			Files: map[string]string{
				"SKILL.md":             testutil.FixtureSKILLMD("owasp-quick-review", "OWASP top-10 quick review."),
				"references/owasp.md":  "Top 10 ...",
			},
		},
		{
			Namespace:   "code-review",
			Name:        "standard",
			Version:     "2.1.0",
			Description: "Standard code review checklist.",
			OwnerTeam:   "dx",
			Tags:        []string{"review"},
			Files: map[string]string{
				"SKILL.md": testutil.FixtureSKILLMD("standard", "Standard code review checklist."),
			},
		},
	})

	srv := httptest.NewServer(http.FileServer(http.Dir(root)))
	t.Cleanup(srv.Close)

	r := &registry.HTTPRegistry{
		BaseURL:    srv.URL + "/",
		APIVersion: "v1",
		CacheDir:   t.TempDir(),
	}

	ctx := context.Background()

	idx, err := r.Index(ctx)
	require.NoError(t, err)
	require.Len(t, idx.Skills, 2, "Index should enumerate both fixture skills")

	m, err := r.Manifest(ctx, "security", "owasp-quick-review")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", m.Latest)
	require.NotNil(t, m.FindVersion("1.0.0"))

	bp, err := r.FetchBundle(ctx, "security", "owasp-quick-review", "1.0.0")
	require.NoError(t, err, "FetchBundle should succeed on a self-consistent fixture")
	t.Cleanup(func() { _ = bp.Cleanup() })
	assert.NotEmpty(t, bp.Hash)
	assert.FileExists(t, filepath.Join(bp.Path, "SKILL.md"))
	assert.FileExists(t, filepath.Join(bp.Path, "references", "owasp.md"))

	hits, err := r.Search(ctx, "owasp")
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "owasp-quick-review", hits[0].Name)
}
