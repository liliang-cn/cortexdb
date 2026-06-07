//! Integration test against a real sidecar.
//! Requires the Go binary: go build -o clients/rust/target/cortexdb-grpc ./cmd/cortexdb-grpc
//! Skips (passes) when CORTEXDB_GRPC_BIN is unset and the default path is missing.

use std::process::Stdio;

use cortexdb_client::proto;
use cortexdb_client::CortexClient;

fn binary_path() -> Option<std::path::PathBuf> {
    if let Ok(p) = std::env::var("CORTEXDB_GRPC_BIN") {
        return Some(p.into());
    }
    let fallback =
        std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../target/cortexdb-grpc");
    fallback.exists().then_some(fallback)
}

async fn wait_for_client(endpoint: &str, token: &str) -> Option<CortexClient> {
    for _ in 0..50 {
        if let Ok(c) = CortexClient::builder(endpoint.to_string())
            .token(token)
            .connect()
            .await
        {
            if c.admin().health(proto::HealthRequest {}).await.is_ok() {
                return Some(c);
            }
        }
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
    }
    None
}

#[tokio::test]
async fn roundtrip_against_sidecar() {
    let Some(bin) = binary_path() else {
        eprintln!("skipping: cortexdb-grpc binary not found (set CORTEXDB_GRPC_BIN)");
        return;
    };
    let dir = std::env::temp_dir().join(format!("cortexdb-it-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let db = dir.join("it.db");

    // Pick an uncommon fixed high port for the test.
    let addr = "127.0.0.1:46137";
    let mut child = tokio::process::Command::new(&bin)
        .arg("-db")
        .arg(&db)
        .arg("-addr")
        .arg(addr)
        .arg("-token")
        .arg("it-token")
        .env_remove("OPENAI_BASE_URL") // force lexical mode regardless of host env/.env
        .current_dir(&dir)
        .stdout(Stdio::null())
        .stderr(Stdio::inherit())
        .spawn()
        .expect("spawn sidecar");

    let endpoint = format!("http://{addr}");
    let client = wait_for_client(&endpoint, "it-token")
        .await
        .expect("sidecar did not become healthy");

    // Wrong token must fail.
    let bad = CortexClient::builder(endpoint.clone())
        .token("wrong")
        .connect()
        .await
        .unwrap();
    let err = bad
        .admin()
        .health(proto::HealthRequest {})
        .await
        .unwrap_err();
    assert_eq!(err.code(), tonic::Code::Unauthenticated);

    // Admin info reports lexical mode.
    let info = client
        .admin()
        .info(proto::InfoRequest {})
        .await
        .unwrap()
        .into_inner();
    assert!(!info.version.is_empty());
    assert!(!info.has_embedder);

    // Knowledge round-trip (lexical mode).
    let saved = client
        .knowledge()
        .save_knowledge(proto::SaveKnowledgeRequest {
            knowledge_id: "k1".into(),
            title: "Rust ownership".into(),
            content: "Ownership is Rust's central memory management concept; borrowing and lifetimes flow from it.".into(),
            ..Default::default()
        })
        .await
        .expect("save")
        .into_inner();
    assert_eq!(saved.knowledge.unwrap().id, "k1");

    let found = client
        .knowledge()
        .search_knowledge(proto::SearchKnowledgeRequest {
            query: "ownership borrowing".into(),
            top_k: 3,
            ..Default::default()
        })
        .await
        .expect("search")
        .into_inner();
    assert!(!found.results.is_empty());
    assert_eq!(found.results[0].knowledge_id, "k1");

    // Memory round-trip.
    let mem = client
        .memory()
        .save_memory(proto::SaveMemoryRequest {
            memory_id: "m1".into(),
            user_id: "u1".into(),
            scope: "user".into(),
            content: "User prefers tabs over spaces.".into(),
            ..Default::default()
        })
        .await
        .expect("save memory")
        .into_inner();
    assert_eq!(mem.memory.unwrap().user_id, "u1");

    // Knowledge graph: triples + SPARQL.
    client
        .graph()
        .upsert_namespace(proto::UpsertNamespaceRequest {
            prefix: "ex".into(),
            uri: "https://example.com/".into(),
        })
        .await
        .expect("namespace");
    let iri = |v: &str| proto::RdfTerm {
        kind: "iri".into(),
        value: v.into(),
        ..Default::default()
    };
    client
        .graph()
        .upsert_knowledge_graph(proto::UpsertKnowledgeGraphRequest {
            triples: vec![proto::RdfTriple {
                subject: Some(iri("ex:alice")),
                predicate: Some(iri("ex:knows")),
                object: Some(iri("ex:bob")),
                ..Default::default()
            }],
        })
        .await
        .expect("triples");
    let q = client
        .graph()
        .query_sparql(proto::QuerySparqlRequest {
            query: "SELECT ?o WHERE { <https://example.com/alice> <https://example.com/knows> ?o . }"
                .into(),
        })
        .await
        .expect("sparql")
        .into_inner();
    assert_eq!(q.result.unwrap().count, 1);

    // Tools surface.
    let tools = client
        .tools()
        .list_tools(proto::ListToolsRequest {})
        .await
        .unwrap()
        .into_inner();
    assert!(tools.tools.iter().any(|t| t.name == "knowledge_search"));

    child.kill().await.ok();
    std::fs::remove_dir_all(&dir).ok();
}
