// Package memory provides mutable core memory and retrieval-oriented stores.
//
// CoreStore persists small structured session memory such as personas, distilled
// episodes, and general-purpose knowledge items. All three are backed by SQLite
// with FTS5 for full-text keyword search. VectorStore implementations persist
// embeddings for semantic retrieval. SQLiteVectorStore is the simplest
// brute-force option for small collections, while HNSWStore targets larger
// approximate-nearest-neighbor workloads.
//
// Indexer bridges the two worlds by watching session message events and
// embedding new text into a VectorStore for later recall.
package memory
