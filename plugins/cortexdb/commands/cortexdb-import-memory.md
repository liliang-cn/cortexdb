---
description: Import Claude Code / Codex memory (memory files + CLAUDE.md) into the CortexDB brain
---

Import the local agent memory — the file-based memory store (`~/.claude/projects/*/memory/*.md`) plus `CLAUDE.md` / `AGENTS.md` — into the CortexDB global brain, so it becomes searchable via `knowledge_memory_recall`. The import then **organizes it into the knowledge graph** (entities + relations, deterministic — no LLM), so the memory is graph-queryable too (`expand_graph` / `/cortexdb-graph`). Run this in a shell:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
"$bin" --import-agent-memory        # scans ~/.claude and ~/.codex by default
```

Then tell the user how many items were imported (the command prints a count and the ids). Re-running is safe — ids are stable, so it refreshes rather than duplicates. To scan extra roots, pass them as arguments (e.g. a specific project dir).

$ARGUMENTS
