// Package approval provides a transport-agnostic approval bridge for pausing
// framework operations until a host explicitly allows or denies them.
//
// Canto owns the durable approval state machine, policy composition, and
// circuit-breaker plumbing. Hosts own product policy: which tools, arguments,
// users, paths, or command strings are safe enough to allow automatically.
// Use PolicyFunc for local deterministic policies, such as a shell-command
// classifier in a terminal agent. Return handled=false to leave the request for
// HITL resolution instead of forcing an automated allow or deny.
package approval
