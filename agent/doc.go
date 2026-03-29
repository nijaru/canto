// Package agent provides the turn-based agent loop over durable sessions.
//
// BaseAgent is the default implementation. New wires a default context.Builder
// chain with instructions, tool definitions, effective history, and model
// capability adaptation.
//
// Step executes one model/tool iteration. Turn repeats Step until the agent
// produces a final assistant message, hands off control, or reaches MaxSteps.
//
// Extend the default builder with WithRequestProcessors and WithMutators, and
// configure gated tool execution with WithApprovalManager when a host needs
// pause/resume approval flow. Use WithHooks for ordinary lifecycle hooks and
// WithHookRunner only when replacing the hook runner wholesale.
package agent
