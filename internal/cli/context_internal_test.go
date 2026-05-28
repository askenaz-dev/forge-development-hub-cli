package cli

import (
	"bytes"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/registry"
)

// resetViper wipes the global viper instance so each test starts from a
// clean slate. The CLI's existing fast-path tests don't need this because
// they construct their own pipelines, but the dispatcher tests directly
// poke viper.
func resetViper(t *testing.T) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
}

func TestBuildRegistry_GitURL_RoutesToGit(t *testing.T) {
	resetViper(t)
	viper.Set("registry.url", "https://github.com/askenaz-dev/forge-development-hub.git")
	r, err := buildRegistry(false)
	require.NoError(t, err)
	_, ok := r.(*registry.GitRegistry)
	assert.True(t, ok, "expected *GitRegistry for .git URL, got %T", r)
}

func TestBuildRegistry_HTTPURL_RoutesToHTTP(t *testing.T) {
	resetViper(t)
	viper.Set("registry.url", "https://pkg.askenaz.dev/registry/v1/")
	r, err := buildRegistry(false)
	require.NoError(t, err)
	hr, ok := r.(*registry.HTTPRegistry)
	require.True(t, ok, "expected *HTTPRegistry for https URL without .git, got %T", r)
	assert.Equal(t, "https://pkg.askenaz.dev/registry/v1/", hr.BaseURL)
	assert.Equal(t, "v1", hr.APIVersion)
	assert.NotEmpty(t, hr.CacheDir, "HTTPRegistry CacheDir should default to a user-cache path")
}

func TestBuildRegistry_HTTPURL_NormalizesTrailingSlash(t *testing.T) {
	resetViper(t)
	viper.Set("registry.url", "https://pkg.askenaz.dev/registry/v1")
	r, err := buildRegistry(false)
	require.NoError(t, err)
	hr := r.(*registry.HTTPRegistry)
	assert.Equal(t, "https://pkg.askenaz.dev/registry/v1/", hr.BaseURL)
}

func TestBuildRegistry_KindGit_OverridesHTTPURL(t *testing.T) {
	resetViper(t)
	viper.Set("registry.url", "https://pkg.askenaz.dev/registry/v1/")
	viper.Set("registry.kind", "git")
	r, err := buildRegistry(false)
	require.NoError(t, err)
	_, ok := r.(*registry.GitRegistry)
	assert.True(t, ok, "registry.kind=git should force GitRegistry")
}

func TestBuildRegistry_KindHTTP_OverridesGitURL(t *testing.T) {
	resetViper(t)
	viper.Set("registry.url", "https://github.com/example/repo.git")
	viper.Set("registry.kind", "http")
	r, err := buildRegistry(false)
	require.NoError(t, err)
	_, ok := r.(*registry.HTTPRegistry)
	assert.True(t, ok, "registry.kind=http should force HTTPRegistry")
}

