---
description: Turn a codebase (any language) into a CortexDB knowledge graph and open an interactive view
---

Build a **code knowledge graph** from a codebase and load it into CortexDB, so structural questions ("who implements this interface?", "who calls X?", "what does this module depend on?", "is there a dependency cycle?") become graph queries instead of grep. **You are the extractor** — you read the source directly, so this works for **any language** (Go, Python, TypeScript, Rust, Java, …), not just one.

**Target:** `$ARGUMENTS` if given (a path), otherwise the current working directory.

### 1. Scan and extract

Find the source files (respect `.gitignore`; skip `node_modules`, `vendor`, `dist`, `.git`, build output, generated code). Read them and extract a **code graph**. Favor architecture over exhaustiveness:

- **Entities** (nodes) — `package`/`module`, `file` (only when useful), `class`/`type`, `interface`, `function`/`method`, `const`. Prefer exported/public and structurally important symbols; don't emit every private one-line helper. Give each a short `summary` and its `file`.
- **Relations** (edges) — `imports`, `defines`, `has_method`, `implements`, `extends`, `calls`, `references`. Only relations actually present in the code.

For a **large repo, work directory-by-directory** (or dispatch parallel subagents, one per top-level package) and accumulate. Keep entity names stable and unambiguous (e.g. package-qualified) so edges connect. Use precise tooling when it helps and exists (e.g. `go list` for Go import edges) — but the extraction itself is yours.

Write the result to a temp file as JSON in this shape:

```json
{
  "language": "go | python | typescript | mixed",
  "entities": [
    { "name": "pkg/foo", "type": "package", "file": "pkg/foo", "summary": "…" },
    { "name": "Embedder", "type": "interface", "file": "pkg/foo/embed.go", "summary": "…" }
  ],
  "relations": [
    { "from": "pkg/bar", "to": "pkg/foo", "type": "imports" },
    { "from": "openAIEmbedder", "to": "Embedder", "type": "implements" }
  ]
}
```

### 2. Load into an isolated graph and open it

Keep the code graph in its **own database** (not the personal brain). Run in a shell — set `JSON` to the temp file you wrote and `NAME` to the repo's basename:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
export CORTEXDB_PATH="$HOME/.cortexdb/code-graphs/${NAME}.db"
mkdir -p "$(dirname "$CORTEXDB_PATH")"
"$bin" --import-code-graph "$JSON"                       # upserts entities + relations
html=$("$bin" --graph-html "$HOME/.cortexdb/code-graphs/${NAME}")   # renders + prints path
echo "$html"
( command -v open >/dev/null && open "$html" ) || ( command -v xdg-open >/dev/null && xdg-open "$html" ) || true
```

(To instead merge the code graph into the personal brain — so the live `expand_graph` / SPARQL MCP tools can query it — leave `CORTEXDB_PATH` unset. Isolation is the default because a code graph is large and distinct from personal memory.)

### 3. Report

Tell the user how many entities/relations were loaded, where the HTML was written (and that it is open), and demonstrate the payoff with 2–3 real answers pulled from *their* graph (e.g. what implements a key interface, what depends on a core module, whether any dependency cycle exists). Re-running refreshes in place (ids are stable).
