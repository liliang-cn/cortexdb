# 11 · Unified Brain — two live databases, streaming CDC, and reasoning

A complex, multi-source CortexDB application. It merges **two live databases**
into one privacy-safe knowledge graph, keeps both continuously in sync, and
reasons over the result — with **no LLM and no embedder** (deterministic lexical
retrieval + SPARQL + RDFS/SHACL).

```
Postgres (customers, orders)  ─┐  connector desensitize ┐
MySQL    (tickets, free-text)  ┘  (signed plan)          ├─▶ one CortexDB brain
                                                          │
  continuous CDC:  PG logical replication + MySQL binlog ─┘  (both stream concurrently)
                                                          │
  reasoning:  RDFS inference · SPARQL aggregates · SHACL validation
```

## What it demonstrates

- **Dual source → one graph.** Customers/orders (Postgres) and support tickets
  (MySQL) become one knowledge graph: `Customer ──placed──▶ Order` and
  `Customer ──raised──▶ Ticket`.
- **Free-text PII redaction.** Ticket bodies contain phone numbers and emails in
  prose; the connector redacts them in place (`[REDACTED:phone]`,
  `[REDACTED:email]`) before indexing — including on the live CDC path.
- **Concurrent streaming CDC.** A Postgres logical-replication watcher and a
  MySQL binlog watcher run at the same time; a live `INSERT` in either database
  lands in the brain within milliseconds.
- **RDFS inference.** `Customer rdfs:subClassOf Party` materializes a derived
  `rdf:type Party` for every customer.
- **SPARQL aggregate.** "Priority customers" = `COUNT(paid orders) >= 2` via
  `GROUP BY … HAVING`.
- **SHACL validation.** A shape requires every `Customer` to have a `city`; the
  demo seeds one customer without a city and the validator catches it.

## Run it

```bash
docker run --rm -d --name cx_pg -e POSTGRES_PASSWORD=p -p 5432:5432 postgres:16 -c wal_level=logical
docker run --rm -d --name cx_my -e MYSQL_ROOT_PASSWORD=p -e MYSQL_DATABASE=test -p 3306:3306 mysql:8

go run ./examples/11_unified_brain \
  -pg 'postgres://postgres:p@localhost:5432/postgres?sslmode=disable' \
  -my 'root:p@tcp(localhost:3306)/test'
```

The program seeds both databases (including a publication for Postgres logical
replication), so a fresh pair of containers is all you need.

## Prerequisites

- Postgres with `wal_level=logical` (the `-c wal_level=logical` flag above).
- MySQL with ROW binlog + `binlog_row_image=FULL` (defaults in `mysql:8`).

No LLM, embedder, or API key — the whole pipeline is deterministic.
