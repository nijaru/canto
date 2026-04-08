// Package hook executes lifecycle hooks around sessions and tool use.
//
// Hooks may run as subprocess Commands or in-process Func implementations.
// Runner fans out hooks registered for each Event and interprets their result
// as proceed, log-only, or block. Hooks may also return structured Data for
// event-specific mutation handled by the caller.
//
// SessionMeta intentionally carries identifiers rather than the full session so
// hook integration stays lightweight and applications control what additional
// data is exposed.
package hook
