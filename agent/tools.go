package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/tracing"
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

func hookContextOutput(event hook.Event, results []*hook.Result) string {
	var out strings.Builder
	for _, res := range results {
		if res == nil || res.Output == "" {
			continue
		}
		fmt.Fprintf(&out, "<hook_context name=%q>\n%s\n</hook_context>\n", event, res.Output)
	}
	return out.String()
}

func toolHookData(
	call llm.Call,
	metadata tool.Metadata,
	extra map[string]any,
) map[string]any {
	data := map[string]any{
		"tool": call.Function.Name,
		"args": call.Function.Arguments,
	}
	if metadataPresent(metadata) {
		data["metadata"] = metadata
	}
	for key, value := range extra {
		data[key] = value
	}
	return data
}

func metadataPresent(metadata tool.Metadata) bool {
	return metadata.Category != "" ||
		metadata.ReadOnly ||
		metadata.Concurrency != tool.Unknown ||
		metadata.Deferred ||
		len(metadata.Examples) > 0
}

func applyPreToolHookData(call *llm.Call, results []*hook.Result) {
	for _, res := range results {
		if res == nil {
			continue
		}
		name, ok := stringHookData(res.Data, "tool")
		if ok {
			call.Function.Name = name
		}
		args, ok := stringHookData(res.Data, "args")
		if ok {
			call.Function.Arguments = args
		}
	}
}

func applyPostToolHookData(output *string, execErr *error, results []*hook.Result) {
	for _, res := range results {
		if res == nil {
			continue
		}
		if nextOutput, ok := stringHookData(res.Data, "output"); ok {
			*output = nextOutput
		}
		if !hookDataPresent(res.Data, "error") {
			continue
		}
		nextErr, ok := stringHookData(res.Data, "error")
		if !ok || nextErr == "" {
			*execErr = nil
			continue
		}
		*execErr = errors.New(nextErr)
	}
}

func stringHookData(data map[string]any, key string) (string, bool) {
	if !hookDataPresent(data, key) {
		return "", false
	}
	value, ok := data[key].(string)
	return value, ok
}

func hookDataPresent(data map[string]any, key string) bool {
	if data == nil {
		return false
	}
	_, ok := data[key]
	return ok
}

func postHookBlockOutput(err error, currentOutput string) string {
	message := fmt.Sprintf("Error: %v", err)
	if strings.TrimSpace(currentOutput) == "" {
		return message
	}
	return strings.TrimSpace(currentOutput) + "\n" + message
}

func getHandoffTargets(r *tool.Registry) []string {
	if r == nil {
		return nil
	}
	var targets []string
	for _, spec := range r.Specs() {
		if after, ok := strings.CutPrefix(spec.Name, "transfer_to_"); ok {
			targets = append(targets, after)
		}
	}
	return targets
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
		if err := s.Append(ctx, session.NewEvent(s.ID(), session.MessageAdded, toolMsg)); err != nil {
			return StepResult{}, err
		}
	}

	handoff := extractHandoff(s, handoffTargets)
	return StepResult{
		Handoff:     handoff,
		ToolResults: toolMsgs,
	}, nil
}

