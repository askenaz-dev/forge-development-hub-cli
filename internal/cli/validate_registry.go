package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

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
		Short: "Validate the hub's skills/registry.yaml against the registry contract",
		Long: `Read skills/registry.yaml from the given repo root (default: current directory),
parse it, and verify each entry against the rules of the hub-skills-registry
spec: unique kebab-case names, paths exist on disk, agents_supported non-empty,
min_fdh_version is valid semver, schema_version is supported, no orphan
directories under skills/.

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

	registryPath := filepath.Join(absRoot, "skills", "registry.yaml")
	raw, err := os.ReadFile(registryPath)
	if err != nil {
		return Errorf(ExitInvalidUsage, "read %s: %v", registryPath, err)
	}
	reg, err := decodeRegistryFromBytes(raw)
	if err != nil {
		// A parse error is itself a validation finding so the JSON
		// output stays uniform across success/failure cases.
		result := ValidateRegistryResult{
			OK:       false,
			RepoRoot: absRoot,
			Errors: []hubregistry.ValidationError{{
				Rule:     "yaml-syntax",
				Message:  err.Error(),
				Location: "skills/registry.yaml",
			}},
		}
		emit := emitValidateResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, outputMode(cmd))
		if emit != nil {
			return Wrap(ExitGenericFailure, emit)
		}
		return Errorf(ExitValidation, "registry.yaml failed validation")
	}

	res := hubregistry.Validate(reg, absRoot)
	result := ValidateRegistryResult{
		OK:       res.OK,
		RepoRoot: absRoot,
		Errors:   res.Errors,
	}
	if err := emitValidateResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, outputMode(cmd)); err != nil {
		return Wrap(ExitGenericFailure, err)
	}
	if !res.OK {
		return Errorf(ExitValidation, "registry.yaml failed validation")
	}
	return nil
}

// decodeRegistryFromBytes parses YAML bytes into a hubregistry.Registry.
// Kept as a small helper so the CLI doesn't import yaml directly from
// other places.
func decodeRegistryFromBytes(raw []byte) (*hubregistry.Registry, error) {
	var r hubregistry.Registry
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse registry.yaml: %w", err)
	}
	return &r, nil
}

func emitValidateResult(stdout, stderr io.Writer, r ValidateRegistryResult, mode string) error {
	if mode == "json" {
		return emitJSON(stdout, r)
	}
	if r.OK {
		fmt.Fprintf(stdout, "ok: %s (%s)\n", filepath.Join("skills", "registry.yaml"), r.RepoRoot)
		return nil
	}
	fmt.Fprintf(stdout, "FAILED: %s (%s)\n\n", filepath.Join("skills", "registry.yaml"), r.RepoRoot)
	for _, e := range r.Errors {
		fmt.Fprintf(stdout, "  [%s] %s\n        at %s\n", e.Rule, e.Message, e.Location)
	}
	_ = stderr
	return nil
}
