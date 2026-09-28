---
description: Merge duplicate/alias entities in the CortexDB graph into canonical nodes, and link uncertain ones for review
---

Clean up the knowledge graph by **resolving duplicate entities** — the same thing recorded under different names. Each candidate pair (case / spacing / punctuation variants like `Cortex DB` ↔ `CortexDB`, near spellings like `Postgres` ↔ `PostgreSQL`, and — when `CORTEXDB_LLM_*` is set — acronyms and synonyms like `K8s` ↔ `Kubernetes`) is scored on name similarity, shared neighbours and type agreement:

- **score ≥ 0.95** — merged into one canonical node, edges repointed and deduped. The alias stays readable in history (reason `merged`).
- **0.85 – 0.95** — not merged. A `possiblySame` edge is written between the two, carrying the evidence and graded `held`, so `contract_needs_attention` lists it. Setting that edge's `_grade` to `verified` merges the pair on the next run; `refused` keeps them apart for good.
- **below 0.85** — nothing.

`--merge-all` restores the old behaviour (merge every normalized-key group and every model group, no scoring). Run in a shell:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
"$bin" --resolve-entities --dry-run   # preview the merges first
"$bin" --resolve-entities             # apply them
```

Always **preview with `--dry-run` first** and show the user the proposed merges (`canonical ← [aliases]`) and links (`a ~ b score …`); apply only if they look right. Applying repoints edges to the canonical node, records the merged names as `aliases` on it, and dedupes edges — the pre-merge rows stay in history, but the merge is not automatically reversed, so a dry run is the safe default. Re-running is safe (already-merged names simply don't reappear, answered links are not reopened). Afterwards, walk the user through the `possiblySame` links from `contract_needs_attention`.

$ARGUMENTS
