package skill

// Skill represents a reusable unit of methodology and knowledge.
// It is defined by a SKILL.md file with YAML frontmatter.
type Skill struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	Instructions string   `yaml:"-"` // Loaded from the markdown body
	AllowedTools []string `yaml:"allowed-tools,omitempty"`
	Scripts      []string `yaml:"scripts,omitempty"`
}

// Summary returns a one-line summary of the skill for progressive disclosure.
func (s *Skill) Summary() string {
	return s.Name + ": " + s.Description
}
