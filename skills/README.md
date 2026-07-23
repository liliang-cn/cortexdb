# CortexDB Agent Integrations

Native memory plugins and [agentskills.io](https://agentskills.io)-format skills
wire CortexDB into OpenClaw and Hermes Agent through the gRPC sidecar.

| Integration | Target | Behavior | Location |
|---|---|---|---|
| Native memory plugin | OpenClaw | Exclusive `memory` capability, prompt guidance, search/store/delete tools | [`liliang-cn/openclaw-cortexdb-memory`](https://github.com/liliang-cn/openclaw-cortexdb-memory) |
| Native memory provider | Hermes Agent | Automatic prefetch, turn sync, explicit memory tools | [`liliang-cn/hermes-cortexdb-memory`](https://github.com/liliang-cn/hermes-cortexdb-memory) |
| Agent skill | OpenClaw | Instructions and reusable Node helpers | [`cortexdb-memory-openclaw`](cortexdb-memory-openclaw/) |
| Agent skill | Hermes Agent | Instructions and reusable Python helpers | [`cortexdb-memory-hermes`](cortexdb-memory-hermes/) |

Use a native plugin when CortexDB should participate automatically in the agent's
memory lifecycle. Use a skill when explicit tools and integration instructions are
enough. Both paths share `knowledge_memory_recall`, one SQLite file, lexical mode
without an API key, and the RDF/SPARQL knowledge graph.
