---
description: Open the brain's knowledge graph — a live 3D view, or a static file to send
---

Two views of the same graph. Pick by what the user asked for, and default to the live one.

## Live 3D (default)

Ask for it with the **`serve_graph_3d`** MCP tool. It opens a rotatable, glowing 3D view in the browser and returns its URL. Prefer this whenever the user says show / see / open / watch the graph, or mentions 3D, live, or rotating.

It is served from inside this MCP server, which is what makes it live: nodes and relations appear as they are written, and **every query, save and relation this server handles lights up the nodes it touched** — so the user watching the page sees the brain react to what you do next. Calling the tool again returns the same URL rather than opening a second view.

After it opens, tell the user the URL, and that the view keeps updating on its own — they do not need to re-run anything. Worth mentioning once: **Trace path** picks two nodes and lights the chain between them, **Orbit** turns the scene, and dragging takes the camera back.

If the user wants it from a terminal instead, the binary serves it too — but a command-line view is its own process, so it sees the graph change and *not* the queries:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
"$bin" --graph-3d          # prints the URL, opens a browser, runs until Ctrl-C
```

## Static file (when it has to be sent)

When the user wants something they can **attach to a message, keep, or open without this session running**, render the self-contained file instead — the live view is a server that dies with this process, so it cannot be sent anywhere.

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
html=$("$bin" --graph-html)        # writes to ~/.cortexdb/graph/ and prints the path
echo "$html"
( command -v open >/dev/null && open "$html" ) || ( command -v xdg-open >/dev/null && xdg-open "$html" ) || true
```

Add `--organize` only if asked: it extracts entities from stored text with no LLM, and it **writes to the graph**. It is ignored against a shared brain.

## If the graph looks thin

Say why rather than leaving them to wonder: the graph grows from knowledge saved with entities and relations (`knowledge_save`, `upsert_relations`) or ingested through GraphRAG. Free-text memories with nothing linked do not add nodes.

$ARGUMENTS