// preflightTools runs sequential validation for all tool calls: registry lookup,
// PreToolUse hooks, and approval checks. This runs in source order so that hooks
// can depend on the results of sibling validations.
func preflightTools(
	ctx context.Context,
	s *session.Session,
	calls []llm.Call,
	r *tool.Registry,
	h *hook.Runner,
	approvals *approval.Gate,
	assistantMessageID string,
) []preflightResult {
	results := make([]preflightResult, len(calls))
	fence := tool.ACRFence{}

	for i, call := range calls {
		results[i].call = call

		// PreToolUse hooks — run sequentially so they can inspect sibling state.
		var metadata tool.Metadata
		if r != nil {
			metadata, _ = r.Metadata(call.Function.Name)
		}
		var hookOutput string
		if h != nil {
			hookResults, err := h.Run(
				ctx,
				hook.EventPreToolUse,
				hook.SessionMeta{ID: s.ID()},
				toolHookData(call, metadata, nil),
			)
			hookOutput = hookContextOutput(hook.EventPreToolUse, hookResults)
			applyPreToolHookData(&call, hookResults)
			results[i].call = call
			if err != nil {
				results[i].err = &escalationError{
					scope:       "tool",
					target:      call.Function.Name,
					message:     fmt.Sprintf("hook blocked tool %q: %v", call.Function.Name, err),
					recoverable: true,
					cause:       err,
					toolMessage: &llm.Message{
						Role:    llm.RoleTool,
						Content: fmt.Sprintf("Error: %v", err),
						ToolID:  call.ID,
						Name:    call.Function.Name,
					},
				}
				results[i].skipExecute = true
				continue
			}
		}

		// Registry lookup.
		if r == nil {
			results[i].output = hookOutput + fmt.Sprintf(
				"Error: no tool registry configured; cannot execute %q",
				call.Function.Name,
			)
			results[i].skipExecute = true
			continue
		}
		t, ok := r.Get(call.Function.Name)
		if !ok && call.Function.Name == tool.SearchToolName {
			t = tool.NewSearchTool(r)
			ok = true
		}
		if !ok {
			results[i].output = hookOutput + fmt.Sprintf(
				"Error: tool %q not found",
				call.Function.Name,
			)
			results[i].skipExecute = true
			continue
		}
		metadata = tool.MetadataFor(t)
		t = tracing.WrapTool(t)
		results[i].tool = t
		metadata = tool.MetadataFor(t)
		results[i].metadata = metadata

		// Approval check.
		if approvals != nil {
			if gated, ok := t.(tool.ApprovalTool); ok {
				req, needsApproval, err := gated.ApprovalRequirement(call.Function.Arguments)
				if err != nil {
					results[i].err = fmt.Errorf(
						"approval requirement for %q: %w",
						call.Function.Name,
						err,
					)
					results[i].skipExecute = true
					continue
				}
				if needsApproval {
					res, err := approvals.Request(
						ctx,
						s,
						call.Function.Name,
						call.Function.Arguments,
						req,
					)
					if err != nil {
						results[i].err = &escalationError{
							scope:  "tool",
							target: call.Function.Name,
							message: fmt.Sprintf(
								"approval request for %q failed: %v",
								call.Function.Name,
								err,
							),
							recoverable: true,
							cause:       err,
							toolMessage: &llm.Message{
								Role:    llm.RoleTool,
								Content: fmt.Sprintf("Error: %v", err),
								ToolID:  call.ID,
								Name:    call.Function.Name,
							},
						}
						results[i].skipExecute = true
						continue
					}
					if denyErr := res.Error(); denyErr != nil {
						results[i].err = &escalationError{
							scope:  "tool",
							target: call.Function.Name,
							message: fmt.Sprintf(
								"tool %q denied: %v",
								call.Function.Name,
								denyErr,
							),
							recoverable: true,
							cause:       denyErr,
							toolMessage: &llm.Message{
								Role:    llm.RoleTool,
								Content: fmt.Sprintf("Error: %v", denyErr),
								ToolID:  call.ID,
								Name:    call.Function.Name,
							},
						}
						results[i].skipExecute = true
						continue
					}
				}
			}
		}

		idempotencyKey := toolIdempotencyKey(s.ID(), assistantMessageID, call, i)
		results[i].idempotencyKey = idempotencyKey
		decision, err := fence.Validate(s, idempotencyKey)
		if err != nil {
			results[i].err = err
			results[i].skipExecute = true
			continue
		}
		if decision.Action == tool.ReplayReuse {
			results[i].output = hookOutput + decision.Output
			results[i].skipExecute = true
			continue
		}

		results[i].output = hookOutput
	}

	return results
}

// executeTools runs approved tool calls concurrently with a semaphore.
// PreToolUse hooks and approvals have already been resolved in preflightTools.
func executeTools(
	ctx context.Context,
	s *session.Session,
	preflight []preflightResult,
	r *tool.Registry,
	h *hook.Runner,
	maxParallel int,
) []toolResult {
	results := make([]toolResult, len(preflight))
	for i := 0; i < len(preflight); {
		pf := preflight[i]
		if pf.skipExecute || pf.err != nil {
			results[i] = toolResult{call: pf.call, output: pf.output, err: pf.err}
			i++
			continue
		}
		if pf.metadata.Concurrency != tool.Parallel || maxParallel == 1 {
			results[i] = executeToolSafely(ctx, s, pf, r, h)
			i++
			continue
		}

		batchStart := i
		for i < len(preflight) && canRunInParallel(preflight[i]) {
			i++
		}
		executeParallelBatch(ctx, s, preflight, results, batchStart, i, r, h, maxParallel)
	}

	return results
}

func canRunInParallel(pf preflightResult) bool {
	return !pf.skipExecute && pf.err == nil && pf.metadata.Concurrency == tool.Parallel
}

func executeParallelBatch(
	ctx context.Context,
	s *session.Session,
	preflight []preflightResult,
	results []toolResult,
	start int,
	end int,
	r *tool.Registry,
	h *hook.Runner,
	maxParallel int,
) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)

	for i := start; i < end; i++ {
		pf := preflight[i]
		wg.Add(1)
		go func(i int, pf preflightResult) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = toolResult{call: pf.call, err: ctx.Err()}
				return
			}

			results[i] = executeToolSafely(ctx, s, pf, r, h)
		}(i, pf)
	}
	wg.Wait()
}

func executeToolSafely(
	ctx context.Context,
	s *session.Session,
	pf preflightResult,
	r *tool.Registry,
	h *hook.Runner,
) (res toolResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			res = toolResult{call: pf.call, err: toolPanicError(pf.call, recovered)}
		}
	}()
	return executeTool(ctx, s, pf, r, h)
}

