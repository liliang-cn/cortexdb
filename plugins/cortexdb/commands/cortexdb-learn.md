---
description: Turn study material (physics, chemistry, math, a language, …) into a prerequisite knowledge graph, and get an ordered study plan
---

Build and query a **learning knowledge graph**: study material as concepts linked by *prerequisite* edges, so the graph can answer what retrieval cannot — **"what must I learn, and in what order, before I can understand X?"**, "what am I ready to study now?", and "why am I stuck on this?".

**You are the extractor**, so this works for any subject and any language of material (physics, chemistry, math, foreign-language grammar/vocabulary, …).

Locate the binary once:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
```

Decide what the user wants from `$ARGUMENTS`:

### A. Ingest study material (a file, a topic, notes, a syllabus)
Read the material (or draw on your own knowledge of the subject if the user just names a topic). Extract:
- **concepts** — `name`, `type` (`concept`/`definition`/`law`/`formula`/`theorem`/`proof`/`method`/`constant`/`unit`/`element`/`compound`/`reaction`/`experiment`/`vocabulary`/`grammar`/`phrase`/`topic`), optional `summary` and `difficulty` (1–5)
- **relations** — most importantly `requires` (**A requires B** = B is a prerequisite of A, study B first); also `part_of`, `example_of`, `applies`

Keep prerequisite edges **acyclic** and genuinely pedagogical (only "you truly cannot understand A without B"). Write JSON to a temp file and import:

```json
{"subject":"physics",
 "concepts":[{"name":"加速度","type":"concept","difficulty":2}],
 "relations":[{"from":"牛顿第二定律","to":"加速度","type":"requires"}]}
```

```bash
"$bin" --import-learning-graph /tmp/learning.json
```

### B. Get a study plan
```bash
"$bin" --learn-path "<concept>"      # ordered prerequisites, mastered ones removed
"$bin" --learn-next                  # what is ready to study right now
"$bin" --learn-mastered <concept>…   # record what the user already knows
```

Then report the plan in the user's language, explaining *why* the order is what it is (which concept unlocks which). If a concept is missing from the graph, offer to ingest material for it (step A). Re-running is safe — imports update in place.

$ARGUMENTS
