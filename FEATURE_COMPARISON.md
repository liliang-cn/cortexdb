# Feature Comparison

| Feature | CortexDB | Traditional Vector DBs | SQLite-only |
|---------|----------|------------------------|-------------|
| **Architecture** | Embedded (Go) | Client-Server | Embedded |
| **Storage Engine** | SQLite (Pure Go) | Custom / Various | SQLite |
| **Vector Indexing** | HNSW, IVF, LSH, Flat | HNSW, IVF, etc. | None (typically) |
| **Hybrid Search** | Yes (RRF + FTS5) | Some | Only Keyword |
| **Knowledge Graph** | Built-in | Separate Service | No |
| **Agent Memory** | Built-in (Hindsight) | Requires Logic | No |
| **MCP Support** | Native Tooling | External Adapters | No |
| **CGO Required** | ❌ No | ✅ Often | ❌ No |
| **Setup Overhead** | Zero (go get) | High (Docker/K8s) | Zero |

## Why choose CortexDB?

1. **Zero External Dependencies**: One single SQLite file. 100% Go.
2. **Beyond Vectors**: It's a graph database, a memory system, and a vector engine in one.
3. **Agent-Centric**: Features like Hindsight and MCP are designed specifically for modern AI Agent workflows.
4. **Pure Go**: Easy to cross-compile and deploy to any environment (Edge, Cloud, IoT).
