package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-json-experiment/json"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// ErrMaxSteps is returned by Turn when the agent exhausts its step budget.
// Use errors.Is(err, ErrMaxSteps) to distinguish from provider errors.
var ErrMaxSteps = errors.New("maximum tool calling steps reached")

// StepResult carries the outcome of a single Step or Turn execution.
type StepResult struct {
	// Content is the final assistant text from the last step.
	// Populated by Turn so orchestrators don't re-parse the session.
	Content string `json:"content,omitzero"`

	// Handoff is non-nil when the agent's last action was a handoff to
	// another agent. The caller must route to the target agent.
	Handoff *Handoff `json:"handoff,omitzero"`

	// ToolResults contains the results of any tools executed in the last step.
	// Useful for graph routing based on tool output without session re-parsing.
	ToolResults []llm.Message `json:"tool_results,omitzero"`

	// Usage reports the token consumption for the turn (sum of all steps).
	Usage llm.Usage `json:"usage,omitzero"`
}

// Handoff describes a control transfer from one agent to another.
// It is emitted as an EventTypeHandoff in the session log and surfaced
// in StepResult so graph/swarm can route without re-parsing events.
type Handoff struct {
	TargetAgentID string `json:"target_agent_id"`
	Reason        string `json:"reason"`
	Context       string `json:"context"` // information passed to the receiving agent
}

// handoffTool implements tool.Tool for a single target agent.
type handoffTool struct {
	targetID string
}

// HandoffTool returns a tool.Tool that the LLM calls to hand off to a
// specific agent. Register it in the source agent's tool registry.
// Multiple calls to HandoffTool create multiple tools (one per target).
//
// Tool name: "transfer_to_{targetAgentID}"
func HandoffTool(targetAgentID string) tool.Tool {
	return &handoffTool{targetID: targetAgentID}
}

func (h *handoffTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: fmt.Sprintf("transfer_to_%s", h.targetID),
		Description: fmt.Sprintf(
			"Transfer control to agent %q. Use when this agent has completed its role "+
				"and the task should continue with %q. Provide a reason and any context "+
				"the receiving agent needs to continue work.",
			h.targetID, h.targetID,
		),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{
					"type":        "string",
					"description": "Why the handoff is happening",
				},
				"context": map[string]any{
					"type":        "string",
					"description": "Information to pass to the receiving agent",
				},
			},
			"required": []string{"reason"},
		},
	}
}

func (h *handoffTool) Execute(_ context.Context, args string) (string, error) {
	var input struct {
		Reason  string `json:"reason"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("handoff: invalid args: %w", err)
	}
	out, _ := json.Marshal(Handoff{
		TargetAgentID: h.targetID,
		Reason:        input.Reason,
		Context:       input.Context,
	})
	return string(out), nil
}

// extractHandoff scans the most recent tool messages in the session for a
// Handoff payload. It only accepts handoffs targeting a known peer (targetIDs)
// to prevent accidental matches against unrelated tool results.
// Returns the first matching handoff found, or nil.
func extractHandoff(s *session.Session, targetIDs []string) *Handoff {
	if len(targetIDs) == 0 {
		return nil
	}
	known := make(map[string]bool, len(targetIDs))
	for _, id := range targetIDs {
		known[id] = true
	}

	events := s.Events()
	// Walk backward — handoff tool results are the most recent events.
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Type != session.EventTypeMessageAdded {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(e.Data, &msg); err != nil {
			continue
		}
		if msg.Role != llm.RoleTool {
			break // moved past the tool result block; stop scanning
		}
		var h Handoff
		if err := json.Unmarshal([]byte(msg.Content), &h); err != nil {
			continue
		}
		if h.TargetAgentID != "" && known[h.TargetAgentID] {
			return &h
		}
	}
	return nil
}

// RecordHandoff appends an EventTypeHandoff event to the session log.
// Called by graph/swarm after a handoff is detected and before routing.
func RecordHandoff(ctx context.Context, s *session.Session, h *Handoff) error {
	return s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeHandoff, h))
}
