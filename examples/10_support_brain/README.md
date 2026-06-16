# 10 · Support Agent Brain — end-to-end over a real DB

A realistic CortexDB application: a customer-support "agent brain" built on top of
a **live Postgres or MySQL** database, end to end.

```
live DB (customers/orders, with PII)
   │  connector: introspect → human-signed MaskingPlan → desensitize
   │  importflow: → RAG + knowledge graph
   ▼
CortexDB (one file)
   ├─ masked Q&A          "which customers are VIP?"        (lexical RAG, no LLM)
   ├─ relationships       "what did customer 1 order?"      (SPARQL over the KG)
   ├─ audited un-mask     pseudonym token → real name       (tenant vault, authorized action)
   └─ live CDC sync       UPDATE in the DB → brain updates  (polling change source + Watcher)
```

Real phone numbers and customer names never enter the knowledge base: phones are
masked (`138****1234`), names are pseudonymized to deterministic vault tokens, and
the name is recoverable only through `connector.Unmask` (simulating an authorized
operational action such as sending a notification).

No LLM or embedder is required — retrieval runs in CortexDB's lexical mode and
relationships are answered with SPARQL, so the run is fully deterministic.

Optionally add a natural-language answer step with `-llm`: the model receives
**only the desensitized context** (tokens + masked phones), so it phrases a
support reply while real PII never reaches the LLM. Configure via env
`OPENAI_API_KEY` and `OPENAI_BASE_URL` (any OpenAI-compatible endpoint) and
`-model <id>`.

## Run it

Start a throwaway database, then point the demo at it (`-seed` creates and
populates the `customers`/`orders` tables):

```bash
# Postgres
docker run --rm -d --name cx_pg -e POSTGRES_PASSWORD=p -p 5432:5432 postgres:16
go run ./examples/10_support_brain -driver postgres \
  -dsn 'postgres://postgres:p@localhost:5432/postgres?sslmode=disable'

# MySQL
docker run --rm -d --name cx_my -e MYSQL_ROOT_PASSWORD=p -e MYSQL_DATABASE=test -p 3306:3306 mysql:8
go run ./examples/10_support_brain -driver mysql \
  -dsn 'root:p@tcp(localhost:3306)/test'
```

Flags: `-driver postgres|mysql`, `-dsn <connection string>`, `-seed=false` if the
tables already exist, `-llm` to add an LLM answer step, `-model <id>` (default
`gpt-5.5`).

```bash
# with an LLM phrasing the answer over masked context
OPENAI_API_KEY=sk-... OPENAI_BASE_URL=https://your-endpoint/v1 \
  go run ./examples/10_support_brain -driver postgres -dsn '...' -llm
```

## What to look at

- The printed **MaskingPlan** — the connector auto-classifies `phone`/`email`/`name`
  as PII; the demo then simulates the human review (keep business columns, confirm
  PII handling) before signing.
- The **VIP answer** shows `tok_…` where the name was — proof the RAG content is
  desensitized.
- The **un-mask** line resolves that token back to the real name via the vault —
  the only reverse path, and it never touches the retrieval path.
- For continuous sync instead of one poll cycle, swap the polling source for
  `connector.NewPostgresCDCSource` / `connector.NewMySQLBinlogSource` (true CDC,
  captures hard deletes) and run the `Watcher` in a goroutine.
