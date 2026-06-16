# 13 · Scale + Analytics — larger volume, end to end

A comprehensive, **larger-volume** CortexDB example. It bulk-loads tens of
thousands of rows from two live databases through the privacy connector into one
knowledge graph, reports ingest throughput, runs analytics at scale, and keeps
the brain in sync under a concurrent write load — all deterministic (no LLM, no
embedder).

```
Postgres (customers, orders)  ─┐ connector desensitize ┐
MySQL    (tickets, free-text)  ┘                        ├─▶ one CortexDB brain
                                                         │
  timed ingest → throughput report                       │
  analytics:  GROUP BY · HAVING subquery · RDFS inference · SHACL
  CDC under load:  N live order updates via PG logical replication
```

## What it demonstrates

- **Throughput at volume.** Times the desensitized import and prints
  rows/s · chunks/s · triples/s (e.g. ~8,300 rows → ~28k KG triples).
- **Analytics over a large graph.** Orders by status (`GROUP BY`), priority
  customers via a `HAVING` subquery, RDFS-lite inference materialization, and
  SHACL validation that catches the seeded missing-city customers.
- **Free-text PII redaction at scale.** Every ticket body has an embedded phone
  and email; all are redacted before indexing.
- **CDC under concurrent write load.** Fires N live `UPDATE`s at Postgres and a
  logical-replication watcher streams them into the brain while the app keeps
  querying — reporting how quickly the brain catches up.

## Run it

```bash
docker run --rm -d --name cx_pg -e POSTGRES_PASSWORD=p -p 5432:5432 postgres:16 -c wal_level=logical
docker run --rm -d --name cx_my -e MYSQL_ROOT_PASSWORD=p -e MYSQL_DATABASE=test -p 3306:3306 mysql:8

go run ./examples/13_scale_analytics \
  -pg 'postgres://postgres:p@localhost:5432/postgres?sslmode=disable' \
  -my 'root:p@tcp(localhost:3306)/test'
```

Volume is configurable — defaults run in ~1–2 minutes:

```
-customers 1500   -orders 6000   -tickets 800   -load 50
```

Ingest cost scales roughly with the printed throughput, so e.g. 50,000 orders is
a few minutes. Crank the flags to stress it harder.

## Notes

- Requires Postgres `wal_level=logical` (for the CDC phase) and MySQL ROW binlog
  defaults; the program seeds both, including the publication.
- The CDC apply path upserts one row per change event, so it favors steady change
  streams over giant bursts; the throughput line and the "caught up" timing make
  the real behavior visible rather than hidden.
