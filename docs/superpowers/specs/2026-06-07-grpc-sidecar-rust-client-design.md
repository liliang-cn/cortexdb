# gRPC Sidecar Server + Rust Client — Design

Date: 2026-06-07
Status: approved (pending implementation plan)

## Goal

Make CortexDB usable from Rust (and, later, other languages) while keeping the
single Go implementation. Ship:

1. A gRPC sidecar server binary (`cmd/cortexdb-grpc`) exposing the full
   `pkg/cortexdb` facade surface.
2. A typed Rust client crate (`cortexdb-client`) published to crates.io, with
   an optional feature that downloads and manages the sidecar binary
   automatically.

Non-goals: a Rust port of the engine, C-FFI bindings, exposing `pkg/core` /
`pkg/graph` engine internals, streaming RPCs (v1 is unary-only).

## Architecture

```
Rust app ──tonic/gRPC──► cortexdb-grpc (Go sidecar) ──► pkg/cortexdb.DB ──► one SQLite file
```

Everything stays layered against the public facade. The server is a thin
conversion shell; no business logic lives outside `pkg/cortexdb`.

## 1. Proto contract — `proto/cortexdb/v1/`

Proto package `cortexdb.v1`. Files mirror the facade's topic files; all RPCs
unary. Messages mirror the existing `XRequest` / `XResponse` Go structs.

| File | Service | RPCs |
|---|---|---|
| `knowledge.proto` | `KnowledgeService` | SaveKnowledge, UpdateKnowledge, GetKnowledge, SearchKnowledge, DeleteKnowledge |
| `memory.proto` | `MemoryService` | SaveMemory, UpdateMemory, GetMemory, SearchMemory, DeleteMemory |
| `graph.proto` | `KnowledgeGraphService` | UpsertNamespace, ListNamespaces, UpsertKnowledgeGraph, FindKnowledgeGraph, DeleteKnowledgeGraph, ImportKnowledgeGraph, ExportKnowledgeGraph, QuerySPARQL, ValidateSHACL, RefreshInference, SummarizeInference, ExplainInference, ExplainInferenceMatch, SaveOntologySchema, GetOntologySchema, ListOntologySchemas, DeleteOntologySchema |
| `graphrag.proto` | `GraphRAGService` | InsertGraphDocument, SearchGraphRAG, InsertText, InsertTextBatch, SearchText, HybridSearchText |
| `tools.proto` | `ToolsService` | ListTools, CallTool |
| `admin.proto` | `AdminService` | Health, Info |

Notes:

- `ToolsService.CallTool(name, json_args) → json_result` is a generic escape
  hatch dispatching over the same toolbox the MCP server uses, so new tools
  become callable from Rust without proto changes.
- Open-ended `map[string]string` metadata stays as proto maps; deeply nested
  option structs (e.g. `GraphRAGQueryOptions`) get mirrored messages.
- `AdminService.Info` returns `version` (from `pkg/cortexdb.Version`),
  `db_path`, `has_embedder`.
- Error mapping: not-found → `NOT_FOUND`, validation errors →
  `INVALID_ARGUMENT`, everything else → `INTERNAL` with the Go error message.

## 2. Go side

- **`pkg/rpc/v1/`** — generated Go code (committed; regenerated via
  `go:generate` / a `make proto` step using `protoc` + `protoc-gen-go` +
  `protoc-gen-go-grpc`).
- **`pkg/rpcserver/`** — service implementations. Each handler converts
  proto ⇄ existing `cortexdb` Request/Response structs and delegates to
  `*cortexdb.DB`. Conversion only.
