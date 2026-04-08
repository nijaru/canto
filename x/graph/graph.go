// Package graph provides deterministic DAG orchestration for multi-agent pipelines.
//
// Orchestration is always deterministic Go code — routing conditions are
// pure functions, never LLM-decided flow control.
package graph

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Edge connects two nodes in the graph. The Condition is evaluated on the
// StepResult of the From agent to decide whether to follow this edge.
// Conditions must be pure Go — no LLM calls.
type Edge struct {
	From      string
	To        string
	Condition func(result agent.StepResult) bool
}

// Graph is a directed acyclic graph of agents with conditional routing.
// Execution starts at the entry node and follows edges whose conditions
// are satisfied by each agent's StepResult. Stops at a terminal node
// (no outgoing edge satisfied) or when the context is cancelled.
type Graph struct {
	id    string
	nodes map[string]agent.Agent
	edges []Edge
	entry string
}

// New creates an empty Graph with the given ID and entry agent ID.
func New(id, entry string) *Graph {
	return &Graph{
		id:    id,
		nodes: make(map[string]agent.Agent),
		entry: entry,
	}
}

// ID returns the graph's unique identifier.
func (g *Graph) ID() string { return g.id }

// Step executes the graph pipeline. For a graph, Step and Turn are equivalent.
func (g *Graph) Step(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return g.Run(ctx, sess)
}

// Turn executes the graph pipeline.
func (g *Graph) Turn(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return g.Run(ctx, sess)
}

// StreamTurn executes the graph pipeline, relaying chunks from any streaming nodes.
func (g *Graph) StreamTurn(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	return g.execute(ctx, sess, chunkFn)
}

// AddNode registers an agent as a node in the graph.
func (g *Graph) AddNode(a agent.Agent) {
	g.nodes[a.ID()] = a
}

// AddEdge adds a conditional edge between two nodes.
// If no condition is provided, the edge is unconditional (always followed).
func (g *Graph) AddEdge(from, to string, condition func(agent.StepResult) bool) {
	if condition == nil {
		condition = func(agent.StepResult) bool { return true }
	}
	g.edges = append(g.edges, Edge{From: from, To: to, Condition: condition})
}

// Validate checks the graph for structural errors (missing nodes, cycles).
// Must be called before Run to ensure the graph is a valid DAG.
func (g *Graph) Validate() error {
	if _, ok := g.nodes[g.entry]; !ok {
		return fmt.Errorf("validate: entry node %q not registered", g.entry)
	}

	for _, e := range g.edges {
		if _, ok := g.nodes[e.From]; !ok {
			return fmt.Errorf("validate: edge references missing source node %q", e.From)
		}
		if _, ok := g.nodes[e.To]; !ok {
			return fmt.Errorf("validate: edge references missing target node %q", e.To)
		}
	}

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var checkCycle func(string) error
	checkCycle = func(node string) error {
		visited[node] = true
		recStack[node] = true

		for _, e := range g.edges {
			if e.From == node {
				if !visited[e.To] {
					if err := checkCycle(e.To); err != nil {
						return err
					}
				} else if recStack[e.To] {
					return fmt.Errorf("validate: cycle detected involving node %q", e.To)
				}
			}
		}
		recStack[node] = false
		return nil
	}

	for node := range g.nodes {
		if !visited[node] {
			if err := checkCycle(node); err != nil {
				return err
			}
		}
	}

	return nil
}

// Run executes the graph starting at the entry node.
// It follows edges whose conditions are satisfied by each StepResult.
// Returns the final StepResult (from the last agent to execute) and any error.
func (g *Graph) Run(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return g.execute(ctx, sess, nil)
}

func (g *Graph) execute(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if _, ok := g.nodes[g.entry]; !ok {
		return agent.StepResult{}, fmt.Errorf("graph: entry node %q not registered", g.entry)
	}

	current := g.entry
	var lastResult agent.StepResult
	var totalUsage llm.Usage

	for {
		if err := ctx.Err(); err != nil {
			lastResult.Usage = totalUsage
			return lastResult, err
		}

		a, ok := g.nodes[current]
		if !ok {
			lastResult.Usage = totalUsage
			return lastResult, fmt.Errorf("graph: node %q not registered", current)
		}

		var result agent.StepResult
		var err error

		// Use streaming if requested AND the node supports it.
		if chunkFn != nil {
			if streamer, ok := a.(agent.Streamer); ok {
				result, err = streamer.StreamTurn(ctx, sess, chunkFn)
			} else {
				result, err = a.Turn(ctx, sess)
			}
		} else {
			result, err = a.Turn(ctx, sess)
		}

		if err != nil {
			lastResult.Usage = totalUsage
			return lastResult, fmt.Errorf("graph: node %q: %w", current, err)
		}

		totalUsage.InputTokens += result.Usage.InputTokens
		totalUsage.OutputTokens += result.Usage.OutputTokens
		totalUsage.TotalTokens += result.Usage.TotalTokens
		totalUsage.Cost += result.Usage.Cost
		lastResult = result

		// If the agent issued a handoff, record it in the session log.
		if result.Handoff != nil {
			if err := agent.RecordHandoff(ctx, sess, result.Handoff); err != nil {
				return lastResult, err
			}
		}

		if result.TurnStopReason.StopsProgress() {
			break
		}

		// Find the first outgoing edge whose condition is satisfied.
		next := g.nextNode(current, result)
		if next == "" {
			// Terminal node — no satisfied outgoing edge.
			break
		}
		current = next
	}

	lastResult.Usage = totalUsage
	return lastResult, nil
}

// nextNode returns the target of the first outgoing edge from `from` whose
// condition is satisfied by `result`. Returns "" if no edge matches.
func (g *Graph) nextNode(from string, result agent.StepResult) string {
	for _, e := range g.edges {
		if e.From == from && e.Condition(result) {
			return e.To
		}
	}
	return ""
}
