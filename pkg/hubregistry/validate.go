package hubregistry

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ValidationError is one finding from Validate. It maps 1:1 to the
// JSON shape `fdh validate-registry --json` emits.
type ValidationError struct {
	Rule     string `json:"rule" yaml:"rule"`
	Message  string `json:"message" yaml:"message"`
	Location string `json:"location" yaml:"location"`
}

// ValidationResult bundles all findings.
type ValidationResult struct {
	OK     bool              `json:"ok" yaml:"ok"`
	Errors []ValidationError `json:"errors" yaml:"errors"`
}

// semverRE accepts a minimal subset of semver (X.Y.Z optionally
// followed by `-pre.release`).
var semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z\-.]+)?$`)

var kebabRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// validAgents is the canonical set of agent IDs.
var validAgents = map[string]struct{}{
	"claude-code": {},
	"codex":       {},
	"copilot":     {},
	"opencode":    {},
}

// Validate inspects a Registry loaded from the hub catalog against
// the rules of the `hub-registry-v2` spec:
//
//   - schema_version is one of the supported values (2 native, 1 mirror
//     accepted via the normalizer)
//   - every component declares kind ∈ {skill, rule, agent, hook}
//   - name is unique within the same kind and kebab-case
//   - path is coherent with kind and exists on disk under hubRoot
//   - agents_supported is non-empty and subset of the canonical agents
//   - min_fdh_version, when set, is valid semver
//   - the catalog has no orphan directories under <kind>s/ that are
//     not registered (only checked if hubRoot is non-empty)
//
// hubRoot is the directory the Registry was loaded from. Pass "" to
// skip filesystem checks (useful for pure-syntax validation).
func Validate(r *Registry, hubRoot string) ValidationResult {
	res := ValidationResult{Errors: []ValidationError{}}

	// Accept both schema_version 1 (legacy mirror, normalized by parse)
	// and 2 (native).
	if r.SchemaVersion != 1 && r.SchemaVersion != 2 {
		res.Errors = append(res.Errors, ValidationError{
			Rule:     "schema-version",
			Message:  fmt.Sprintf("schema_version %d not supported (this fdh supports 1 legacy mirror and 2 native)", r.SchemaVersion),
			Location: "registry.yaml",
		})
	}

	// Per-entry validation.
	type seenKey struct{ name, kind string }
	seen := map[seenKey]int{}
	for i, c := range r.Components {
		loc := fmt.Sprintf("registry.yaml#/components/%d (%s/%s)", i, c.Kind, c.Name)

		// kind required and in enum.
		if c.Kind == "" {
			res.Errors = append(res.Errors, ValidationError{
				Rule:     "kind-required",
				Message:  fmt.Sprintf("entry %q is missing 'kind' (want one of %s)", c.Name, strings.Join(AllKinds, ", ")),
				Location: loc,
			})
		} else if !kindOK(c.Kind) {
			res.Errors = append(res.Errors, ValidationError{
				Rule:     "kind-invalid",
				Message:  fmt.Sprintf("entry %q has invalid kind %q (want one of %s)", c.Name, c.Kind, strings.Join(AllKinds, ", ")),
				Location: loc,
			})
		}

		// name required, kebab-case, unique per kind.
		if c.Name == "" {
			res.Errors = append(res.Errors, ValidationError{
				Rule:     "name-required",
				Message:  "component is missing 'name'",
				Location: loc,
			})
		} else {
			if !kebabRE.MatchString(c.Name) {
				res.Errors = append(res.Errors, ValidationError{
					Rule:     "name-kebab-case",
					Message:  fmt.Sprintf("name %q must be kebab-case (lowercase letters, digits, single dashes)", c.Name),
					Location: loc,
				})
			}
			key := seenKey{name: c.Name, kind: c.Kind}
			if prev, dup := seen[key]; dup {
				res.Errors = append(res.Errors, ValidationError{
					Rule:     "unique-name",
					Message:  fmt.Sprintf("duplicate (kind=%s, name=%q) (first seen at index %d)", c.Kind, c.Name, prev),
					Location: loc,
				})
			} else {
				seen[key] = i
			}
		}

		// path required, coherent with kind, and on disk if hubRoot set.
		if c.Path == "" {
			res.Errors = append(res.Errors, ValidationError{
				Rule:     "path-required",
				Message:  "component is missing 'path'",
				Location: loc,
			})
		} else {
			if kindOK(c.Kind) {
				wantPrefix := KindDir(c.Kind) + "/"
				if !strings.HasPrefix(filepath.ToSlash(c.Path), wantPrefix) {
					res.Errors = append(res.Errors, ValidationError{
						Rule:     "kind-path-mismatch",
						Message:  fmt.Sprintf("entry %q kind=%s has path %q; expected to start with %q", c.Name, c.Kind, c.Path, wantPrefix),
						Location: loc,
					})
				}
			}
			if hubRoot != "" {
				abs := filepath.Join(hubRoot, filepath.FromSlash(c.Path))
				info, err := os.Stat(abs)
				if err != nil {
					res.Errors = append(res.Errors, ValidationError{
						Rule:     "path-exists",
						Message:  fmt.Sprintf("path %q does not exist on disk", c.Path),
						Location: loc,
					})
				} else if !info.IsDir() {
					res.Errors = append(res.Errors, ValidationError{
						Rule:     "path-exists",
						Message:  fmt.Sprintf("path %q is not a directory", c.Path),
						Location: loc,
					})
				}
			}
		}

		// agents_supported non-empty + subset of canonical.
		if len(c.AgentsSupported) == 0 {
			res.Errors = append(res.Errors, ValidationError{
				Rule:     "agents-supported-nonempty",
				Message:  "agents_supported is empty",
				Location: loc,
			})
		} else {
			for _, a := range c.AgentsSupported {
				if _, ok := validAgents[a]; !ok {
					res.Errors = append(res.Errors, ValidationError{
						Rule:     "agents-supported-unknown",
						Message:  fmt.Sprintf("agents_supported contains unknown agent %q (want subset of claude-code, codex, copilot, opencode)", a),
						Location: loc,
					})
				}
			}
		}

		if c.MinFDHVersion != "" && !semverRE.MatchString(c.MinFDHVersion) {
			res.Errors = append(res.Errors, ValidationError{
				Rule:     "semver",
				Message:  fmt.Sprintf("min_fdh_version %q is not a valid semver", c.MinFDHVersion),
				Location: loc,
			})
		}
	}

	// Orphan detection per kind. Only runs when hubRoot is set.
	if hubRoot != "" {
		for _, kind := range AllKinds {
			orphans, err := findOrphansByKind(hubRoot, kind, r.Components)
			if err != nil {
				continue
			}
			for _, o := range orphans {
				res.Errors = append(res.Errors, ValidationError{
					Rule:     "no-orphans",
					Message:  fmt.Sprintf("directory %q exists under %s/ but is not registered in the catalog", o, KindDir(kind)),
					Location: o,
				})
			}
		}
	}

	res.OK = len(res.Errors) == 0
	return res
}

// findOrphansByKind lists `<kindDir>/<X>/` directories under hubRoot
// that do not have a matching entry in the registry for that kind.
func findOrphansByKind(hubRoot, kind string, components []ComponentEntry) ([]string, error) {
	registered := map[string]bool{}
	for _, c := range components {
		if c.Kind != kind {
			continue
		}
		registered[filepath.ToSlash(filepath.Clean(c.Path))] = true
	}

	dir := KindDir(kind)
	if dir == "" {
		return nil, nil
	}
	root := filepath.Join(hubRoot, dir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var orphans []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := dir + "/" + e.Name()
		if !registered[rel] {
			orphans = append(orphans, rel)
		}
	}
	return orphans, nil
}