- **`cmd/cortexdb-grpc/`** — the binary. Config via env/flags (`.env`
  supported via godotenv, matching `cmd/cortexdb-mcp-stdio`):
  - `CORTEXDB_PATH` (default `cortexdb.db`)
  - `CORTEXDB_GRPC_ADDR` (default `127.0.0.1:47821`, localhost-only)
  - `CORTEXDB_GRPC_TOKEN` — when set, a unary interceptor requires
    `authorization: Bearer <token>` metadata on every RPC (including
    `AdminService.Health`); mismatch or absence returns `UNAUTHENTICATED`.
    Comparison is constant-time; error messages never echo the token. When
    unset, auth is disabled (zero-config localhost default). The token rides
    plaintext gRPC — fine for localhost/trusted networks; cross-machine
    deployments must add TLS or a reverse proxy themselves (v1 ships no
    built-in TLS).
  - `OPENAI_BASE_URL` / `OPENAI_API_KEY` / `CORTEXDB_EMBED_MODEL` — when set,
    the binary wires an OpenAI-compatible embeddings client implemented with
    plain `net/http`, living under `cmd/cortexdb-grpc` (the "no LLM SDKs under
    `pkg/`" rule holds). When unset, the server runs in lexical mode — the
    existing first-class no-embedder path.
- `go.mod` gains `google.golang.org/grpc` + `google.golang.org/protobuf`,
  following the MCP-SDK precedent; Go 1.17+ module pruning keeps library-only
  consumers unaffected.

## 3. Rust client — `clients/rust/cortexdb-client/`

- tonic + prost; `build.rs` compiles protos vendored into the crate (copied
  from `proto/` by the regen step) so the crate publishes standalone.
- Entry type: `CortexClient::connect(addr)` exposing `.knowledge()`,
  `.memory()`, `.graph()`, `.graphrag()`, `.tools()`, `.admin()` sub-clients.
- Auth: `CortexClient::builder(addr).token(t).connect()` attaches
  `authorization: Bearer <token>` to every request via a tonic interceptor;
  plain `connect(addr)` sends no token (matches the server's no-auth default).
- `examples/` with save→search round-trips for RAG, memory, and SPARQL.

### Sidecar distribution (C + A)

- **Base path (always):** the crate is a pure client. Docs state how to get
  the server: `go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest`
  or download a prebuilt release binary.
- **Optional `managed-server` feature (runtime download, esbuild/playwright
  pattern):** adds a `Sidecar` module:
  1. `Sidecar::ensure()` resolves a binary: `$CORTEXDB_GRPC_BIN` → `PATH` →
     download the platform-matching prebuilt from GitHub Releases into
     `~/.cache/cortexdb/bin/<version>/`, verifying sha256.
  2. `sidecar.spawn(db_path)` launches it on a random high ephemeral port
     with a freshly generated random token (passed via `CORTEXDB_GRPC_TOKEN`
     to the child and pre-configured on the returned client), and returns the
     address; the child process is killed on drop. Managed mode is therefore
     authenticated by default.
  - Binary version is pinned to the crate version (download URL embeds the
    release tag). No build-time network access — `cargo build` stays offline
    and docs.rs builds work.
- The default feature set does NOT include `managed-server` (zero extra deps
  for plain clients).

### Release prerequisite

The repo's release flow must publish multi-platform prebuilt `cortexdb-grpc`
binaries (darwin/linux/windows × amd64/arm64) with sha256 checksums on GitHub
Releases — via goreleaser or a GitHub Actions matrix — so `managed-server`
has something to download.

## 4. Testing & CI

- **Go:** `pkg/rpcserver` tests run the server in-process over `bufconn`,
  covering both embedder mode (fake embedder) and lexical mode, matching the
  existing dual-mode test convention. Auth interceptor tests cover: no token
  configured (open), valid token, missing/wrong token (`UNAUTHENTICATED`).
- **Rust:** `cargo test` integration test spawns the locally built
  `cortexdb-grpc` binary on an ephemeral port against a temp DB. The
  `managed-server` download path is unit-tested with a mocked release URL;
  the real download is not exercised in CI.
- **CI:** new job builds the server and runs `cargo fmt --check`, `clippy`,
  `cargo test`. Existing Go gates (`go build ./...`, `go test -race`)
  unchanged. Release workflow gains the multi-platform binary matrix.

## 5. Docs

README + README_CN + SKILL.md get a "Use from Rust (gRPC sidecar)" section
covering: starting the sidecar manually, the env config, the crate's connect
example, and the `managed-server` feature. (Repo convention: keep all three
in sync.)
