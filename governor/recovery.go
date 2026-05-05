package governor

import (
	"context"
	"errors"
	"fmt"

	"github.com/nijaru/canto/llm"
)

// CompactFunc performs session compaction. The caller is responsible for
// closing over the session, provider, and options. Returning a non-nil error
// from this function aborts the retry.
type CompactFunc func(ctx context.Context) error

// RecoveryProvider wraps an LLM provider and intercepts context overflow
// errors. On overflow it calls Compact once, then retries the same failed request.
// A second overflow is propagated immediately — recovery runs at most once per
// Generate or Stream call to prevent infinite compaction loops.
//
// The compact callback receives the context from the original call, so
// cancellation propagates correctly.
//
// RecoveryProvider is only appropriate when Compact can make the existing
// request succeed without rebuilding it. Session-backed agents should prefer
// runtime.WithOverflowRecovery, which retries the whole turn so the request is
// rebuilt from compacted durable history.
//
// Construction:
//
//	rp, err := governor.NewRecoveryProvider(baseProvider, compactFn)
//	agent, _ := agent.New(agent.WithProvider(rp), ...)
type RecoveryProvider struct {
	llm.Provider
	compact CompactFunc
}

// NewRecoveryProvider wraps inner so that context overflow errors trigger a
// single compaction retry. compact is called exactly once on the first
// overflow; a second overflow is returned as-is.
func NewRecoveryProvider(inner llm.Provider, compact CompactFunc) (*RecoveryProvider, error) {
	if compact == nil {
		return nil, errors.New("governor: recovery provider requires a compact function")
	}
	return &RecoveryProvider{Provider: inner, compact: compact}, nil
}

// Generate intercepts context overflow errors. On the first overflow it calls
// compact and retries. A second overflow propagates immediately.
func (r *RecoveryProvider) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	resp, err := r.Provider.Generate(ctx, req)
	if err == nil || !r.Provider.IsContextOverflow(err) {
		return resp, err
	}

	if compactErr := r.compact(ctx); compactErr != nil {
		return nil, fmt.Errorf(
			"overflow recovery: compact failed: %w (original: %v)",
			compactErr,
			err,
		)
	}

	return r.Provider.Generate(ctx, req)
}

// Stream intercepts context overflow errors from the initial stream request.
// Streaming errors that occur mid-stream (via stream.Err()) are not
// recoverable through this wrapper since the stream is already in progress.
func (r *RecoveryProvider) Stream(
	ctx context.Context,
	req *llm.Request,
) (llm.Stream, error) {
	s, err := r.Provider.Stream(ctx, req)
	if err == nil || !r.Provider.IsContextOverflow(err) {
		return s, err
	}

	if compactErr := r.compact(ctx); compactErr != nil {
		return nil, fmt.Errorf(
			"overflow recovery: compact failed: %w (original: %v)",
			compactErr,
			err,
		)
	}

	return r.Provider.Stream(ctx, req)
}
