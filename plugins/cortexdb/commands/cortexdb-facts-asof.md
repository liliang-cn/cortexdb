---
description: Show CortexDB knowledge-graph facts valid at a point in time (as-of / bitemporal query)
---

Ask the **temporal knowledge graph** what was true at a given moment. CortexDB stores relation facts with a validity interval (`valid_from`..`valid_to`, plus a `recorded_at` transaction time), so you can query "as of" any instant — the fact whose interval contains that time wins, and superseded/expired facts drop out. The timestamp is RFC3339 and defaults to now; `--from` scopes to a subject and `--type` to a predicate. Run in a shell:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
"$bin" --facts-as-of                              # facts valid right now
"$bin" --facts-as-of 2022-06-01T00:00:00Z         # as of a past date
"$bin" --facts-as-of 2022-06-01T00:00:00Z --from Alice --type works_at
```

Each line prints `subject -predicate-> object  [valid_from .. valid_to]`, where `open` means the fact is still current (no end date). Use this to answer historical questions ("who was CTO in 2021?", "what did Alice work on then?") without the current graph state overwriting the past — supersession keeps each subject's history as a chain of non-overlapping intervals.

$ARGUMENTS
