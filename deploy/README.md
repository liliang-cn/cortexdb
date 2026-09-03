# Running CortexDB as a service

CortexDB is a library first, and most of the time it is one: your process opens
`~/.cortexdb/cortexdb.db` and that is the whole deployment. But `cortexdb-grpc`
turns the same facade into a long-running server, and that is what you want as
soon as more than one machine shares a brain — a SQLite file on a network mount
is a corruption hazard, not a shared database. Exactly one host runs the server
over the real file; everything else talks to it.

This directory holds what that host needs:

| File | What it is |
| --- | --- |
| [`systemd/cortexdb-grpc.service`](systemd/cortexdb-grpc.service) | hardened unit for a bare-metal or VM install |
| [`systemd/cortexdb-grpc.env.example`](systemd/cortexdb-grpc.env.example) | the unit's configuration, token included |
| [`docker/Dockerfile`](docker/Dockerfile) | static image with a built-in healthcheck |
| [`docker/docker-compose.yml`](docker/docker-compose.yml) | the same, as one service you can `up -d` |
| [`docker/.env.example`](docker/.env.example) | every override the compose file reads |
| [`agents-demo/`](agents-demo/) | a different thing: CortexDB plus two agents, for trying it out |

## Ports

Every port has a default and every default can be overridden. Nothing here
binds a common port, on purpose.

| Service | Default | Override with | Reachable from |
| --- | --- | --- | --- |
| `cortexdb-grpc` | `127.0.0.1:47821` | `CORTEXDB_GRPC_ADDR`, or `-addr` | whatever address you bind |
| live graph view | `127.0.0.1:37423` | `CORTEXDB_LIVE_PORT` | loopback only, by design |

Precedence is **flag > environment > default**, so `-addr` wins over
`CORTEXDB_GRPC_ADDR`. Prefer the environment variable in a deployment: the
health probe reads the same variable, so the check follows the port without a
second place to edit. If the port is taken, the server fails to start and says
so — it does not silently pick another one.

## systemd

```bash
go build -trimpath -ldflags="-s -w" -o cortexdb-grpc ./cmd/cortexdb-grpc
sudo install -m 0755 cortexdb-grpc /usr/local/bin/

sudo useradd --system --home-dir /var/lib/cortexdb --shell /usr/sbin/nologin cortexdb
sudo install -d -m 0750 /etc/cortexdb
sudo install -m 0640 -o root -g cortexdb \
  deploy/systemd/cortexdb-grpc.env.example /etc/cortexdb/cortexdb-grpc.env
sudo sed -i "s/^CORTEXDB_GRPC_TOKEN=.*/CORTEXDB_GRPC_TOKEN=$(openssl rand -hex 32)/" \
  /etc/cortexdb/cortexdb-grpc.env

sudo install -m 0644 deploy/systemd/cortexdb-grpc.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now cortexdb-grpc
```

Then check it:

```bash
systemctl status cortexdb-grpc
sudo bash -c 'set -a; . /etc/cortexdb/cortexdb-grpc.env; exec cortexdb-grpc -health'
```

The `sudo` is not decoration: the environment file is `0640 root:cortexdb`
precisely so that an ordinary account cannot read the token out of it, which
means an ordinary account cannot source it either.

Notes that save an hour:

- **The token lives in the environment file, never on the command line.** A
  running process's arguments are readable by every user on the box.
- **`StateDirectory=cortexdb` is the only writable path.** The unit sets
  `ProtectSystem=strict`, so a `CORTEXDB_PATH` outside `/var/lib/cortexdb`
  needs a matching `ReadWritePaths=` line or the server cannot open its database.
- **Changing the port is an edit to the env file plus `systemctl restart`** —
  the unit does not name a port anywhere.
- To serve other machines, set `CORTEXDB_GRPC_ADDR` to this host's LAN or
  Tailscale address. The transport is plaintext, so that decision and setting a
  token are the same decision.

## Docker

```bash
cp deploy/docker/.env.example deploy/docker/.env
sed -i '' "s/^CORTEXDB_GRPC_TOKEN=.*/CORTEXDB_GRPC_TOKEN=$(openssl rand -hex 32)/" deploy/docker/.env
docker compose -f deploy/docker/docker-compose.yml up -d --build
docker compose -f deploy/docker/docker-compose.yml ps       # health shows here
```

