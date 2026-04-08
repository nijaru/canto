package graph

import (
	"context"
	"slices"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
)

// CheckpointStore persists graph superstep progress between node turns.
type CheckpointStore interface {
	Load(ctx context.Context, graphID, sessionID string) (*Checkpoint, error)
	Save(ctx context.Context, checkpoint Checkpoint) error
	Clear(ctx context.Context, graphID, sessionID string) error
}

// Checkpoint captures the durable resume point for a graph execution.
type Checkpoint struct {
	GraphID     string
	SessionID   string
	NextNode    string
	Steps       int
	LastEventID string
	Usage       llm.Usage
	Result      agent.StepResult
	Completed   bool
}

func cloneStepResult(in agent.StepResult) agent.StepResult {
	out := in
	out.ToolResults = slices.Clone(in.ToolResults)
	if in.Handoff != nil {
		handoff := *in.Handoff
		out.Handoff = &handoff
	}
	return out
}
