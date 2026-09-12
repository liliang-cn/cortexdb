# `cortexdb` — an operator CLI

2026-09-12

## Why

A shared brain at `192.168.123.252:47821` was audited by hand. It holds 4456
graph nodes and 8681 edges. Of its 2570 entities, 256 have no edges at all and
1123 have exactly one; 61 are generic tokens the extractor mistook for entities
(`ASCII`, `BT`, `AG`, `HTTP`, `JSON`, `DELETE`, `Fact`). Of 1783 memories, about
70% are verbatim agent tool-call echoes (`[list_dir] {…}`), 346 of them byte
identical to another, and six of the 562 sampled had ever been recalled.

None of that was reachable with the tools shipped today. There is no CLI: the
only entry points are `cortexdb-grpc` (a server) and `cortexdb-mcp-stdio` (an
MCP server for agents). The audit above required hand-writing a JSON-RPC stdio
client, which turned up two defects on the way:

1. `cortexdb-mcp-stdio` closes as soon as stdin reaches EOF, so a batch of
   messages written and then closed gets no responses.
2. `memory_list_all` cannot return the full set. It fails with
   `ResourceExhausted: received message larger than max (5121045 vs 4194304)`.

The second is the real one. `ListAllMemoriesPaged` is named for paging it does
not do: it takes a `Limit` and returns a `Truncated` flag, with no cursor and no
offset. It tells the caller it stopped and gives no way to continue. Worse,
`defaultMemoryListLimit` is 5000 while the gRPC transport gives out around 1100
records — the two limits were never reconciled, so the default is a promise the
transport cannot keep.

The brain has outgrown its own listing tool, and the same wall stops every agent,
not just a human at a terminal.

## What this is

An operator tool: commands named for what a person needs to do to understand and
tidy a brain. Not a shell over the 75-tool MCP surface — agents keep using MCP.

## Shape

A fourth binary, `cmd/cortexdb`, alongside `cortexdb-grpc`,
`cortexdb-mcp-stdio` and `cortexdb-connector-mcp`.

**No CLI framework.** Standard-library `flag` plus a `map[string]command`
dispatch. This repository has twelve direct dependencies and every one earns its
place; pulling in cobra and its transitive tree to parse a dozen subcommands
does not fit. Revisit if the surface passes roughly thirty commands.

### Two backends, one answer

Reached the way `cortexdb-mcp-stdio` already decides, so there is no new concept
to learn:

```
CORTEXDB_REMOTE set  → gRPC client (with CORTEXDB_GRPC_TOKEN)
otherwise            → open the local .db directly (--db, default ~/.cortexdb/cortexdb.db)
```

Commands depend on one `brain` interface (`Stats`, `ListEntities`,
`ListMemories`, `Delete`, …). Two implementations satisfy it: `localBrain` calls
`pkg/cortexdb` directly, `remoteBrain` goes over gRPC. One set of command logic,
two backends, **one shared conformance suite** (see Testing).

## Command surface

```sh
# Seeing
cortexdb stats                      # nodes/edges/entities/memories/docs; entity degree
                                    # distribution; namespace distribution
cortexdb entities --degree 0        # orphans
cortexdb entities --degree '<=1' --noise     # generic-token and hash-shaped heuristics
cortexdb memories --namespace 'superleo-chat-*' --never-recalled --older-than 30d
cortexdb export --jsonl > brain.jsonl        # streams by looping the cursor

# Tidying, in two steps
cortexdb gc --orphans --noise --namespace 'superleo-chat-*' --plan p.json
$EDITOR p.json                      # delete the lines you want to keep
cortexdb gc --apply p.json          # deletes only what survived the edit

# Auditing
cortexdb snapshot --at 2026-09-01T00:00:00Z
cortexdb diff <t1> <t2>
cortexdb vacuum --before 2026-08-01 --dry-run
```

### Flag semantics that would otherwise be read two ways

- `--degree` takes either an exact count (`--degree 0`) or a comparison
  (`--degree '<=1'`, `--degree '>=2'`). Exact is the default reading of a bare
  number.
- `--noise` is a named, versioned heuristic, not a judgement call. It matches an
  entity whose name is (a) in a checked-in stop list of generic programming and
  English tokens, or (b) hash-shaped — `[0-9a-f]{6,}` or `[0-9]{6,}`. The list
  lives in the repository and is reviewable; `--noise` never consults a model.
- `--older-than` takes a Go duration with a `d` extension (`30d`, `12h`).
- `--namespace` matches with shell-style globs (`superleo-chat-*`).
- `--plan` and `--apply` are mutually exclusive. `--apply` takes no selectors:
  the plan file is the selector, which is the point of the two-step shape.

Two rules run through all of it:

- **No selector, no run.** `gc` with none of `--orphans` / `--noise` /
  `--namespace` exits with an error rather than deleting everything that looks
  like junk. This matches gates `pkg/graph` already enforces: it refuses a count
  with no key and a listing with no filter, on the grounds that an unfiltered
  read of a shared store reads other people's records.
- **`--format table|jsonl`**, table by default, `jsonl` for pipes.

## The `gc` plan file

`--plan` writes a reviewable, editable JSON-lines file; `--apply` executes what
is left in it. The two-step shape follows what this codebase already does
elsewhere — `pkg/connector`'s signed plans, and alchemy's review → `Decide`.

