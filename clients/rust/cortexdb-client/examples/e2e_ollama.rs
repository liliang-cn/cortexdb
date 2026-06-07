//! Full end-to-end demo against local Ollama:
//!   1. qwen3.5 extracts entities/relations from a document (LLM step)
//!   2. SaveKnowledge writes content + graph artifacts through the sidecar
//!   3. embeddinggemma powers vector GraphRAG search (embedder step)
//!   4. The knowledge graph is queried back via the tools surface
//!
//! Prereqs:
//!   ollama pull embeddinggemma qwen3.5
//!   OPENAI_BASE_URL=http://localhost:11434/v1 \
//!   CORTEXDB_EMBED_MODEL=embeddinggemma CORTEXDB_EMBED_DIM=768 \
//!   go run ./cmd/cortexdb-grpc -db e2e.db -addr 127.0.0.1:46137 -token e2e
//!
//! Run:
//!   CORTEXDB_GRPC_ENDPOINT=http://127.0.0.1:46137 CORTEXDB_GRPC_TOKEN=e2e \
//!   cargo run --example e2e_ollama

use cortexdb_client::{proto, CortexClient};

const DOC: &str = "CortexDB is a pure-Go AI memory library created by Liang Li. \
It stores vectors, full-text indexes, and RDF knowledge graphs inside a single \
SQLite file. The Rust client talks to CortexDB through a gRPC sidecar.";

fn extract_json(raw: &str) -> Option<serde_json::Value> {
    let start = raw.find('{')?;
    let end = raw.rfind('}')?;
    serde_json::from_str(&raw[start..=end]).ok()
}

async fn qwen_extract(
    ollama: &str,
) -> Result<serde_json::Value, Box<dyn std::error::Error + Send + Sync>> {
    let prompt = format!(
        "Extract entities and relations from the text below. Reply with ONLY a JSON object, \
         no prose, in this exact shape:\n\
         {{\"entities\":[{{\"name\":\"...\",\"type\":\"...\"}}],\
         \"relations\":[{{\"from\":\"...\",\"to\":\"...\",\"type\":\"...\"}}]}}\n\
         Entity types: Person, Library, Language, Database, Protocol.\n\nText: {DOC}"
    );
    // Ollama native chat API with think:false — qwen3.5 is a reasoning model
    // and would otherwise spend minutes (and the whole token budget) thinking.
    let client = reqwest::Client::new();
    let resp: serde_json::Value = client
        .post(format!("{ollama}/api/chat"))
        .json(&serde_json::json!({
            "model": "qwen3.5",
            "think": false,
            "stream": false,
            "messages": [{"role": "user", "content": prompt}],
        }))
        .timeout(std::time::Duration::from_secs(300))
        .send()
        .await?
        .json()
        .await?;
    let content = resp["message"]["content"]
        .as_str()
        .ok_or("no content in chat response")?;
    extract_json(content).ok_or_else(|| format!("qwen3.5 did not return JSON: {content}").into())
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let endpoint = std::env::var("CORTEXDB_GRPC_ENDPOINT")
        .unwrap_or_else(|_| "http://127.0.0.1:46137".to_string());
    let ollama =
        std::env::var("OLLAMA_BASE").unwrap_or_else(|_| "http://localhost:11434".to_string());

    // 1. LLM extraction with qwen3.5.
    println!("[1/4] qwen3.5: extracting entities/relations ...");
    let extraction = qwen_extract(&ollama).await?;
    let entities: Vec<proto::ToolEntityInput> = extraction["entities"]
        .as_array()
        .unwrap_or(&vec![])
        .iter()
        .map(|e| proto::ToolEntityInput {
            name: e["name"].as_str().unwrap_or_default().to_string(),
            r#type: e["type"].as_str().unwrap_or_default().to_string(),
            ..Default::default()
        })
        .filter(|e| !e.name.is_empty())
        .collect();
    let relations: Vec<proto::ToolRelationInput> = extraction["relations"]
        .as_array()
        .unwrap_or(&vec![])
        .iter()
        .map(|r| proto::ToolRelationInput {
            from: r["from"].as_str().unwrap_or_default().to_string(),
            to: r["to"].as_str().unwrap_or_default().to_string(),
            r#type: r["type"].as_str().unwrap_or_default().to_string(),
            ..Default::default()
        })
        .filter(|r| !r.from.is_empty() && !r.to.is_empty())
        .collect();
    println!(
        "      entities: {:?}",
        entities.iter().map(|e| &e.name).collect::<Vec<_>>()
    );
    println!(
        "      relations: {:?}",
        relations
            .iter()
            .map(|r| format!("{} -{}-> {}", r.from, r.r#type, r.to))
            .collect::<Vec<_>>()
    );

    // 2. Save knowledge (content + graph artifacts) through the sidecar.
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
        "[2/4] sidecar: cortexdb {} (embedder: {}) — saving knowledge ...",
        info.version, info.has_embedder
    );
    if !info.has_embedder {
        return Err("sidecar is in lexical mode; start it with the ollama embedder env".into());
    }
    let saved = client
        .knowledge()
        .save_knowledge(proto::SaveKnowledgeRequest {
            knowledge_id: "e2e-cortexdb".into(),
            title: "What is CortexDB".into(),
            content: DOC.into(),
            entities: entities.clone(),
            relations,
            ..Default::default()
        })
        .await?
        .into_inner();
    println!(
        "      doc node: {}, entity nodes: {}, relation edges: {}",
        saved.document_node_id,
        saved.entity_node_ids.len(),
        saved.relation_edge_ids.len()
    );

    // 3. Vector search via embeddinggemma.
    println!("[3/4] embeddinggemma: vector GraphRAG search ...");
    let res = client
        .knowledge()
        .search_knowledge(proto::SearchKnowledgeRequest {
            query: "which library keeps AI memory in one SQLite file?".into(),
            top_k: 3,
            ..Default::default()
        })
        .await?
        .into_inner();
    let top = res.results.first().ok_or("no search results")?;
    println!(
        "      top hit: {} (score {:.3}), entities in context: {:?}",
        top.knowledge_id, top.score, res.entities
    );
    assert_eq!(top.knowledge_id, "e2e-cortexdb");

    // 4. Query the graph back through the generic tools surface, expanding
    //    around the entity nodes SaveKnowledge created.
    println!("[4/4] tools: expand_graph around extracted entity nodes ...");
    let out = client
        .tools()
        .call_tool(proto::CallToolRequest {
            name: "expand_graph".into(),
            args_json: serde_json::json!({
                "node_ids": saved.entity_node_ids,
                "max_hops": 2,
            })
            .to_string(),
        })
        .await?
        .into_inner();
    let parsed: serde_json::Value = serde_json::from_str(&out.result_json)?;
    let nodes = parsed["nodes"].as_array().map(|a| a.len()).unwrap_or(0);
    let edges = parsed["edges"].as_array().map(|a| a.len()).unwrap_or(0);
    println!("      subgraph: {nodes} nodes, {edges} edges");
    assert!(nodes > 0, "expected a non-empty subgraph");

    println!(
        "\nE2E OK: qwen3.5 extraction -> gRPC save -> embeddinggemma vector search -> graph expand"
    );
    Ok(())
}
