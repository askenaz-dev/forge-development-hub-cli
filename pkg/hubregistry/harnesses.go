package hubregistry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// HarnessesFile is the relative path of the harnesses YAML inside the
// hub clone.
const HarnessesFile = "hub/harnesses.yaml"

// HarnessesDoc is the in-memory shape of hub/harnesses.yaml.
type HarnessesDoc struct {
	Harnesses map[string]HarnessDoc `yaml:"harnesses"`
}

// HarnessDoc is one named harness (a curated bundle of components).
type HarnessDoc struct {
	Description string   `yaml:"description,omitempty"`
	OwnerTeam   string   `yaml:"owner_team,omitempty"`
	Skills      []string `yaml:"skills,omitempty"`
	Rules       []string `yaml:"rules,omitempty"`
	Agents      []string `yaml:"agents,omitempty"`
	Hooks       []string `yaml:"hooks,omitempty"`
}

// LoadHarnesses reads hub/harnesses.yaml from the registry's LocalPath
// and returns the parsed document. If the file does not exist returns
// an empty HarnessesDoc (no error).
func (r *Registry) LoadHarnesses() (*HarnessesDoc, error) {
	if r.LocalPath == "" {
		return nil, fmt.Errorf("hubregistry.LoadHarnesses: registry has no LocalPath")
	}
	p := filepath.Join(r.LocalPath, filepath.FromSlash(HarnessesFile))
	body, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &HarnessesDoc{Harnesses: map[string]HarnessDoc{}}, nil
		}
		return nil, fmt.Errorf("hubregistry.LoadHarnesses: read %s: %w", p, err)
	}
	var doc HarnessesDoc
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("hubregistry.LoadHarnesses: parse %s: %w", p, err)
	}
	if doc.Harnesses == nil {
		doc.Harnesses = map[string]HarnessDoc{}
	}
	return &doc, nil
}
