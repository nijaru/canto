// Package memory provides long-term memory orchestration plus the underlying
// storage implementations used to persist it.
//
// Manager is the main entry point for framework-facing memory behavior. It
// coordinates scoped core blocks and durable long-term memories across
// thread/user/agent/workspace/app namespaces, with option-based configuration
// for pluggable write policy, retrieval policy, vectors, and embeddings.
//
// Small interfaces such as Writer, Retriever, and Store keep the higher-level
// helpers decoupled from any one concrete implementation. CoreStore is the
// built-in SQLite/FTS5 store, not the only supported backing store shape.
//
// CoreStore persists durable memory blocks and text-searchable memories in
// SQLite/FTS5. VectorStore implementations add optional semantic retrieval.
// SQLiteVectorStore is the simplest brute-force option for small collections,
// while HNSWStore targets larger approximate-nearest-neighbor workloads.
//
// Indexer remains available for session-derived semantic indexing when hosts
// want live embeddings of newly appended session messages.
package memory