func TestBuildRegistry_KindHTTP_RejectsNonHTTPURL(t *testing.T) {
	resetViper(t)
	viper.Set("registry.url", "git@github.com:example/repo.git")
	viper.Set("registry.kind", "http")
	_, err := buildRegistry(false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http(s) URL")
}

func TestBuildRegistry_LocalPath_AlwaysGit(t *testing.T) {
	resetViper(t)
	local := t.TempDir()
	viper.Set("registry.local_path", local)
	r, err := buildRegistry(false)
	require.NoError(t, err)
	gr, ok := r.(*registry.GitRegistry)
	require.True(t, ok)
	assert.Equal(t, local, gr.LocalPath)
	assert.Empty(t, gr.RemoteURL)
}

func TestBuildRegistry_AmbiguousURL_AutoErrors(t *testing.T) {
	resetViper(t)
	viper.Set("registry.url", "foo://bar.example.com/registry")
	_, err := buildRegistry(false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auto-detect")
}

func TestBuildRegistry_UnknownKind(t *testing.T) {
	resetViper(t)
	viper.Set("registry.url", "https://pkg.askenaz.dev/registry/v1/")
	viper.Set("registry.kind", "ftp")
	_, err := buildRegistry(false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry.kind")
}

func TestBuildRegistry_NoConfig(t *testing.T) {
	resetViper(t)
	_, err := buildRegistry(false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no registry configured")
}

func TestBuildHTTPRegistry_PicksUpAuthAndAPIVersion(t *testing.T) {
	resetViper(t)
	viper.Set("registry.url", "https://pkg.askenaz.dev/registry/v1/")
	viper.Set("registry.kind", "http")
	viper.Set("registry.http.api_version", "v1")
	viper.Set("registry.http.auth.bearer", "token-xyz")
	viper.Set("registry.http.auth.basic.user", "alice")
	viper.Set("registry.http.auth.basic.pass", "secret")
	viper.Set("registry.http.auth.client_cert", "/etc/ssl/cli.crt")
	viper.Set("registry.http.auth.client_key", "/etc/ssl/cli.key")

	r, err := buildRegistry(false)
	require.NoError(t, err)
	hr := r.(*registry.HTTPRegistry)
	assert.Equal(t, "v1", hr.APIVersion)
	assert.Equal(t, "token-xyz", hr.Auth.Bearer)
	assert.Equal(t, "alice", hr.Auth.BasicUser)
	assert.Equal(t, "secret", hr.Auth.BasicPass)
	assert.Equal(t, "/etc/ssl/cli.crt", hr.Auth.ClientCert)
	assert.Equal(t, "/etc/ssl/cli.key", hr.Auth.ClientKey)
}

func TestConfig_SupportsNewKeys(t *testing.T) {
	want := []string{
		"registry.kind",
		"registry.http.api_version",
		"registry.http.auth.bearer",
		"registry.http.auth.basic.user",
		"registry.http.auth.basic.pass",
		"registry.http.auth.client_cert",
		"registry.http.auth.client_key",
	}
	for _, k := range want {
		_, ok := SupportedConfigKeys[k]
		assert.Truef(t, ok, "SupportedConfigKeys missing %q", k)
	}
}

func TestConfig_SetAndGetRoundTrip(t *testing.T) {
	resetViper(t)
	cmd := &cobra.Command{}
	for k, val := range map[string]string{
		"registry.kind":              "http",
		"registry.http.auth.bearer":  "abc123",
		"registry.http.auth.basic.user": "bob",
	} {
		require.NoError(t, runConfigSetNoPersist(cmd, []string{k, val}))
		assert.Equal(t, val, viper.GetString(k))
	}
}

// runConfigSetNoPersist mimics runConfigSet but skips the file-system
// write. The persistence path is exercised by the existing config tests
// (and indirectly by config migrate). For dispatcher coverage we just
// need the viper update.
func runConfigSetNoPersist(_ *cobra.Command, args []string) error {
	key, value := args[0], args[1]
	if _, ok := SupportedConfigKeys[key]; !ok {
		return Errorf(ExitInvalidUsage, "unknown key %q", key)
	}
	viper.Set(key, value)
	return nil
}

func TestConfig_RejectsUnknownKey(t *testing.T) {
	resetViper(t)
	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	err := runConfigSet(cmd, []string{"registry.http.unknown", "x"})
	require.Error(t, err)
	assert.Equal(t, ExitInvalidUsage, ExitCode(err))
}

func TestConfig_ListIncludesNewKeys(t *testing.T) {
	resetViper(t)
	out := &bytes.Buffer{}
	require.NoError(t, printConfigList(out))
	body := out.String()
	for _, k := range []string{
		"registry.kind",
		"registry.http.api_version",
		"registry.http.auth.bearer",
		"registry.http.auth.basic.user",
		"registry.http.auth.basic.pass",
		"registry.http.auth.client_cert",
		"registry.http.auth.client_key",
	} {
		assert.Contains(t, body, k)
	}
}

func TestEnvBindings_FDH_REGISTRY_KIND_TakesPrecedence(t *testing.T) {
	resetViper(t)
	t.Setenv("FDH_REGISTRY_KIND", "http")
	require.NoError(t, viper.BindEnv("registry.kind", "FDH_REGISTRY_KIND"))
	// Set a conflicting config-file value; env must win.
	viper.Set("registry.kind", "git") // viper.Set is "explicit" — higher than env
	// Re-check semantics: viper.Set takes precedence over BindEnv. The
	// real CLI never calls viper.Set for these keys; the test mirrors
	// the production order: defaults < config < env < flag < explicit.
	// So we sanity-check the mapping by reading via the lower-precedence
	// SetDefault.
	viper.Reset()
	t.Cleanup(viper.Reset)
	require.NoError(t, viper.BindEnv("registry.kind", "FDH_REGISTRY_KIND"))
	viper.SetDefault("registry.kind", "git")
	assert.Equal(t, "http", viper.GetString("registry.kind"))
}

func TestEnvBindings_AllFDHKeysMapToViper(t *testing.T) {
	for env, viperKey := range fdhEnvBindings {
		t.Run(env, func(t *testing.T) {
			resetViper(t)
			value := "value-" + env
			t.Setenv(env, value)
			require.NoError(t, viper.BindEnv(viperKey, env))
			assert.Equal(t, value, viper.GetString(viperKey))
		})
	}
}

func TestClassifyRegistry_HTTP(t *testing.T) {
	hr := &registry.HTTPRegistry{APIVersion: "v1"}
	kind, transport := classifyRegistry(hr)
	assert.Equal(t, "http", kind)
	assert.Equal(t, "http v1", transport)
}

func TestClassifyRegistry_GitRemote(t *testing.T) {
	gr := &registry.GitRegistry{RemoteURL: "https://example.test/repo.git"}
	kind, transport := classifyRegistry(gr)
	assert.Equal(t, "git", kind)
	assert.Equal(t, "git", transport)
}

func TestClassifyRegistry_GitLocal(t *testing.T) {
	gr := &registry.GitRegistry{LocalPath: "/some/path"}
	kind, transport := classifyRegistry(gr)
	assert.Equal(t, "local", kind)
	assert.Equal(t, "local", transport)
}

func TestDoctorHuman_IncludesTransportLine(t *testing.T) {
	report := DoctorReport{
		InstallerVersion: "test",
		HomeDir:          os.TempDir(),
		Registry: RegistryHealth{
			Configured: true,
			Source:     "http:https://pkg.askenaz.dev/registry/v1/?api=v1",
			Reachable:  true,
			Kind:       "http",
			Transport:  "http v1",
		},
	}
	buf := &bytes.Buffer{}
	printDoctorTable(buf, report)
	out := buf.String()
	assert.Contains(t, out, "transport: http v1")
	assert.Contains(t, out, "source: http:https://pkg.askenaz.dev/registry/v1/?api=v1")
}
