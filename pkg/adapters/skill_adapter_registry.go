package adapters

// AllSkillAdapters returns one instance of every shipped
// SkillAdapter, in stable order. Used by the wizard to map a
// selected agent id back to the adapter that handles it.
func AllSkillAdapters() []SkillAdapter {
	return []SkillAdapter{
		ClaudeCodeAdapter{},
		CodexAdapter{},
		CopilotAdapter{},
		OpenCodeAdapter{},
	}
}

// SkillAdapterByID returns the adapter whose Agent() matches id, or
// nil when no shipped adapter does. The returned value is a
// value-receiver instance, safe to use concurrently.
func SkillAdapterByID(id string) SkillAdapter {
	for _, a := range AllSkillAdapters() {
		if a.Agent() == id {
			return a
		}
	}
	return nil
}
