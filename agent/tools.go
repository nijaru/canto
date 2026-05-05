package agent

import (
	"context"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

type toolResult struct {
	call   llm.Call
	output string
	err    error
}

// preflightResult holds the outcome of sequential validation for one tool call.
type preflightResult struct {
	call           llm.Call
	idempotencyKey string
	tool           tool.Tool
	metadata       tool.Metadata
	output         string // non-empty if preflight produced output (error or hook context)
	err            error  // non-nil if preflight blocked execution
	skipExecute    bool   // true if execution should be skipped (preflight handled it)
}

func runTools(
	ctx context.Context,
	s *session.Session,
	calls []llm.Call,
	r *tool.Registry,
	h *hook.Runner,
	approvals *approval.Gate,
	handoffTargets []string,
	maxParallel int,
	assistantMessageID string,
) (StepResult, error) {
	if maxParallel <= 0 {
		maxParallel = 10
	}

	// Phase 1: sequential preflight — validate, run PreToolUse hooks, check approvals.
	preflight := preflightTools(ctx, s, calls, r, h, approvals, assistantMessageID)

	// Phase 2: metadata-driven execution — parallel-safe tools fan out in waves
	// while serialized or unknown tools remain in source order.
	results := executeTools(ctx, s, preflight, r, h, maxParallel)

	var toolMsgs []llm.Message
	messageCtx := context.WithoutCancel(ctx)
	for _, r := range results {
		if r.err != nil {
			return StepResult{}, r.err
		}
		toolMsg := llm.Message{
			Role:    llm.RoleTool,
			Content: r.output,
			ToolID:  r.call.ID,
			Name:    r.call.Function.Name,
		}
		toolMsgs = append(toolMsgs, toolMsg)
		if err := s.Append(messageCtx, session.NewEvent(s.ID(), session.MessageAdded, toolMsg)); err != nil {
			return StepResult{}, err
		}
	}

	handoff := extractHandoff(s, handoffTargets)
	return StepResult{
		Handoff:     handoff,
		ToolResults: toolMsgs,
	}, nil
}
