package governor

import (
	"context"
	"errors"

	"github.com/nijaru/canto/artifact"
	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// CompactOptions configures manual durable compaction for a session.
type CompactOptions struct {
	MaxTokens    int
	ThresholdPct float64
	MinKeepTurns int
	// Message is an optional instruction passed to the summarizer LLM.
	Message string

	OffloadDir string
	Artifacts  artifact.Store
}

// CompactResult reports the outcome of a manual compaction run.
type CompactResult struct {
	Compacted bool
}

// CompactSession runs durable manual compaction against sess using the
// framework's built-in offload-then-summarize pipeline.
//
// The returned result reports whether the session appended any new durable
// compaction snapshots during the call.
func CompactSession(
	ctx context.Context,
	provider llm.Provider,
	model string,
	sess *session.Session,
	opts CompactOptions,
) (CompactResult, error) {
	if provider == nil {
		return CompactResult{}, errors.New("compact session: provider is required")
	}
	if model == "" {
		return CompactResult{}, errors.New("compact session: model is required")
	}
	if sess == nil {
		return CompactResult{}, errors.New("compact session: session is required")
	}
	if opts.MaxTokens <= 0 {
		return CompactResult{}, errors.New("compact session: max tokens must be > 0")
	}
	if (opts.Artifacts == nil) == (opts.OffloadDir == "") {
		return CompactResult{}, errors.New(
			"compact session: exactly one offload target is required",
		)
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
	summarizer.Message = opts.Message

	before := compactionEventCount(sess)
	builder := ccontext.NewBuilder()
	builder.AppendMutators(offloader, summarizer)
	if err := builder.BuildCommit(ctx, provider, model, sess, &llm.Request{Model: model}); err != nil {
		return CompactResult{}, err
	}

	return CompactResult{Compacted: compactionEventCount(sess) > before}, nil
}

func compactionEventCount(sess *session.Session) int {
	var count int
	for e := range sess.All() {
		if e.Type == session.CompactionTriggered {
			count++
		}
	}
	return count
}
