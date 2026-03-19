// Package context builds model requests from session state.
//
// Processors always rewrite the in-flight llm.Request and may optionally
// declare side effects through ProcessorEffects when they also append durable
// session facts or write external artifacts.
//
// History uses session.EffectiveMessages rather than the raw transcript, so
// compaction remains durable across future turns. Offloader and Summarizer
// persist compaction snapshots back into the session log; LazyTools derives
// unlocked tool state from prior search_tools completions recorded in the
// session.
package context
