package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

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
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = toolResult{call: pf.call, err: ctx.Err()}
				return
			}

			results[i] = executeToolSafely(ctx, s, pf, r, h)
		})
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
			output := fmt.Sprintf("Error: tool panicked: %v", recovered)
			res = toolResult{call: pf.call, output: output}
			if err := s.Append(context.WithoutCancel(ctx), session.NewToolCompletedEvent(
				s.ID(),
				session.ToolCompletedData{
					Tool:           pf.call.Function.Name,
					ID:             pf.call.ID,
					IdempotencyKey: pf.idempotencyKey,
					Output:         output,
					Error:          fmt.Sprintf("tool panicked: %v", recovered),
				},
			)); err != nil {
				res.err = err
			}
		}
	}()
	return executeTool(ctx, s, pf, r, h)
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
	terminalCtx := context.WithoutCancel(ctx)
	if err := s.Append(terminalCtx, session.NewToolCompletedEvent(s.ID(), session.ToolCompletedData{
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
