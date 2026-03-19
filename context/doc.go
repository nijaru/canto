// Package context builds model requests from session state.
//
// Builder.BuildPreview runs preview-safe request shaping only. Builder.Build
// and Builder.BuildCommit run commit-time mutation first and then rebuild the
// request from the updated session state.
//
// Legacy Processor implementations remain supported and still declare side
// effects through ProcessorEffects. New code can use RequestProcessor and
// ContextMutator to make the phase split explicit.
//
// History uses session.EffectiveMessages rather than the raw transcript, so
// compaction remains durable across future turns. Offloader and Summarizer
// persist compaction snapshots back into the session log; Offloader also emits
// durable artifact descriptors for externalized content. LazyTools derives
// unlocked tool state from prior search_tools completions recorded in the
// session.
package context
