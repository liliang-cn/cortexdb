# 12 · Incident-Analysis Agent — requires an LLM

A complex CortexDB example that **requires an LLM** (a chat/generation model — no
embedding model is used). The LLM does two jobs:

1. **Knowledge extraction (GraphFlow).** It reads unstructured incident reports
   and extracts a knowledge graph — services, errors, mitigations, and their
   causal relations — which CortexDB stores as RDF.
2. **Agentic tool use.** An LLM agent answers an analytical question by deciding,
   step by step, which CortexDB tool to call, reading the results, and
   synthesizing a grounded answer with citations.

```
unstructured incident reports
   │  LLM extraction (GraphFlow)         ← requires LLM
   ▼
CortexDB: knowledge graph + lexical RAG index
   ▲
   │  agent loop (LLM picks tools)       ← requires LLM
   │    • search_docs(query)   → lexical full-text search over the reports
   │    • top_entities()       → most-connected entities (GraphFlow centrality)
   │    • final(answer)        → grounded answer citing INC- ids
```

Retrieval is lexical and graph centrality is deterministic — the **only** model
required is the chat LLM. The agent loop uses a simple JSON action protocol (one
JSON object per turn) rather than native function-calling, so it works against
any OpenAI-compatible endpoint.

## Run it

```bash
OPENAI_API_KEY=sk-... OPENAI_BASE_URL=https://your-endpoint/v1 \
  go run ./examples/12_incident_agent -model gpt-5.5
```

Without `OPENAI_API_KEY` the program exits — unlike examples 10 and 11, the LLM
is mandatory here.

## What to look at

- The extraction line per report (entities/relations) — the model turning prose
  into graph structure.
- The **agent trace** — the model choosing `top_entities` to find the systemic
  cause, then `search_docs` to gather mitigations, then `final`.
- The final answer cites incident IDs and is grounded only in tool results.

Example trace:

```
[1] top_entities()
[2] search_docs("db-primary connection pool exhaustion mitigations")
[3] final
→ The systemic root cause is db-primary connection pool exhaustion (INC-101);
  mitigations were increasing the pool size and adding a circuit breaker.
```
