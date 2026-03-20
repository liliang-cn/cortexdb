# CortexDB Roadmap

This document outlines the planned features and improvements for CortexDB.

## 🟢 v2.12.x (Current Focus)
- [x] High-level Knowledge & Memory APIs
- [x] MCP Stdio Server integration
- [ ] Improved documentation and example suite

## 🟡 v2.13.0 (Planned)
- **Lexical Fallback Mode**: Support for `SearchText` and `HybridSearchText` to automatically fallback to FTS5/BM25 when no embedder is configured.
- **LLM-Assisted Retrieval**: Use LLM for query intent parsing and keyword expansion during lexical retrieval.
- **External Vector Support**: Enhanced helpers for ingesting precomputed vectors from upstream pipelines.

## 🔴 Future (v3.0.0 and beyond)
- **Sparse Vector Support**: Better handling of SPLADE/BM25 sparse vectors.
- **Nullable Vectors**: Architectural shift to allow collections/nodes without vectors for pure-lexical apps.
- **Distributed Mode**: Optional synchronization layer for multi-instance deployments (while remaining embedded-first).
- **Python/JS/Rust SDKs**: Native-like bindings for other languages.

---
*Note: This roadmap is subject to change based on community feedback and research findings.*
