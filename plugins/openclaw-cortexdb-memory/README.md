# CortexDB Memory for OpenClaw

Native OpenClaw `memory` capability backed by the CortexDB gRPC sidecar. It
registers graph-aware recall, durable memory store/delete tools, prompt guidance,
and the generic OpenClaw memory search runtime.

Install the public release:

```bash
openclaw plugins install npm:cortexdb-openclaw-memory@2.57.0
openclaw config set plugins.slots.memory cortexdb-memory
openclaw gateway restart
openclaw plugins inspect cortexdb-memory --runtime --json
```

GitHub fallback:

```bash
openclaw plugins install git:github.com/liliang-cn/openclaw-cortexdb-memory@v2.57.0
```

Run the sidecar first:

```bash
CORTEXDB_PATH="$HOME/.cortexdb/cortexdb.db" cortexdb-grpc
```

Configure `plugins.entries.cortexdb-memory.config` with `endpoint`, `token`,
`userId`, `scope`, or `namespace`, or use the matching `CORTEXDB_*` environment
variables. Selecting this plugin replaces OpenClaw's built-in `memory-core`
slot; do not select both.
