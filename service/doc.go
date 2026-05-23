// Package service provides typed helpers for authoring tools that call
// external services and APIs.
//
// The package adapts typed Go handlers to Canto's tool.Tool,
// tool.MetadataTool, and tool.ApprovalTool interfaces, adding service/API retry
// helpers on top of the lower-level tool.NewTyped authoring path. It is
// intentionally service-agnostic: product-specific clients belong in
// applications or extension packages.
package service
