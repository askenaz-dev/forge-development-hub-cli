package hubregistry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProfilesFile is the relative path of the profiles YAML inside the
// hub clone.
const ProfilesFile = "hub/profiles.yaml"

// ProfilesDoc is the in-memory shape of hub/profiles.yaml.
type ProfilesDoc struct {
	Profiles map[string]ProfileDoc `yaml:"profiles"`
}

// ProfileDoc is one named profile.
type ProfileDoc struct {
	Description string   `yaml:"description,omitempty"`
	OwnerTeam   string   `yaml:"owner_team,omitempty"`
	Skills      []string `yaml:"skills,omitempty"`
	Rules       []string `yaml:"rules,omitempty"`
	Agents      []string `yaml:"agents,omitempty"`
	Hooks       []string `yaml:"hooks,omitempty"`
}

// LoadProfiles reads hub/profiles.yaml from the registry's LocalPath
// and returns the parsed document. If the file does not exist returns
// an empty ProfilesDoc (no error).
func (r *Registry) LoadProfiles() (*ProfilesDoc, error) {
	if r.LocalPath == "" {
		return nil, fmt.Errorf("hubregistry.LoadProfiles: registry has no LocalPath")
	}
	p := filepath.Join(r.LocalPath, filepath.FromSlash(ProfilesFile))
	body, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &ProfilesDoc{Profiles: map[string]ProfileDoc{}}, nil
		}
		return nil, fmt.Errorf("hubregistry.LoadProfiles: read %s: %w", p, err)
	}
	var doc ProfilesDoc
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("hubregistry.LoadProfiles: parse %s: %w", p, err)
	}
	if doc.Profiles == nil {
		doc.Profiles = map[string]ProfileDoc{}
	}
	return &doc, nil
}
