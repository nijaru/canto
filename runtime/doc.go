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
// and execute the child agent asynchronously. Child runs inherit spawn-context
// cancellation by default. Detached execution is explicit via ChildSpec.
//
// The framework owns execution semantics, lifecycle recording, and concurrency
// boundaries. Applications still decide decomposition policy, model selection,
// approval rules, and merge behavior.
package runtime
