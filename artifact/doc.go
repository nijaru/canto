// Package artifact provides durable artifact descriptors and pluggable storage.
//
// Canto session events should record artifact descriptors and provenance, while
// artifact bodies live behind a Store implementation. The framework owns
// artifact identity, storage interfaces, and provenance fields; applications
// decide presentation, retention policy, and approval policy.
package artifact
