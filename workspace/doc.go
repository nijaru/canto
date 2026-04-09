// Package workspace provides validated rooted filesystem access for
// workspace-aware agents and hosts.
//
// Validator canonicalizes the workspace root and rejects malformed,
// absolute, traversal, over-deep, or symlink-escaping paths before Root
// delegates to os.Root for capability-based containment.
package workspace
