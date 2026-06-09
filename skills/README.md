# CortexDB Agent Skills

Two [agentskills.io](https://agentskills.io)-format skills that wire CortexDB in as the memory + knowledge-graph layer for an AI agent, via the gRPC sidecar and the `cortexdb-client` clients.

| Skill | Target agent | Client | Install |
|---|---|---|---|
| [`cortexdb-memory-hermes`](cortexdb-memory-hermes/) | Hermes Agent (Python) | `pip install cortexdb-client` | `hermes skills install <url> --name cortexdb-memory-hermes` |
| [`cortexdb-memory-openclaw`](cortexdb-memory-openclaw/) | OpenClaw (Node.js) | `npm install cortexdb-client` | `openclaw skills install ./skills/cortexdb-memory-openclaw` |

Both follow the same shape: run the `cortexdb-grpc` sidecar (one binary, one SQLite file, zero-key lexical mode by default), then `remember`/`recall` plus a real RDF/SPARQL knowledge graph. Each skill bundles a ready-to-use helper module under `scripts/` (verified end-to-end against a live sidecar).
