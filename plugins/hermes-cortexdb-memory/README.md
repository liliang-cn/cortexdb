# CortexDB Memory for Hermes Agent

Native Hermes `MemoryProvider` backed by the CortexDB gRPC sidecar. It adds
automatic pre-turn recall, completed-turn capture, explicit memory tools, and
graph-aware context from `knowledge_memory_recall`.

Install this directory as `$HERMES_HOME/plugins/cortexdb` (normally
`~/.hermes/plugins/cortexdb`) and configure:

```yaml
memory:
  provider: cortexdb
```

Run the sidecar before starting Hermes:

```bash
CORTEXDB_PATH="$HOME/.cortexdb/cortexdb.db" cortexdb-grpc
```

Optional environment variables: `CORTEXDB_GRPC_ENDPOINT`,
`CORTEXDB_GRPC_TOKEN`, `CORTEXDB_USER_ID`, `CORTEXDB_MEMORY_SCOPE`,
`CORTEXDB_MEMORY_NAMESPACE`, `CORTEXDB_MEMORY_TOP_K`,
`CORTEXDB_MAX_CONTEXT_CHARS`, and `CORTEXDB_AUTO_CAPTURE`.

Do not also configure CortexDB as an MCP memory workflow unless duplicate tools
and duplicate writes are intentional.
