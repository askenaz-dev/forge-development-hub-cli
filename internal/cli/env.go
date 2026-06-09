package cli

import "os"

// fdhEnvBindings maps FDH_* environment variables to their canonical
// viper config keys. Used by initConfig to explicitly bind them — the
// global SetEnvPrefix only covers the legacy forge_INSTALLER_* names.
//
// These bindings take precedence over config.yaml (the viper default)
// and are read on each command invocation.
var fdhEnvBindings = map[string]string{
	"FDH_REGISTRY_KIND":             "registry.kind",
	"FDH_REGISTRY_HTTP_API_VERSION": "registry.http.api_version",
	"FDH_REGISTRY_HTTP_BEARER":      "registry.http.auth.bearer",
	"FDH_REGISTRY_HTTP_BASIC_USER":  "registry.http.auth.basic.user",
	"FDH_REGISTRY_HTTP_BASIC_PASS":  "registry.http.auth.basic.pass",
	"FDH_REGISTRY_HTTP_CLIENT_CERT": "registry.http.auth.client_cert",
	"FDH_REGISTRY_HTTP_CLIENT_KEY":  "registry.http.auth.client_key",
	"FDH_TELEMETRY_ENDPOINT":        "telemetry.endpoint",
}

// envVar wraps os.Getenv so tests can stub the environment if they need
// to. The CLI's existing config + adapter flow doesn't need this level
// of indirection, but the init command reads FDH_DEFAULT_REGISTRY at
// build-team-configuration time and being able to override it from a
// test makes future expansion easier.
func envVar(key string) string {
	return os.Getenv(key)
}

// probeContextFor builds the adapter ProbeContext from a runContext.
// Kept here (rather than inlined in init.go) so doctor and init both
// share the same construction.
func probeContextFor(rc *runContext) adaptersProbeContext {
	return adaptersProbeContext{
		HomeDir:     rc.HomeDir,
		ProjectRoot: rc.ProjectRoot,
	}
}

// adaptersProbeContext is a tiny aliasing struct so init.go doesn't
// have to import the adapters package directly for one struct literal.
// The actual type lives in pkg/adapters; we re-declare a compatible
// shape here to keep the import surface narrow.
type adaptersProbeContext = struct {
	HomeDir     string
	ProjectRoot string
	LookPath    func(string) (string, error)
	StatDir     func(string) (os.FileInfo, error)
	RunShell    func(cmd string) error
}
