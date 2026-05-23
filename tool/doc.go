// Package tool defines executable tool contracts and registry helpers.
//
// The common authoring path is NewTyped or MustTyped, which adapt typed Go
// handlers to the provider-facing JSON boundary. Raw Tool implementations and
// Func remain available for dynamic adapters and externally supplied schemas.
//
// A Tool provides an llm.Spec and an Execute method that accepts JSON
// arguments. Registry stores tools by name, exposes deterministic model-facing
// specs, and dispatches execution by tool name.
//
// StreamingTool is an optional extension for tools that can emit incremental
// output while still returning a final combined result.
package tool