Or without compose:

```bash
docker build -f deploy/docker/Dockerfile -t cortexdb-grpc:local .
docker run -d --name cortexdb \
  -v cortexdb-data:/data \
  -e CORTEXDB_GRPC_TOKEN=$(openssl rand -hex 32) \
  -e CORTEXDB_GRPC_ADDR=0.0.0.0:43510 \
  -p 127.0.0.1:43510:43510 \
  cortexdb-grpc:local
```

The database lives on the `cortexdb-data` volume. Bind-mount a host directory
instead if you would rather back it up with the rest of the machine — the image
runs as uid 10001, so `chown 10001` that directory first.

`CORTEXDB_GRPC_PORT` in `.env` moves the listener, the published port and the
healthcheck at once. `CORTEXDB_BIND_HOST` decides who can reach it, and stays
on `127.0.0.1` until you say otherwise.

## What the service does and does not carry

Worth knowing before you plan around it, because none of it fails loudly:

- **The server's only external dependency is the embeddings endpoint.**
  `cortexdb-grpc` reads `OPENAI_BASE_URL` and friends and nothing else; there is
  no LLM in it. Set the embedder and vector search turns on for every client at
  once — clients configure neither.
- **Without an embedder the service still works**, in lexical mode. That is a
  supported path, not a broken one, and the startup log and `-health` both say
  which mode you are in (`embedder=on` / `embedder=none`).
- **The LLM-backed one-shot modes are not part of the service.**
  `cortexdb-mcp-stdio --multi-hop`, `--global-search`, `--graph-update`,
  `--resolve-entities` and the opt-in `CORTEXDB_QUERY_REWRITE` all open a local
  SQLite file at `CORTEXDB_PATH` and **ignore `CORTEXDB_REMOTE`**. Point a client
  at a shared brain and run `--multi-hop` on it and you get an answer computed
  over an empty local database, with no error. The MCP *tools* are all proxied
  and complete — 67 of them, the same set in both modes — this applies only to
  those command-line modes.
- **The live 3D view runs where the MCP server runs**, not on the brain's host.
  It reads the shared brain remotely (the page names its source), binds loopback
  only by design, and its page pulls the 3d-force-graph library from a CDN — so
  the machine with the *browser* needs internet, and reaching a headless server's
  view means an SSH tunnel: `ssh -L 37423:127.0.0.1:37423 host`.
- **A remote embedder needs egress.** On a server that reaches the internet
  through a proxy, give the unit the proxy environment (a systemd drop-in
  covering `HTTPS_PROXY` is the usual shape) or the embedder silently never
  answers.

## Health

The server binary is its own probe:

```bash
cortexdb-grpc -health
# ok 127.0.0.1:47821 v2.90.0 db=/var/lib/cortexdb/cortexdb.db embedder=none
```

It opens no database — it dials a server that is already running, reading
`CORTEXDB_GRPC_ADDR` and `CORTEXDB_GRPC_TOKEN` from the same environment the
server uses, and exits non-zero with the reason when the answer is not healthy.
A wildcard listen address is probed on loopback, so `0.0.0.0:47821` works
without a second variable. That is what the image's `HEALTHCHECK` runs, which is
why the image needs no probe helper installed beside the binary.

## Pointing clients at it

Nothing on the client side changes except two variables:

```bash
export CORTEXDB_REMOTE="10.0.0.5:47821"
export CORTEXDB_GRPC_TOKEN="<the same token>"
```

The MCP server then opens no local database: it discovers the tool surface from
the server at startup and proxies every call. Embedder and model settings live
on the server, not on the clients. Typed clients for other languages:
`cargo add cortexdb-client` · `pip install cortexdb-client` ·
`npm install cortexdb-client`.

## Backup and upgrade

The brain is one SQLite file (plus `-wal` and `-shm` while running). Copying
just the `.db` of a live server gives you a torn backup. Ask the server for one
instead — it is the process that owns the file, and it does not have to stop:

