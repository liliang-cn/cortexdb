# Contributing to CortexDB

CortexDB is a pure-Go library — no services or extra infrastructure needed for development.

## Build

```bash
go mod download
go build ./...
```

The gRPC sidecar and MCP server binaries live under `cmd/` (`cortexdb-grpc`, `cortexdb-mcp-stdio`).

## Tests

Run the full suite the same way CI does (race detector + coverage):

```bash
go test -race ./...
```

Examples must compile (CI checks each `examples/*/` with `go build`).

## Eval regression

Retrieval quality has regression floors enforced in CI. Before touching retrieval, chunking, or search code, run:

```bash
go test ./pkg/eval -run TestLexicalRetrievalQuality -v
```

Fuzz-covered surfaces (FTS5, SPARQL, SQL-dump import) can be exercised with `go test ./... -run Fuzz`.

## Rust client (if you touch `clients/rust`)

```bash
go build -o clients/rust/target/cortexdb-grpc ./cmd/cortexdb-grpc
cd clients/rust
cargo fmt --check && cargo clippy --all-features -- -D warnings
CORTEXDB_GRPC_BIN=$PWD/target/cortexdb-grpc cargo test --all-features
```

## Pull requests

- Target the `main` branch; CI must pass (tests, eval floors, example builds, version consistency).
- If you bump the version, keep `version.go` in sync with `plugins/cortexdb/.claude-plugin/plugin.json`, `plugins/cortexdb/.codex-plugin/plugin.json`, and `.claude-plugin/marketplace.json` — CI rejects mismatches.
- Keep commits focused; conventional-commit style (`feat(scope): ...`, `fix: ...`) is used for changelog generation.
