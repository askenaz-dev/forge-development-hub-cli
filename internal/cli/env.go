package cli

import "os"

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