```bash
grpcurl -H "authorization: Bearer $CORTEXDB_GRPC_TOKEN" \
  -d '{"path": "cortexdb-'"$(date +%F)"'.db"}' \
  10.0.0.5:47821 cortexdb.v1.AdminService/Backup
# {"path": "/var/lib/cortexdb/cortexdb-2026-09-03.db", "sizeBytes": "4227072"}
```

`path` is **relative to the server's backup directory**, which defaults to the
directory holding the database — under `ProtectSystem=strict` that is the only
place the unit can write anyway. Absolute paths and anything climbing out with
`..` are refused: the token that reads and writes rows should not also decide
where on the host a full copy of the brain lands. Subdirectories (`daily/mon.db`)
are created as needed, at mode 0700, and an existing destination is refused
rather than overwritten, so a schedule that reuses a name fails loudly instead
of eating the previous run. The same RPC is on the typed clients
(`AdminService.Backup`) if you would rather not install `grpcurl`.

The copy is one self-contained file: `VACUUM INTO` runs in a read transaction,
so everything committed to the `-wal` at that instant is folded in and nothing
committed after it is. Restoring is copying that file into place — there is no
`-wal` to remember.

If the server is not running, or you would rather not go through it, the
`sqlite3` CLI does the same thing from outside:

```bash
sudo apt-get install -y sqlite3   # not installed by default on a server image
sudo -u cortexdb sqlite3 /var/lib/cortexdb/cortexdb.db \
  ".backup '/var/backups/cortexdb-$(date +%F).db'"
```

And with neither: `systemctl stop cortexdb-grpc`, copy the `.db`, `-wal` and
`-shm` together, then start it again — those three files are one backup, and
taking only the first is the mistake that looks fine until you restore it.

A brain on PostgreSQL answers the RPC with `UNIMPLEMENTED` naming its backend,
because its backups are `pg_dump` and whatever retention the database team
already runs, not a file this process writes beside a database it does not own.

Upgrading is: build the new binary, replace it, restart. The schema migrates
forward on open. `TimeoutStopSec=30s` gives the server room to checkpoint the
WAL on the way down, so a restart is clean rather than merely recoverable.

## What of this was actually tried

The unit and the image are not sketches. On Ubuntu 24.04 with systemd 255, a
clean install of exactly the commands above came up, and then: the database
landed in `/var/lib/cortexdb` readable only by its own user; `ProtectSystem=strict`
refused a write to `/etc` while the state directory stayed writable;
`systemd-analyze security` scored the unit 1.5 (OK) with nothing outstanding;
`kill -9` on the main PID was followed by a new one seconds later; a memory
written through the gRPC API came back after `systemctl restart`; a wrong token
was refused; and moving `CORTEXDB_GRPC_ADDR` to another port took the server and
its health probe with it. The container was checked the same way — built,
started on an overridden port, and reported `healthy` by Docker's own
healthcheck. Boot persistence is `systemctl is-enabled` saying `enabled`; a real
reboot was not part of the check.

The embedder was checked against a real endpoint rather than a stub: with
`OPENAI_BASE_URL` pointed at an OpenAI-compatible gateway serving
`embeddinggemma` at 768 dimensions, the server logged the model on startup,
`-health` reported `embedder=on`, and a memory saved through the API came back
for a query sharing no words with it — the response naming the reason, *semantic
vector search because an embedder is available*. The live 3D view was opened
against that same service over gRPC and served its page with the brain's four
nodes and five edges on it.

## When it does not come up

- `connection refused` from a client — check what the server actually bound:
  `systemctl status cortexdb-grpc` prints the listen address on startup. A
  server on `127.0.0.1` is invisible to other machines by design.
- `Unauthenticated: invalid token` — the client's `CORTEXDB_GRPC_TOKEN` differs
  from the server's. The probe reports this too, rather than "unhealthy".
- `database is locked` — two processes are opening the same file, and one of
  them should be a client instead. One host, one server, one file.
- Container is `unhealthy` but the logs look fine — the healthcheck is dialing
  the port in `CORTEXDB_GRPC_ADDR`. If you moved the port with `-addr` on the
  command line instead of the environment, the probe never learned about it.
