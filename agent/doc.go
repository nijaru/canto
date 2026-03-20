// Package agent provides the turn-based agent loop over durable sessions.
//
// BaseAgent is the default implementation. New wires a default context.Builder
// chain with instructions, tool definitions, effective history, and model
// capability adaptation.
//
// Step executes one model/tool iteration. Turn repeats Step until the agent
// produces a final assistant message, hands off control, or reaches MaxSteps.
//
// New code should prefer WithRequestProcessors and WithMutators when extending
// the default builder. WithProcessors remains available for legacy
// context.Processor integrations.
package agent
