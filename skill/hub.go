package skill

import "fmt"

// Hub handles remote skill discovery and downloading.
// It is intended to be compatible with agentskills.io standard.
type Hub struct {
	BaseURL string
}

func NewHub(baseURL string) *Hub {
	return &Hub{BaseURL: baseURL}
}

// Download fetches a skill from a remote registry.
// (To be implemented in a future phase)
func (h *Hub) Download(name string) (*Skill, error) {
	return nil, fmt.Errorf("skill hub: not implemented")
}
