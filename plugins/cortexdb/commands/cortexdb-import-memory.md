---
description: Import what Claude Code and Codex already remember into the CortexDB brain
---

Bring the user's existing agent memory into the brain, so `knowledge_memory_recall` finds it. Use the `import_agent_memory` MCP tool:

1. Call it with `dry_run: true` and tell the user what it found per source: Claude Code memory notes (`~/.claude/projects/*/memory/*.md`), `CLAUDE.md` / `AGENTS.md`, Codex's memories (`~/.codex/memories_1.sqlite`, `~/.codex/memories`). Add `sources: ["claude_memory", "instructions", "codex_memory", "claude_sessions", "codex_sessions"]` to also count past session transcripts (`since: "30d"` narrows them).
2. Run it for real with the same arguments. Past sessions are distilled by a model, a few per call (`max_sessions`, default 5): while the result says sessions are pending, call again. If it notes that no model is configured, tell the user to set `CORTEXDB_LLM_BASE_URL` / `CORTEXDB_LLM_MODEL` for the MCP server.
3. Report what was imported and into which brain (`brain` in the result).

Re-running is safe — ids are stable, so it refreshes rather than duplicates, and a distilled session is not read again. Without the MCP tool, the same import runs from a shell:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
"$bin" --import-agent-memory --dry-run            # count first
"$bin" --import-agent-memory [--sessions] [--since 30d] [--max N]
```

$ARGUMENTS
