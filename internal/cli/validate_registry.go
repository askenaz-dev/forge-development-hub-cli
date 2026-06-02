package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/hubregistry"
)

// ValidateRegistryResult is the JSON shape emitted by
// `fdh validate-registry --json`. The shape is part of the
// stable contract: future fields may be added but existing
// ones SHALL NOT change.
type ValidateRegistryResult struct {
	OK       bool                          `json:"ok"`
	RepoRoot string                        `json:"repo_root"`
	Errors   []hubregistry.ValidationError `json:"errors"`
}

func newValidateRegistryCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate-registry [repo-root]",
		Short: "Validate the hub's hub/registry.yaml against the registry contract",
		Long: `Read hub/registry.yaml from the given repo root (default: current directory;
falls back to the legacy skills/registry.yaml mirror), parse it, and verify
each entry against the rules of the hub-registry spec: unique kebab-case names,
paths exist on disk, agents_supported non-empty, min_fdh_version is valid
semver, schema_version is supported, no orphan component directories.

Designed to be invoked both interactively (developer running locally) and as
a CI step (replacing the Python validator the hub currently uses).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidateRegistry(cmd, args, info)
		},
	}
	return cmd
}

func runValidateRegistry(cmd *cobra.Command, args []string, _ BuildInfo) error {
	root := "."
	if len(args) == 1 {
		root = args[0]
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Errorf(ExitInvalidUsage, "resolve repo root %q: %v", root, err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return Errorf(ExitInvalidUsage, "repo root %s: %v", absRoot, err)
	}
	if !info.IsDir() {
		return Errorf(ExitInvalidUsage, "repo root %s is not a directory", absRoot)
	}

	// Prefer v2 path, fall back to v1 legacy mirror.
	registryPath, registryRel, raw, readErr := readRegistry(absRoot)
	if readErr != nil {
		return Errorf(ExitInvalidUsage, "read registry: %v", readErr)
	}
	reg, err := hubregistry.Parse(raw, nil)
	if err != nil {
		// A parse error is itself a validation finding so the JSON
		// output stays uniform across success/failure cases.
		rule := "yaml-syntax"
		var schemaErr *hubregistry.UnsupportedSchemaError
		if errors.As(err, &schemaErr) {
			rule = "schema-version"
		}
		result := ValidateRegistryResult{
			OK:       false,
			RepoRoot: absRoot,
			Errors: []hubregistry.ValidationError{{
				Rule:     rule,
				Message:  err.Error(),
				Location: registryRel,
			}},
		}
		emit := emitValidateResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, outputMode(cmd), registryRel)
		if emit != nil {
			return Wrap(ExitGenericFailure, emit)
		}
		return Errorf(ExitValidation, "registry.yaml failed validation")
	}
	_ = registryPath

	res := hubregistry.Validate(reg, absRoot)
	result := ValidateRegistryResult{
		OK:       res.OK,
		RepoRoot: absRoot,
		Errors:   res.Errors,
	}
	if err := emitValidateResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, outputMode(cmd), registryRel); err != nil {
		return Wrap(ExitGenericFailure, err)
	}
	if !res.OK {
		return Errorf(ExitValidation, "registry.yaml failed validation")
	}
	return nil
}

// readRegistry finds the catalog under absRoot, preferring v2
// (hub/registry.yaml) and falling back to the legacy v1 mirror
// (skills/registry.yaml). Returns absolute path, hub-relative display
// path, the bytes, and an error.
func readRegistry(absRoot string) (abs, rel string, raw []byte, err error) {
	candidates := []string{
		filepath.Join("hub", "registry.yaml"),
		filepath.Join("skills", "registry.yaml"),
	}
	for _, c := range candidates {
		p := filepath.Join(absRoot, c)
		if b, e := os.ReadFile(p); e == nil {
			return p, filepath.ToSlash(c), b, nil
		}
	}
	return "", "", nil, fmt.Errorf("no registry.yaml found under %s (tried hub/registry.yaml and skills/registry.yaml)", absRoot)
}

func emitValidateResult(stdout, stderr io.Writer, r ValidateRegistryResult, mode, rel string) error {
	if mode == "json" {
		return emitJSON(stdout, r)
	}
	if r.OK {
		fmt.Fprintf(stdout, "ok: %s (%s)\n", rel, r.RepoRoot)
		return nil
	}
	fmt.Fprintf(stdout, "FAILED: %s (%s)\n\n", rel, r.RepoRoot)
	for _, e := range r.Errors {
		fmt.Fprintf(stdout, "  [%s] %s\n        at %s\n", e.Rule, e.Message, e.Location)
	}
	_ = stderr
	return nil
}
