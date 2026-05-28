//go:build smoke

// Subprocess smoke test for the HTTP registry transport. Builds the
// `fdh` binary, serves a fixture registry over HTTP via httptest, and
// drives the binary through `config set` → `search` → `doctor` against
// it. Verifies that the dispatcher chose HTTPRegistry, that the wire
// protocol round-trips end-to-end, and that the on-disk cache lands at
// the expected path.
//
// This is the Windows-runnable equivalent of the canonical Mac smoke
// described in the implement-http-registry-consumer change, Section 17.
// Run it with:
//
//	go test -tags=smoke ./cmd/fdh/...
//
// Skipped under the default `go test ./...` to keep the unit suite fast.
package main_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/testutil"
)

// repoRoot walks up from the test binary's working directory until it
// finds go.mod. Returns the directory containing it.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	cur := wd
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			t.Fatalf("go.mod not found above %s", wd)
		}
		cur = parent
	}
}

// buildBinary builds cmd/fdh into a temp dir and returns the binary path.
func buildBinary(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	outDir := t.TempDir()
	bin := filepath.Join(outDir, "fdh")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/fdh")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "go build failed: %s", out)
	return bin
}

// runFDH invokes the binary with isolated env (no leakage from the
// developer's real config / cache / home) and returns combined output.
func runFDH(t *testing.T, bin string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// isolatedEnv builds an env slice that points all per-user dirs at temp
// locations and pre-creates fake agent dirs so adapter probes detect at
// least one agent (Claude Code) — doctor refuses to flag the agent
// section as unhealthy when at least one is present.
func isolatedEnv(t *testing.T) (env []string, paths struct {
	Home      string
	AppData   string
	LocalApp  string
	CacheDir  string
	ConfigDir string
}) {
	t.Helper()
	paths.Home = t.TempDir()
	paths.AppData = t.TempDir()
	paths.LocalApp = t.TempDir()
	paths.ConfigDir = filepath.Join(paths.AppData, "fdh")
	paths.CacheDir = filepath.Join(paths.LocalApp, "fdh", "http-cache")
	for _, sub := range []string{".claude", ".agents", ".copilot", ".config/opencode"} {
		require.NoError(t, os.MkdirAll(filepath.Join(paths.Home, sub), 0o755))
	}

	// Strip inherited PATH-affecting vars to keep the test isolated, but
	// keep PATH itself so `fdh` can find auxiliary binaries (none today,
	// but a guard against future deps).
	base := []string{}
	for _, kv := range os.Environ() {
		k := strings.SplitN(kv, "=", 2)[0]
		switch strings.ToUpper(k) {
		case "PATH", "SYSTEMROOT", "TEMP", "TMP", "USERNAME", "USER":
			base = append(base, kv)
		}
	}
	base = append(base,
		"HOME="+paths.Home,
		"USERPROFILE="+paths.Home,
		"APPDATA="+paths.AppData,
		"LOCALAPPDATA="+paths.LocalApp,
		"XDG_CONFIG_HOME="+paths.AppData,
		"XDG_CACHE_HOME="+paths.LocalApp,
	)
	return base, paths
}

func TestSmoke_HTTPRegistry_DoctorReachable(t *testing.T) {
	bin := buildBinary(t)

	// 1. Fixture registry served over HTTP.
	regRoot := t.TempDir()
	testutil.BuildRegistry(t, regRoot, []testutil.SkillSpec{
		{
			Namespace:   "security",
			Name:        "owasp-quick-review",
			Version:     "1.0.0",
			Description: "OWASP top-10 quick review.",
			OwnerTeam:   "appsec",
			Tags:        []string{"owasp", "security"},
			Files: map[string]string{
				"SKILL.md": testutil.FixtureSKILLMD("owasp-quick-review", "OWASP top-10 quick review."),
			},
		},
	})
	srv := httptest.NewServer(http.FileServer(http.Dir(regRoot)))
	t.Cleanup(srv.Close)

	env, _ := isolatedEnv(t)

	// 2. Configure the registry URL — registry.kind=auto should pick HTTP
	// because the URL is https/http without a .git suffix.
	out, err := runFDH(t, bin, env, "config", "set", "registry.url", srv.URL+"/")
	require.NoErrorf(t, err, "config set: %s", out)

	// 3. Verify config get round-trips.
	out, err = runFDH(t, bin, env, "config", "get", "registry.url")
	require.NoErrorf(t, err, "config get: %s", out)
	assert.Contains(t, out, srv.URL)

	// 4. Search exercises Index() through the binary.
	out, err = runFDH(t, bin, env, "search", "owasp")
	require.NoErrorf(t, err, "search: %s", out)
	assert.Contains(t, out, "owasp-quick-review")

	// 5. Doctor must surface the transport line and "reachable".
	out, err = runFDH(t, bin, env, "doctor")
	// Doctor exits 1 if it reports any error-severity issue; on a fresh
	// home with no real agents installed, the agent section may produce
	// warnings but no errors. Tolerate exit 1 when the message itself
	// proves the transport classification + reachability worked.
	if err != nil {
		t.Logf("doctor stderr/stdout (exit=%v):\n%s", err, out)
	}
	assert.Contains(t, out, "transport: http v1", "doctor should print the HTTP transport line")
	assert.Contains(t, out, "[reachable]", "doctor should mark the registry reachable")
	assert.Contains(t, out, "http:"+srv.URL+"/?api=v1", "doctor should include the canonical Source() string")
}

func TestSmoke_HTTPRegistry_InstallMaterializesSkill(t *testing.T) {
	bin := buildBinary(t)

	regRoot := t.TempDir()
	testutil.BuildRegistry(t, regRoot, []testutil.SkillSpec{
		{
			Namespace:   "security",
			Name:        "owasp-quick-review",
			Version:     "1.0.0",
			Description: "OWASP top-10 quick review.",
			OwnerTeam:   "appsec",
			Files: map[string]string{
				"SKILL.md": testutil.FixtureSKILLMD("owasp-quick-review", "OWASP top-10 quick review."),
			},
		},
	})
	srv := httptest.NewServer(http.FileServer(http.Dir(regRoot)))
	t.Cleanup(srv.Close)

	env, paths := isolatedEnv(t)

	_, err := runFDH(t, bin, env, "config", "set", "registry.url", srv.URL+"/")
	require.NoError(t, err)

	// Install at user scope so the test doesn't depend on detecting a
	// project root (the temp HOME has none).
	out, err := runFDH(t, bin, env,
		"install", "security/owasp-quick-review",
		"--scope", "user",
		"--agent", "claude-code",
	)
	require.NoErrorf(t, err, "install: %s", out)
	assert.Contains(t, out, "owasp-quick-review")

	// Confirm the skill materialized at the Claude Code user-scope path.
	skillRoot := filepath.Join(paths.Home, ".claude", "skills", "owasp-quick-review")
	skillMD := filepath.Join(skillRoot, "SKILL.md")
	require.FileExistsf(t, skillMD, "expected SKILL.md at %s after install", skillMD)
	body, err := os.ReadFile(skillMD)
	require.NoError(t, err)
	assert.Contains(t, string(body), "name: owasp-quick-review",
		"installed SKILL.md should carry the skill's frontmatter")
}

func TestSmoke_HTTPRegistry_CacheLanding(t *testing.T) {
	bin := buildBinary(t)

	regRoot := t.TempDir()
	testutil.BuildRegistry(t, regRoot, []testutil.SkillSpec{
		{
			Namespace:   "code-review",
			Name:        "standard",
			Version:     "1.0.0",
			Description: "Standard code review checklist.",
			OwnerTeam:   "dx",
			Files: map[string]string{
				"SKILL.md": testutil.FixtureSKILLMD("standard", "Standard code review checklist."),
			},
		},
	})
	srv := httptest.NewServer(http.FileServer(http.Dir(regRoot)))
	t.Cleanup(srv.Close)

	env, paths := isolatedEnv(t)

	// Configure + drive at least one HTTP fetch.
	_, err := runFDH(t, bin, env, "config", "set", "registry.url", srv.URL+"/")
	require.NoError(t, err)
	_, err = runFDH(t, bin, env, "search", "review")
	require.NoError(t, err)

	// On Windows the HTTP cache must land under %LocalAppData%\fdh\http-cache.
	// On Linux it'd be under XDG_CACHE_HOME (we set both above).
	httpCache := paths.CacheDir
	info, err := os.Stat(httpCache)
	require.NoError(t, err, "expected HTTP cache dir at %s", httpCache)
	assert.True(t, info.IsDir())

	// objects/ and index/ subtrees should both exist after one fetch of
	// index.json.
	entries, err := os.ReadDir(httpCache)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	assert.True(t, names["objects"], "http-cache should contain objects/")
	assert.True(t, names["index"], "http-cache should contain index/")

	// Crucially, no git clone should appear — confirms the dispatcher
	// truly picked HTTPRegistry, not GitRegistry-against-https.
	gitCache := filepath.Join(paths.ConfigDir, "registry-cache")
	_, err = os.Stat(gitCache)
	assert.True(t, os.IsNotExist(err),
		"git registry-cache must NOT be created when transport is http (got err=%v)", err)
}
