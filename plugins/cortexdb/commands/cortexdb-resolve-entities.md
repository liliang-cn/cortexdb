---
description: Merge duplicate/alias entities in the CortexDB graph into canonical nodes
---

Clean up the knowledge graph by **merging duplicate entities** — the same thing recorded under different names — into one canonical node, repointing every edge and deduping the result. Deterministic by default (case / spacing / punctuation variants like `Cortex DB` ↔ `CortexDB`, `Open AI` ↔ `OpenAI`); when `CORTEXDB_LLM_*` is set it also catches acronyms and synonyms (`K8s` ↔ `Kubernetes`, `Postgres` ↔ `PostgreSQL`). Run in a shell:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
"$bin" --resolve-entities --dry-run   # preview the merges first
"$bin" --resolve-entities             # apply them
```

Always **preview with `--dry-run` first** and show the user the proposed merges (`canonical ← [aliases]`); apply only if they look right. Applying repoints edges to the canonical node, records the merged names as `aliases` on it, and dedupes edges — it is not automatically reversible, so a dry run is the safe default. Re-running is safe (already-merged names simply don't reappear).

$ARGUMENTS
