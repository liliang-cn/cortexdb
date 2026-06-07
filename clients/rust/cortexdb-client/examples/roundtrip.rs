//! Save → search round-trip against a running sidecar.
//!
//! Start the sidecar first:
//!   go run ./cmd/cortexdb-grpc -db demo.db
//! Then:
//!   cargo run --example roundtrip
//! Optional env: CORTEXDB_GRPC_ENDPOINT, CORTEXDB_GRPC_TOKEN

use cortexdb_client::{proto, CortexClient};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let endpoint = std::env::var("CORTEXDB_GRPC_ENDPOINT")
        .unwrap_or_else(|_| "http://127.0.0.1:47821".to_string());
    let mut builder = CortexClient::builder(endpoint);
    if let Ok(token) = std::env::var("CORTEXDB_GRPC_TOKEN") {
        builder = builder.token(token);
    }
    let client = builder.connect().await?;

    let info = client
        .admin()
        .info(proto::InfoRequest {})
        .await?
        .into_inner();
    println!(
        "connected: cortexdb {} (embedder: {})",
        info.version, info.has_embedder
    );

    client
        .knowledge()
        .save_knowledge(proto::SaveKnowledgeRequest {
            knowledge_id: "demo-1".into(),
            title: "CortexDB".into(),
            content: "CortexDB is a single-file AI memory and knowledge graph database for Go, \
                      now reachable from Rust over gRPC."
                .into(),
            ..Default::default()
        })
        .await?;
    println!("saved knowledge demo-1");

    let res = client
        .knowledge()
        .search_knowledge(proto::SearchKnowledgeRequest {
            query: "AI memory database".into(),
            top_k: 3,
            ..Default::default()
        })
        .await?
        .into_inner();
    for hit in res.results {
        println!(
            "hit: {} (score {:.3}) — {}",
            hit.knowledge_id, hit.score, hit.snippet
        );
    }
    Ok(())
}
