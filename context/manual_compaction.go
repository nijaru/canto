package context

import (
	"context"
	"errors"

	"github.com/nijaru/canto/artifact"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// CompactOptions configures manual durable compaction for a session.
type CompactOptions struct {
	MaxTokens    int
	ThresholdPct float64
	MinKeepTurns int

	OffloadDir string
	Artifacts  artifact.Store
}

// CompactSession runs durable manual compaction against sess using the
// framework's built-in offload-then-summarize pipeline.
func CompactSession(
	ctx context.Context,
	provider llm.Provider,
	model string,
	sess *session.Session,
	opts CompactOptions,
) error {
	if provider == nil {
		return errors.New("compact session: provider is required")
	}
	if model == "" {
		return errors.New("compact session: model is required")
	}
	if sess == nil {
		return errors.New("compact session: session is required")
	}
	if opts.MaxTokens <= 0 {
		return errors.New("compact session: max tokens must be > 0")
	}
	if (opts.Artifacts == nil) == (opts.OffloadDir == "") {
		return errors.New("compact session: exactly one offload target is required")
	}

	thresholdPct := opts.ThresholdPct
	if thresholdPct == 0 {
		thresholdPct = defaultThresholdPct
	}
	minKeepTurns := opts.MinKeepTurns
	if minKeepTurns == 0 {
		minKeepTurns = defaultMinKeepTurns
	}

	var offloader *Offloader
	if opts.Artifacts != nil {
		offloader = NewArtifactOffloader(opts.MaxTokens, opts.Artifacts)
	} else {
		offloader = NewOffloader(opts.MaxTokens, opts.OffloadDir)
	}
	offloader.ThresholdPct = thresholdPct
	offloader.MinKeepTurns = minKeepTurns

	summarizer := NewSummarizer(opts.MaxTokens, provider, model)
	summarizer.ThresholdPct = thresholdPct
	summarizer.MinKeepTurns = minKeepTurns

	builder := NewBuilder(offloader, summarizer)
	return builder.BuildCommit(ctx, provider, model, sess, &llm.Request{Model: model})
}
