// Package service provides typed helpers for authoring tools that call
// external services and APIs.
//
// The package adapts typed Go handlers to Canto's tool.Tool,
// tool.MetadataTool, and tool.ApprovalTool interfaces. It is intentionally
// service-agnostic: product-specific clients belong in applications or
// extension packages.
package service
