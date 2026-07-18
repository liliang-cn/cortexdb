---
description: Export all CortexDB memories to Markdown files (Claude Code memory-file layout)
---

Export every memory in the CortexDB brain to a directory of Markdown files — one file per memory with YAML frontmatter (`name` / `description` / `metadata`), plus a `MEMORY.md` index — mirroring Claude Code's file-based memory layout, so the brain is human-readable, diffable, and backup-friendly. Run this in a shell:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
"$bin" --export-memory        # writes to ~/.cortexdb/memory-export/ by default
```

Pass a directory as an argument to export somewhere else (e.g. `--export-memory ./memory`). Then tell the user how many memories were exported and where `MEMORY.md` was written. It reads the memory buckets only (not arbitrary chat history), skips expired memories, and is safe to re-run (it overwrites the export directory).

$ARGUMENTS