```json
{"op":"delete_entity","id":"entity:ascii","reason":"generic-token, degree=1","recoverable":"retraction until vacuum"}
{"op":"delete_memory","id":"harness-1788577644161582000-295","reason":"namespace=superleo-chat-*, recall_count=0, duplicate_of=harness-…-291","recoverable":"NONE","content":"[list_dir] {\"entries\":[…]}"}
```

### Why deleted memories carry their full content

The two deletions in that file are not equally reversible, and the CLI must not
let anyone assume they are:

- **Graph nodes and edges are bitemporal.** Since v2.99.0 a delete is a
  retraction; the row survives and `vacuum_graph` is the only hard delete. An
  entity removed by `gc` can be read back at an earlier instant until vacuumed.
- **Memories are not.** `DeleteMemory` runs `DELETE FROM messages`. There is no
  history table and no retraction. Deleting a memory is final.

So `recoverable` is an explicit field, and every `delete_memory` entry carries
the record's full `content`. **For memories the plan file is the only backup.**

`graph_snapshot` does not fill that role: it counts the graph as it stood at an
instant. It is an auditing read, not a backup, and it rolls nothing back.

## Server-side change: a cursor on the listings

```go
type MemoryListAllRequest struct {
    Limit  int    `json:"limit,omitempty"`
    Cursor string `json:"cursor,omitempty"`   // new
}

type MemoryListAllResponse struct {
    Memories   []MemoryRecord `json:"memories"`
    Truncated  bool           `json:"truncated,omitempty"`
    NextCursor string         `json:"next_cursor,omitempty"`   // new
}
```

`graph_list_all` gets the same treatment. Three constraints:

- **The cursor is opaque**, encoding the last row's sort key (`created_at` plus
  `id`, the id breaking ties within a second). Not an offset: a shared brain is
  written while it is read, and an offset silently skips records when rows are
  inserted behind the cursor.
- **`defaultMemoryListLimit` drops from 5000 to 500.** The current default is a
  trap — it offers 5000 and the transport gives out around 1100. The cursor, not
  a large limit, is how a caller gets everything. This changes what existing
  callers receive by default, in exchange for their being able to reach the rest.
  500 is chosen as roughly half of what the 4 MiB transport was measured to
  carry (~1100 records of the sizes this brain holds), leaving headroom for
  records larger than today's average rather than sitting at the edge.
- **`Truncated == true` implies a non-empty `NextCursor`.** "I stopped and will
  not tell you how to continue" is the present bug; a test pins the invariant.

This fixes the MCP surface at the same time. The wall is not CLI-specific.

## Error handling

| Situation | Behaviour |
|---|---|
| `gc` with no selector | Error, delete nothing |
| `--apply` names an id that no longer exists | Record `skipped`, continue — on a shared brain someone else may have removed it |
| `--apply` fails partway | Report how many were deleted and where it stopped; write the unprocessed remainder to `<plan>.remaining.json` so the run can resume |
| Remote unreachable, or token rejected | Say which of the two it was, and **never fall back to the local database** — silently operating on a different brain is the worst outcome available |
| Local `.db` held by `cortexdb-grpc` | Say the file is in use by the server and to reach it through `CORTEXDB_REMOTE` |

## Testing

1. **`brainconform` — one test body, both backends, same answer required.**
   `localBrain` and `remoteBrain` each run the suite. This is what alchemy's
   `sinkconform` did: it caught five stores handling "two sources assert the same
   node" four different ways, and revealed that the shared suite went through
   `sink.Load` and so bypassed each connector's own `Load` — which is why nobody
   had noticed.
2. **Cursor invariants.** The union of a paged walk equals a single full read;
   `Truncated` always carries `NextCursor`; a walk concurrent with writes neither
   drops nor repeats a record.
3. **Plan-file round trip.** Generate, parse back, execute every `op`; and every
   `delete_memory` entry must carry `content`. That last assertion is what holds
   the "the plan file is the only backup" promise — without it the failure is
   silent data loss.

`--apply` is never tested against the real shared brain. Tests use a temporary
local `.db`.

## Repository conventions this must honour

From `CLAUDE.md`:

- **Imports use the `/v2` module path** — `github.com/liliang-cn/cortexdb/v2/pkg/...`.
- **The cursor change goes in the existing tool-definition files**
  (`knowledge_memory_tooldefs.go`, `graph_list_all.go`), not a parallel path.
- **CI's gates are `go build ./...` and `go test -race ./...`** — there is no
  separate lint step, so the conformance suite has to carry the weight.
- **A new binary is a non-trivial architectural change**, so `README.md`,
  `README_CN.md` and `SKILL.md` are all updated in the same change. They are
  kept in sync deliberately.
- `tool_mutates_test.go` requires every tool to declare whether it writes, and
  pins `toolCount`. The cursor change adds no tool, so `toolCount` stays 75 —
  if that assertion moves, something unintended was added.

## Out of scope

- No interactive TUI.
- No `cortexdb tool <name>` generic shell over the 75 MCP tools.
- No scheduled or daemonised cleanup. `gc` is something a person starts.
- No change to `DeleteMemory`'s hard-delete semantics. Giving memories the
  bitemporal treatment the graph has is its own project, not a CLI feature.

## Known defects this does not fix

- `cortexdb-mcp-stdio` closing on stdin EOF before answering queued requests.
- `harness-rs-serve` failing `cargo clippy --all-features` on a non-exhaustive
  `ChatChunk::Error` match — a different repository, noted here because it was
  found during the same audit.
