---
description: Reconcile new text against the knowledge graph with an LLM — add, correct, and retract facts (no embedder needed)
---

Keep the knowledge graph **correct**, not just growing. Ingestion only ever adds, so stale and wrong facts accumulate. This shows an LLM the *relevant part of the existing graph* alongside new text and asks for the edits — **add / update / delete** — that make the graph reflect reality.

Needs only a chat model (`CORTEXDB_LLM_BASE_URL`, e.g. a local Ollama at `http://localhost:11434`, plus `CORTEXDB_LLM_MODEL`). **No embedding model required** — the existing entities relevant to the text are found by lexical mention.

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"

# 1. ALWAYS preview first — see the proposed edits without touching the graph
printf '%s' "$TEXT" | "$bin" --graph-update --dry-run --allow-delete

# 2. Apply once the user agrees (drop --allow-delete to apply only adds/updates)
printf '%s' "$TEXT" | "$bin" --graph-update --allow-delete
```

`$TEXT` can be pasted text, or pass a file path instead of piping: `"$bin" --graph-update notes.md --dry-run`.

**Always dry-run first and show the user the proposed edits** (each prints `op kind target — reason`), especially deletions: `--allow-delete` is required for any delete to apply, deletes are capped per run, and they are not reversible. Without `--allow-delete`, deletes are proposed but skipped and the command says so.

$ARGUMENTS
