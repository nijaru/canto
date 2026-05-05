package graph

import "fmt"

// Validate checks the graph for structural errors (missing nodes, cycles).
// Must be called before Run to ensure the graph is a valid DAG.
func (g *Graph) Validate() error {
	if _, ok := g.nodes[g.entry]; !ok {
		return fmt.Errorf("validate: entry node %q not registered", g.entry)
	}

	for _, edge := range g.edges {
		if _, ok := g.nodes[edge.From]; !ok {
			return fmt.Errorf("validate: edge references missing source node %q", edge.From)
		}
		if _, ok := g.nodes[edge.To]; !ok {
			return fmt.Errorf("validate: edge references missing target node %q", edge.To)
		}
	}

	visited := make(map[string]bool)
	active := make(map[string]bool)
	var checkCycle func(string) error
	checkCycle = func(node string) error {
		visited[node] = true
		active[node] = true
		for _, edge := range g.edges {
			if edge.From != node {
				continue
			}
			if !visited[edge.To] {
				if err := checkCycle(edge.To); err != nil {
					return err
				}
				continue
			}
			if active[edge.To] {
				return fmt.Errorf("validate: cycle detected involving node %q", edge.To)
			}
		}
		active[node] = false
		return nil
	}

	for node := range g.nodes {
		if visited[node] {
			continue
		}
		if err := checkCycle(node); err != nil {
			return err
		}
	}
	return nil
}
