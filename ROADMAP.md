# CortexDB Roadmap

This document outlines the planned features and improvements for CortexDB.

## 🟢 v2.13.0 (Current)
- [x] **Lexical Fallback Mode**: Support for `SearchText` and `HybridSearchText` to automatically fallback to FTS5/BM25 when no embedder is configured.
- [x] **LLM-Assisted Retrieval**: `TextSearchOptions` extended with `Keywords` and `AlternateQueries` for multi-query FTS5 expansion via `SearchTextOnly`.
- [x] **External Vector Support**: `UpsertBatchWithAdapt`, `InsertTextWithVector`, `InsertTextBatchWithVectors`, `Quick.AddWithVector`, `Quick.AddBatchWithVectors` for pre-computed vectors.
- [ ] Improved documentation and example suite

## 🟡 v2.14.0 (Planned)
- **Sparse Vector Support**: Better handling of SPLADE/BM25 sparse vectors.
- **Nullable Vectors**: Architectural shift to allow collections/nodes without vectors for pure-lexical apps.

## 🔴 Future (v3.0.0 and beyond)
- **Distributed Mode**: Optional synchronization layer for multi-instance deployments (while remaining embedded-first).
- **Python/JS/Rust SDKs**: Native-like bindings for other languages.

---
*Note: This roadmap is subject to change based on community feedback and research findings.*
