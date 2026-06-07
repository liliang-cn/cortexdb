# cortexdb-client

Typed gRPC client for [CortexDB](https://github.com/liliang-cn/cortexdb) — a
pure-Go, single-file AI memory and knowledge graph database, served as a
sidecar (`cortexdb-grpc`).

## Quick start

Start the sidecar (one binary, one SQLite file):

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=my.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc
# listening on 127.0.0.1:47821
```

Talk to it from Rust:

```rust
use cortexdb_client::{proto, CortexClient};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let client = CortexClient::builder("http://127.0.0.1:47821")
        .token("s3cret")
        .connect()
        .await?;

    client
        .knowledge()
        .save_knowledge(proto::SaveKnowledgeRequest {
            knowledge_id: "k1".into(),
            title: "Rust ownership".into(),
            content: "Ownership is Rust's central memory management concept.".into(),
            ..Default::default()
        })
        .await?;

    let hits = client
        .knowledge()
        .search_knowledge(proto::SearchKnowledgeRequest {
            query: "ownership".into(),
            top_k: 3,
            ..Default::default()
        })
        .await?
        .into_inner();
    println!("{:#?}", hits.results);
    Ok(())
}
```

Services: `.knowledge()`, `.memory()`, `.graph()` (SPARQL/RDF/SHACL/inference/
ontology), `.graphrag()`, `.tools()` (generic tool dispatch), `.admin()`.

## managed-server feature

With the `managed-server` feature the crate can resolve (env → PATH → download
from GitHub Releases) and spawn the sidecar itself, authenticated with a fresh
random token:

```rust
use cortexdb_client::sidecar::Sidecar;

let sidecar = Sidecar::ensure().await?;
let running = sidecar.spawn("my.db").await?;
let client = running.client().await?;
```

## Embeddings

The sidecar runs in lexical mode by default. Point it at any OpenAI-compatible
embeddings endpoint to enable vector retrieval (e.g. Ollama):

```bash
OPENAI_BASE_URL=http://localhost:11434/v1 \
CORTEXDB_EMBED_MODEL=embeddinggemma \
CORTEXDB_EMBED_DIM=768 \
cortexdb-grpc
```

## Regenerating the protobuf code

Generated code is committed (`src/gen/`), so building this crate never needs
`protoc`. To regenerate after a proto change: `cargo run -p gen` from
`clients/rust/`.
