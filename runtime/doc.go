// Package runtime executes agents against durable sessions.
//
// Runner is the main entry point for single-session execution. It keeps one
// in-memory session object per session ID so appends, subscriptions, and runs
// observe the same live state while persistence stays in the session.Store.
//
// By default, Runner uses built-in local coordination to serialize work within
// a session and allow parallel work across different sessions. When a
// Coordinator is configured, the same execution path can run through a custom
// lease-and-queue coordinator instead.
//
// ChildRunner builds on the same model for parent/child orchestration:
// materialize a child session, record durable lifecycle events in the parent,
// and execute the child agent through one delegation surface. Spawn/Wait is the
// asynchronous path; Run is the synchronous convenience wrapper. Runner adds
// scheduled child delegation on top of the same substrate. Runner also exposes
// Bootstrap snapshots for seeding a session with workspace and tool context at
// the start of a run. Child runs inherit spawn-context cancellation by default.
// Detached execution is explicit via ChildSpec. Waiting children emit durable
// blocked lifecycle events instead of being reported as completed.
//
// Runner also owns a shared child delegation surface via Delegate, SpawnChild,
// and WaitChild so hosts do not need to manage ChildRunner handles manually.
// Option-based constructors plus Runner.ChildRunner still provide the helper
// path for callers that need an explicitly separate child worker.
//
// The framework owns execution semantics, lifecycle recording, and concurrency
// boundaries. Applications still decide decomposition policy, model selection,
// approval rules, and merge behavior.
package runtime
