// Package memory provides long-term memory orchestration plus the underlying
// storage implementations used to persist it.
//
// Status: experimental within v0. The current Manager-based surface is the
// default entrypoint for Canto consumers today, but it is not yet the final
// long-term public center of gravity. Future memory-shaped contracts may move
// toward operations like remember/search/forget with optional link/context
// capabilities.
//
// Manager is the main entry point for framework-facing memory behavior. It
// coordinates scoped core blocks and durable long-term memories across
// thread/user/agent/workspace/app namespaces, with option-based configuration
// for pluggable write policy, retrieval policy, vectors, and embeddings.
//
// Index is the cheap pointer layer that sits above the repository. It renders
// namespaces, core blocks, and long-term memories as short filetree-style
// summaries so callers can keep a stable memory map in context without loading
// full memory bodies.
//
// Small interfaces such as Writer, Retriever, and Store keep the higher-level
// helpers decoupled from any one concrete implementation. CoreStore is the
// built-in SQLite/FTS5 store, not the only supported backing store shape.
// Repository exposes the lower-level block/memory listing and lookup surface
// that Index and future retrieval planners build on.
// Vector-store details, block layout, and future graph/context capabilities are
// intentionally not stable consumer-facing commitments yet.
//
// CoreStore persists durable memory blocks and text-searchable memories in
// SQLite/FTS5. VectorStore implementations add optional semantic retrieval.
// SQLiteVectorStore is the simplest brute-force option for small collections,
// while HNSWStore targets larger approximate-nearest-neighbor workloads.
//
// Indexer remains available for session-derived semantic indexing when hosts
// want live embeddings of newly appended session messages.
package memory
