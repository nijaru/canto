package skill

import (
	"os"
	"path/filepath"
	"sync"
)

// Registry manages discovery and loading of skills.
type Registry struct {
	mu     sync.RWMutex
	skills map[string]*Skill
	paths  []string
}

// NewRegistry creates a new empty skill registry.
func NewRegistry(paths ...string) *Registry {
	return &Registry{
		skills: make(map[string]*Skill),
		paths:  paths,
	}
}

// Discover searches configured paths for SKILL.md files.
func (r *Registry) Discover() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, root := range r.paths {
		// Ensure root exists
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}

		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if !info.IsDir() && info.Name() == "SKILL.md" {
				s, err := Load(path)
				if err == nil {
					r.skills[s.Name] = s
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Get returns a skill by name.
func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

// List returns all discovered skills.
func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		res = append(res, s)
	}
	return res
}

// Register adds or replaces a skill in the registry.
func (r *Registry) Register(s *Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.Name] = s
}

// Deregister removes a skill from the registry by name.
// It is a no-op if the skill does not exist.
func (r *Registry) Deregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.skills, name)
}
