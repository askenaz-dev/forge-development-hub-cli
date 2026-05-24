package hubregistry

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// ValidationError is one finding from Validate. It maps 1:1 to the
// JSON shape `fdh validate-registry --json` emits.
type ValidationError struct {
	// Rule is the short rule name (e.g. "unique-name", "path-exists").
	Rule string `json:"rule" yaml:"rule"`

	// Message is the human-readable description.
	Message string `json:"message" yaml:"message"`

	// Location is the file/skill the error attaches to. For top-level
	// errors (missing field, bad schema) this is "registry.yaml".
	Location string `json:"location" yaml:"location"`
}

// ValidationResult bundles all findings into one structure so the
// caller can decide whether to fail or warn.
type ValidationResult struct {
	OK     bool              `json:"ok" yaml:"ok"`
	Errors []ValidationError `json:"errors" yaml:"errors"`
}

// semverRE accepts a minimal subset of semver (X.Y.Z optionally
// followed by `-pre.release`). Sufficient for `min_fdh_version`
// values like "0.5.2" or "1.0.0-beta.1".
var semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z\-.]+)?$`)

var kebabRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Validate inspects a Registry loaded from `skills/registry.yaml`
// against the rules of the `hub-skills-registry` spec:
//
//   - schema_version is one of the supported values
//   - every skill name is unique kebab-case
//   - every skill path exists on disk under hubRoot
//   - every skill declares agents_supported (non-empty)
//   - min_fdh_version, when set, is valid semver
//   - the catalog has no orphan directories under skills/ that are
//     not registered (only checked if hubRoot is non-empty)
//
// hubRoot is the directory the Registry was loaded from
// (`Registry.LocalPath`). Pass "" to skip filesystem checks (useful
// for pure-syntax validation).
func Validate(r *Registry, hubRoot string) ValidationResult {
	res := ValidationResult{Errors: []ValidationError{}}

	// schema_version supported?
	if r.SchemaVersion != SchemaVersion {
		res.Errors = append(res.Errors, ValidationError{
			Rule:     "schema-version",
			Message:  fmt.Sprintf("schema_version %d not supported (this fdh supports %d)", r.SchemaVersion, SchemaVersion),
			Location: "registry.yaml",
		})
	}

	seen := map[string]int{}
	for i, s := range r.Skills {
		loc := fmt.Sprintf("registry.yaml#/skills/%d (%s)", i, s.Name)

		if s.Name == "" {
			res.Errors = append(res.Errors, ValidationError{
				Rule:     "name-required",
				Message:  "skill is missing 'name'",
				Location: loc,
			})
		} else {
			if !kebabRE.MatchString(s.Name) {
				res.Errors = append(res.Errors, ValidationError{
					Rule:     "name-kebab-case",
					Message:  fmt.Sprintf("name %q must be kebab-case (lowercase letters, digits, single dashes)", s.Name),
					Location: loc,
				})
			}
			if prev, dup := seen[s.Name]; dup {
				res.Errors = append(res.Errors, ValidationError{
					Rule:     "unique-name",
					Message:  fmt.Sprintf("duplicate name %q (first seen at index %d)", s.Name, prev),
					Location: loc,
				})
			} else {
				seen[s.Name] = i
			}
		}

		if s.Path == "" {
			res.Errors = append(res.Errors, ValidationError{
				Rule:     "path-required",
				Message:  "skill is missing 'path'",
				Location: loc,
			})
		} else if hubRoot != "" {
			abs := filepath.Join(hubRoot, filepath.FromSlash(s.Path))
			info, err := os.Stat(abs)
			if err != nil {
				res.Errors = append(res.Errors, ValidationError{
					Rule:     "path-exists",
					Message:  fmt.Sprintf("path %q does not exist on disk", s.Path),
					Location: loc,
				})
			} else if !info.IsDir() {
				res.Errors = append(res.Errors, ValidationError{
					Rule:     "path-exists",
					Message:  fmt.Sprintf("path %q is not a directory", s.Path),
					Location: loc,
				})
			}
		}

		if len(s.AgentsSupported) == 0 {
			res.Errors = append(res.Errors, ValidationError{
				Rule:     "agents-supported-nonempty",
				Message:  "agents_supported is empty",
				Location: loc,
			})
		}

		if s.MinFDHVersion != "" && !semverRE.MatchString(s.MinFDHVersion) {
			res.Errors = append(res.Errors, ValidationError{
				Rule:     "semver",
				Message:  fmt.Sprintf("min_fdh_version %q is not a valid semver", s.MinFDHVersion),
				Location: loc,
			})
		}
	}

	if hubRoot != "" {
		orphans, err := findOrphans(hubRoot, r.Skills)
		if err == nil {
			for _, o := range orphans {
				res.Errors = append(res.Errors, ValidationError{
					Rule:     "no-orphans",
					Message:  fmt.Sprintf("directory %q exists under skills/ but is not registered in registry.yaml", o),
					Location: o,
				})
			}
		}
	}

	res.OK = len(res.Errors) == 0
	return res
}

// findOrphans lists `skills/<X>/` directories under hubRoot that do
// not have a matching entry in the registry. Files at the top level
// of `skills/` (including `registry.yaml`) are ignored.
func findOrphans(hubRoot string, skills []SkillEntry) ([]string, error) {
	registered := map[string]bool{}
	for _, s := range skills {
		// Normalise: store as forward-slash path relative to hubRoot.
		registered[filepath.ToSlash(filepath.Clean(s.Path))] = true
	}

	skillsDir := filepath.Join(hubRoot, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, err
	}
	var orphans []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := "skills/" + e.Name()
		if !registered[rel] {
			orphans = append(orphans, rel)
		}
	}
	return orphans, nil
}