func toolPanicError(call llm.Call, recovered any) error {
	return &escalationError{
		scope:  "tool",
		target: call.Function.Name,
		message: fmt.Sprintf(
			"tool %q panicked: %v",
			call.Function.Name,
			recovered,
		),
		recoverable: true,
		cause: fmt.Errorf(
			"tool %q panicked: %v",
			call.Function.Name,
			recovered,
		),
		toolMessage: &llm.Message{
			Role:    llm.RoleTool,
			Content: fmt.Sprintf("Error: tool panicked: %v", recovered),
			ToolID:  call.ID,
			Name:    call.Function.Name,
		},
	}
}

// executeTool runs a single tool and its PostToolUse hooks. Preflight
// validation has already completed; this only performs I/O.
func executeTool(
	ctx context.Context,
	s *session.Session,
	pf preflightResult,
	r *tool.Registry,
	h *hook.Runner,
) toolResult {
	call := pf.call
	output := pf.output // hook context from preflight
	t := pf.tool
	if t == nil {
		// Should not happen — preflight already checked — but guard defensively.
		output = fmt.Sprintf("Error: tool %q not found", call.Function.Name)
		return toolResult{call: call, output: output}
	}

	if err := s.Append(ctx, session.NewToolStartedEvent(s.ID(), session.ToolStartedData{
		Tool:           call.Function.Name,
		Arguments:      call.Function.Arguments,
		ID:             call.ID,
		IdempotencyKey: pf.idempotencyKey,
	})); err != nil {
		return toolResult{call: call, output: output, err: err}
	}

	var execErr error
	if st, ok := t.(tool.StreamingTool); ok {
		for delta, err := range st.ExecuteStreaming(ctx, call.Function.Arguments) {
			if err != nil {
				execErr = err
				break
			}
			output += delta
			_ = s.Append(
				ctx,
				session.NewEvent(s.ID(), session.ToolOutputDelta, map[string]any{
					"tool":  call.Function.Name,
					"id":    call.ID,
					"delta": delta,
				}),
			)
		}
	} else {
		var execOutput string
		execOutput, execErr = t.Execute(ctx, call.Function.Arguments)
		output += execOutput
	}

	if execErr != nil {
		output = strings.TrimSpace(
			strings.TrimSpace(output) + "\n" + fmt.Sprintf("Error: %s", execErr),
		)
		if h != nil {
			metadata, _ := r.Metadata(call.Function.Name)
			hookResults, hookErr := h.Run(
				ctx,
				hook.EventPostToolUseFailure,
				hook.SessionMeta{ID: s.ID()},
				toolHookData(call, metadata, map[string]any{
					"output": output,
					"error":  execErr.Error(),
				}),
			)
			applyPostToolHookData(&output, &execErr, hookResults)
			if hookErr != nil {
				output = postHookBlockOutput(hookErr, output)
				execErr = nil
			}
		}
	} else {
		if h != nil {
			metadata, _ := r.Metadata(call.Function.Name)
			hookResults, hookErr := h.Run(
				ctx,
				hook.EventPostToolUse,
				hook.SessionMeta{ID: s.ID()},
				toolHookData(call, metadata, map[string]any{"output": output}),
			)
			applyPostToolHookData(&output, &execErr, hookResults)
			if hookErr != nil {
				output = postHookBlockOutput(hookErr, output)
				execErr = nil
			}
		}
	}
	if execErr != nil && !strings.Contains(output, execErr.Error()) {
		output = strings.TrimSpace(
			strings.TrimSpace(output) + "\n" + fmt.Sprintf("Error: %s", execErr),
		)
	}

	res := toolResult{call: call, output: output}
	var errorText string
	if execErr != nil {
		errorText = execErr.Error()
	}
	if err := s.Append(ctx, session.NewToolCompletedEvent(s.ID(), session.ToolCompletedData{
		Tool:           call.Function.Name,
		ID:             call.ID,
		IdempotencyKey: pf.idempotencyKey,
		Output:         output,
		Error:          errorText,
	})); err != nil {
		res.err = err
	}
	return res
}

// executeToolWithHooks is kept for backward compatibility with callers that
// bypass the two-phase model. New code should use preflightTools + executeTools.
func executeToolWithHooks(
	ctx context.Context,
	s *session.Session,
	call llm.Call,
	r *tool.Registry,
	h *hook.Runner,
	approvals *approval.Gate,
) toolResult {
	pfResults := preflightTools(ctx, s, []llm.Call{call}, r, h, approvals, "")
	execResults := executeTools(ctx, s, pfResults, r, h, 1)
	return execResults[0]
}

func toolIdempotencyKey(sessionID, assistantMessageID string, call llm.Call, index int) string {
	sum := sha256.Sum256([]byte(call.Function.Arguments))
	return fmt.Sprintf(
		"%s:%s:%s:%d:%s",
		sessionID,
		assistantMessageID,
		call.Function.Name,
		index,
		hex.EncodeToString(sum[:]),
	)
}
