# 18 — vectors, a knowledge graph, and an ontology over one estate

The other examples show the three separately. This one puts all three over a
single body of facts and spends most of its length on the **seams**, because
that is where combining them earns anything.

```bash
# lexical: runs anywhere, no model needed
go run ./examples/18_vector_graph_ontology

# with real embeddings
OPENAI_API_KEY=... OPENAI_BASE_URL=... EMBED_MODEL=... EMBED_DIM=... \
  go run ./examples/18_vector_graph_ontology
```

Run it both ways and compare section 5: the example names the document that
actually answers the question and prints where each mode ranked it. On this
eight-document corpus a small local embedding model does not reliably beat
keyword overlap — which is the sort of thing an example should let you measure
rather than assert.

Compare the ranks, not the scores. The score is reciprocal rank fusion with
k=60, so the top three are always 1/61, 1/62 and 1/63 — they round to the same
number and carry no information about either retriever's confidence.

Any OpenAI-compatible `/embeddings` endpoint works, including a local Ollama:

```bash
OPENAI_API_KEY=ollama OPENAI_BASE_URL=http://localhost:11434/v1 \
  EMBED_MODEL=embeddinggemma EMBED_DIM=768 \
  go run ./examples/18_vector_graph_ontology
```

## The estate

A replicated block-storage cluster — DRBD resources on LINSTOR nodes — chosen
because it has a real invariant and real prose about the same objects:

```
rack-b                          rack-c
  dell   pool0  900 GiB free      openclaw  pool0  1800 GiB free
  hp     pool0 3200 GiB free      sds-a     pool0   120 GiB free   ← over-committed

  sds-meta      primary dell   replicas dell hp openclaw   quorum on
  vm-store      primary hp     replicas hp dell            quorum on
  backup-vault  no primary     replicas sds-a openclaw     quorum off
```

Eight runbooks and incident notes describe the same objects in the vocabulary
of the tool rather than the vocabulary of whoever will search for them later.

## The three questions

| question | answered by | the other two |
|---|---|---|
| how do I recover when the replicas disagree? | the text | no property holds a procedure |
| which resources sit on a nearly full pool? | the structure | no runbook lists what is replicated where |
| may `hp` become primary for `sds-meta` as well? | the ontology | both would happily store it |

The third is the one worth reading the schema for. `resourcePrimary` and
`resourceReplica` are the same shape and differ in one word — the cardinality
of one side — and that word is what refuses the two-writer state at the write,
rather than leaving it to a reviewer.

## The seams

1. **text → structure.** A passage found by meaning; its chunks are graph
   nodes; the mention edges run from them to the estate objects. Retrieval
   found the procedure, the graph found what it applies to.
2. **structure → text.** The same edges walked back. A three-hop typed query
   names exactly one resource, and the prose about that resource comes back —
   instead of hoping a keyword search for "capacity" finds the right three.
3. **retrieval inside the set algebra.** The passages become a `static` object
   set, intersected with a graph traversal. The question is asked in words and
   answered in objects, with keys and properties you can act on.

## What this example found

Writing it against the real API surfaced four defects. Two are fixed; the
remaining two are commented where they bite.

Fixed:

- **Naming an object erased its properties.** An upsert replaced the property
  map, so a document declaring the entities it mentions — identity and no
  detail, which is the shape the knowledge API asks for — wiped every domain
  property an earlier, fuller write had established. Silently, with the
  ontology's required-property check still passing because the primary key was
  the one thing the mention carried. An upsert now updates: a property the
  request names wins, one it leaves out survives. Two write paths had the same
  bug and now share one merger.
- **A strict ontology blocked embedder-backed ingestion entirely.** The
  built-in extractor types everything it finds `entity`, that was validated as
  an ordinary object type, and no user schema declares it — so the two headline
  features were mutually exclusive and the error blamed the user's schema for a
  name the library had chosen. The bookkeeping type is now exempt, unless a
  schema declares it and means something by it.

Still open:

- **`OntologyProperty.Vectorized` is compiled and never used.** Nothing writes
  a vector for the property, so the `nearest_neighbors` object-set predicate —
  implemented on the read side — has nothing to match. Seam (c) uses a static
  set from retrieval instead.
- **GraphRAG's chunk→entity enrichment looks for `node_type = 'entity'`,** and
  a typed entity gets its ontology type as its node type. Enrichment therefore
  goes quiet for exactly the users who adopted the ontology. This example walks
  the mention edges itself.
