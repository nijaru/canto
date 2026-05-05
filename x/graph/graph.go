// Package graph provides deterministic DAG orchestration for multi-agent pipelines.
//
// Orchestration is always deterministic Go code — routing conditions are
// pure functions, never LLM-decided flow control.
package graph

import (
	"context"

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
	id          string
	nodes       map[string]agent.Agent
	edges       []Edge
	entry       string
	checkpoints CheckpointStore
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

// SetCheckpointStore configures durable checkpoint persistence for graph runs.
func (g *Graph) SetCheckpointStore(store CheckpointStore) {
	g.checkpoints = store
}

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

// Run executes the graph starting at the entry node.
// It follows edges whose conditions are satisfied by each StepResult.
// Returns the final StepResult (from the last agent to execute) and any error.
func (g *Graph) Run(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return g.execute(ctx, sess, nil)
}
