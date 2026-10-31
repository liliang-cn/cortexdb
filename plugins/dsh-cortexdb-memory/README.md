# CortexDB Memory for DeepSeek Harness

A native DeepSeek Harness (dsh) plugin backed by the CortexDB gRPC sidecar:

- **Recall.** Before each prompt a person types, CortexDB is asked about it and
  the matching memories and knowledge are attached for the model — the same
  question, snippets and wording as the Claude Code and Codex recall hook.
- **Capture.** When a session has been quiet for `captureIdleMinutes`, or ends,
  its durable facts are distilled into memories (`auto:dsh-<session>:<slug>`),
  and older memories they make untrue are marked superseded.
- **Tools.** `cortexdb_memory_search`, `cortexdb_memory_store` and
  `cortexdb_memory_forget` for the model to use on purpose.

Recall reads the whole brain unless `scope`, `namespace` or `userId` narrow it,
so dsh sees what Claude Code, Codex and other agents saved there, and they see
what dsh saved.

## Install

```bash
dsh plugin --profile web add "github:liliang-cn/cortexdb#semver:^2&path:/plugins/dsh-cortexdb-memory"
```

It installs from this repository's release tags: `semver:^2` takes the newest
v2 release, and the profile's lockfile pins the exact commit. Pin one release
with `#v2.142.0&path:/plugins/dsh-cortexdb-memory` instead; move to the newest
with `dsh plugin --profile web update cortexdb-dsh-memory`.

Any profile works (`web`, `tui`, `headless`, your own). Installing selects the
plugin's bundle; restart the profile, or let HMR pick it up.

The plugin has no runtime dependencies: it speaks gRPC over `node:http2`, so
pnpm has no build script to approve.

## The brain

By default the plugin connects to `127.0.0.1:47821`. If nothing answers there,
it downloads the `cortexdb-grpc` release matching this package's version,
checks its SHA-256, and starts it against `~/.cortexdb/cortexdb.db`; it stops
it again when the profile shuts down.

For a shared brain, or a sidecar you run yourself, set `endpoint` (or
`CORTEXDB_REMOTE`). The plugin never starts a local sidecar in place of a shared
one that is down.

Configure it from the profile's `cordis.patch.yml`
(`~/.dsh/profiles/<profile>/cordis.patch.yml`):

```yaml
- id: cortexdb-memory
  config:
    endpoint: 192.168.1.10:47821
    token: ...              # or CORTEXDB_GRPC_TOKEN
```

## Capture

Distilling a session needs a model; without one, capture stays off and says so
once in the log. Either export the variables `cortexdb-mcp` reads:

```bash
export CORTEXDB_LLM_BASE_URL=https://api.example.com/v1   # OpenAI-compatible, or an Ollama host
export CORTEXDB_LLM_MODEL=...
export CORTEXDB_LLM_API_KEY=...
```

or name them in the config:

```yaml
- id: cortexdb-memory
  config:
    captureLlmBaseUrl: https://api.deepseek.com/v1
    captureLlmModel: deepseek-v4-flash
    captureLlmApiKeyEnv: DEEPSEEK_API_KEY
```

A session is captured once it has three turns a person typed, again only when
it has more, and one capture at a time. The capture runs `cortexdb-mcp
--capture-session` (downloaded and verified like the sidecar, or
`mcpBinaryPath`) as a detached process, so it finishes after a one-shot
`dsh headless` run has exited. Its log is `~/.cortexdb/dsh/capture/capture.log`.

Sessions from before the plugin was installed can be imported with the
`import_agent_memory` tool (`sources: ["dsh_sessions"]`) or
`cortexdb-mcp --import-agent-memory --sessions`; both read dsh's own logs under
`$DSH_HOME` (default `~/.dsh`).

## Configuration

| Field | Default | Meaning |
| --- | --- | --- |
| `endpoint` | `CORTEXDB_REMOTE`, else `127.0.0.1:47821` | Sidecar, `host:port`, or an `https://` URL. |
| `token` | `CORTEXDB_GRPC_TOKEN` | Bearer token, if the sidecar requires one. |
| `autoStart` | `true` | Start a sidecar on the local default endpoint when none answers. |
| `dbPath` | `CORTEXDB_PATH`, else `~/.cortexdb/cortexdb.db` | Database a started sidecar opens. |
| `binaryPath` / `mcpBinaryPath` | release download | `cortexdb-grpc` / `cortexdb-mcp` to use instead. |
| `dataDir` | `~/.cortexdb/dsh` | Downloaded binaries and capture exports. |
| `userId`, `scope`, `namespace` | empty | Narrow recall; `scope`/`namespace` also apply to stored memories (default scope `global`). |
| `autoRecall` | `true` | Recall before each typed prompt. |
| `recallTopK` | `3` | Memories and knowledge hits per automatic recall. |
| `recallTimeoutMs` | `4000` | A slower automatic recall is skipped, never waited on. |
| `callTimeoutMs` | `30000` | Timeout for the memory tools. |
| `autoCapture` | `true` | Distil sessions into memories (needs a model). |
| `captureIdleMinutes` | `10` | Capture this long after a session's last turn; `0` captures only at the end. |
| `captureLlmBaseUrl`, `captureLlmModel`, `captureLlmApiKeyEnv` | empty | The capture's model, instead of `CORTEXDB_LLM_*`. |

Do not also add CortexDB's MCP server to the same profile unless duplicate
tools are intended.
