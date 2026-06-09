package gitops

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// genTestPEM returns a fresh RSA private key in PKCS#1 PEM. ghinstallation
// requires a real RSA key to sign the App JWT; this keeps the test self-contained.
func genTestPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
}

func TestConfigured(t *testing.T) {
	full := Config{AppID: 1, InstallationID: 2, PrivateKeyPEM: []byte("x"), Owner: "o", Repo: "r"}
	assert.True(t, full.Configured())

	cases := []Config{
		{InstallationID: 2, PrivateKeyPEM: []byte("x"), Owner: "o", Repo: "r"}, // no AppID
		{AppID: 1, PrivateKeyPEM: []byte("x"), Owner: "o", Repo: "r"},          // no InstallationID
		{AppID: 1, InstallationID: 2, Owner: "o", Repo: "r"},                   // no key
		{AppID: 1, InstallationID: 2, PrivateKeyPEM: []byte("x"), Repo: "r"},   // no owner
		{AppID: 1, InstallationID: 2, PrivateKeyPEM: []byte("x"), Owner: "o"},  // no repo
	}
	for i, c := range cases {
		assert.Falsef(t, c.Configured(), "case %d must be unconfigured", i)
	}
}

func TestNew_MissingEnvReturnsDisabledClient(t *testing.T) {
	c, err := New(Config{}) // nothing configured
	require.NoError(t, err, "missing App must NOT fail construction (boots dark)")
	assert.False(t, c.Enabled())

	// Every method returns the typed not-configured error.
	_, derr := c.DefaultBranchSHA(context.Background())
	assert.ErrorIs(t, derr, ErrGitopsNotConfigured)
}

func TestConfigFromEnv_DefaultsAndBase64Key(t *testing.T) {
	pemBytes := genTestPEM(t)
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "456")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", base64.StdEncoding.EncodeToString(pemBytes))
	// Owner/Repo unset → defaults.
	t.Setenv("GITHUB_HUB_OWNER", "")
	t.Setenv("GITHUB_HUB_REPO", "")

	cfg, err := ConfigFromEnv()
	require.NoError(t, err)
	assert.Equal(t, int64(123), cfg.AppID)
	assert.Equal(t, int64(456), cfg.InstallationID)
	assert.Equal(t, "askenaz-dev", cfg.Owner)
	assert.Equal(t, "forge-development-hub", cfg.Repo)
	assert.True(t, strings.HasPrefix(string(cfg.PrivateKeyPEM), "-----BEGIN"), "base64 PEM decoded to raw PEM")
	assert.True(t, cfg.Configured())
}

func TestConfigFromEnv_MalformedAppIDErrors(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "not-a-number")
	_, err := ConfigFromEnv()
	require.Error(t, err)
}

// fakeGitHub is a tiny in-memory GitHub REST stand-in exercising the real
// githubClient's Git Data flow end to end (default branch → ref SHA → blob →
// tree → commit → update-ref → open PR), so the atomic-commit machinery is
// covered without a network. It also asserts NO merge endpoint is ever hit.
func TestGithubClient_AtomicCommitAndPR(t *testing.T) {
	var hitMerge bool
	mux := http.NewServeMux()

	// GET /repos/o/r  → default_branch
	mux.HandleFunc("GET /repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
	})
	// GET /repos/o/r/git/ref/heads/main → tip SHA
	mux.HandleFunc("GET /repos/o/r/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": "tipsha"}})
	})
	mux.HandleFunc("GET /repos/o/r/git/commits/tipsha", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tree": map[string]string{"sha": "basetree"}})
	})
	mux.HandleFunc("POST /repos/o/r/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blobsha"})
	})
	mux.HandleFunc("POST /repos/o/r/git/trees", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "newtree"})
	})
	mux.HandleFunc("POST /repos/o/r/git/commits", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "newcommit"})
	})
	var refUpdated bool
	mux.HandleFunc("PATCH /repos/o/r/git/refs/heads/web/import/skill/x", func(w http.ResponseWriter, r *http.Request) {
		refUpdated = true
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": "newcommit"}})
	})
	mux.HandleFunc("POST /repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"html_url": "https://example/pull/1"})
	})
	// A merge attempt would hit PUT .../pulls/{n}/merge — assert it never does.
	mux.HandleFunc("/repos/o/r/pulls/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/merge") {
			hitMerge = true
		}
		w.WriteHeader(http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	g := &githubClient{http: srv.Client(), apiBase: srv.URL, owner: "o", repo: "r"}

	ctx := context.Background()
	branch := "web/import/skill/x"
	sha, err := g.DefaultBranchSHA(ctx)
	require.NoError(t, err)
	assert.Equal(t, "tipsha", sha)

	newSHA, err := g.CommitFiles(ctx, branch, sha, []FileChange{
		{Path: "skills/x/SKILL.md", Content: []byte("hello")},
		{Path: "skills/x/old.md", Delete: true},
	}, "feat(x): add skill")
	require.NoError(t, err)
	assert.Equal(t, "newcommit", newSHA)
	assert.True(t, refUpdated, "branch ref fast-forwarded to the new commit")

	url, err := g.OpenPR(ctx, branch, "main", "feat(x): add skill", "body")
	require.NoError(t, err)
	assert.Equal(t, "https://example/pull/1", url)

	assert.False(t, hitMerge, "the client must NEVER call a merge endpoint (propose-only)")
}

func TestGithubClient_GetFileBase64Decode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/contents/hub/registry.yaml", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"content":  base64.StdEncoding.EncodeToString([]byte("schema_version: 2\n")),
			"encoding": "base64",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	g := &githubClient{http: srv.Client(), apiBase: srv.URL, owner: "o", repo: "r"}

	data, found, err := g.GetFile(context.Background(), "hub/registry.yaml", "main")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "schema_version: 2\n", string(data))

	// A missing file is (nil, false, nil).
	_, found, err = g.GetFile(context.Background(), "hub/nope.yaml", "main")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestGithubClient_FindOpenPR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "web%2Fcurate") || strings.Contains(r.URL.RawQuery, "web/curate") {
			_ = json.NewEncoder(w).Encode([]map[string]string{{"html_url": "https://example/pull/5"}})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	g := &githubClient{http: srv.Client(), apiBase: srv.URL, owner: "o", repo: "r"}

	url, found, err := g.FindOpenPR(context.Background(), "web/curate/skill/x")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "https://example/pull/5", url)

	_, found, err = g.FindOpenPR(context.Background(), "web/import/skill/y")
	require.NoError(t, err)
	assert.False(t, found)
}
