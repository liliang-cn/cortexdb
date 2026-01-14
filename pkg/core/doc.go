// Package core provides the core storage and retrieval engine for sqvect.
//
// It implements vector storage using SQLite as the primary backend, supported by
// specialized in-memory indexes (HNSW, IVF) for high-performance approximate
// nearest neighbor (ANN) search.
//
// # Key Components
//
//   - SQLiteStore: The main entry point for data operations, managing both persistent SQL data and memory indexes.
//   - Store Interface: Defines the standard operations for vector storage, document management, and chat memory.
//   - DimensionAdapter: Automatically handles vector dimension mismatches based on configurable policies.
//   - Hybrid Search: Combines HNSW/IVF vector search with SQLite FTS5 keyword search.
//   - Metadata Filtering: Efficiently filters results using JSON-extract SQL push-down.
//   - ACL: Provides row-level security for multi-user/department environments.
//
// # Observability
//
// Since v2.0.0, the core engine supports pluggable structured logging through the Logger interface.
package core
