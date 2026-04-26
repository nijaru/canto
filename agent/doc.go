// Package agent provides the turn-based agent loop over durable sessions.
//
// BaseAgent is the default implementation. New wires a default prompt.Builder
// chain with instructions, tool definitions, effective history, and model
// capability adaptation.
//
// Run exposes the loop as an iterator over per-step results so callers can
// consume turn progress with native backpressure.
//
// Step executes one model/tool iteration. Turn repeats Step until the agent
// produces a final assistant message, hands off control, waits for input, or
// exhausts its step budget. The turn stop state is exposed on StepResult.
//
// Extend the default builder with WithRequestProcessors and WithMutators, and
// configure gated tool execution with WithApprovalManager when a host needs
// pause/resume approval flow. Use WithBudgetGuard for clean budget exhaustion
// stops, WithHooks for ordinary lifecycle hooks, and WithHookRunner only when
// replacing the hook runner wholesale.
package agent
