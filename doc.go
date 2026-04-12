// Package cortexdb is the public documentation entrypoint for CortexDB.
//
// CortexDB is a pure-Go, single-file AI memory and knowledge graph library.
// It uses SQLite as the storage kernel and exposes vector search, lexical
// search, RAG knowledge storage, scoped agent memory, RDF/SPARQL/RDFS/SHACL
// knowledge graph features, corpus-to-graph workflows, and MCP-aligned tool
// APIs.
//
// # Architecture
//
// The main packages are:
//
//   - pkg/cortexdb: primary DB facade for vectors, text search, knowledge,
//     memory, KnowledgeMemory recall, knowledge graph APIs, GraphRAG tools, and MCP.
//   - pkg/memoryflow: agent memory workflow for transcript ingest, recall,
//     wake-up layers, diary, transcript reconstruction, and promotion.
//   - pkg/graphflow: corpus-to-graph workflow for extraction schema, build,
//     analysis, report, JSON/Markdown/HTML export, and optional LLM extraction.
//   - pkg/graph: low-level graph engine for property graph operations,
//     RDF triples/quads, SPARQL, RDFS-lite inference, and SHACL-lite validation.
//   - pkg/core: low-level SQLite storage, embeddings, FTS5, indexes, and
//     chat/session primitives.
//
// Use pkg/cortexdb first unless you need a workflow layer or low-level graph
// control.
//
// # Quick Start
//
//	import (
//		"context"
//		"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
//	)
//
//	func main() {
//		db, _ := cortexdb.Open(cortexdb.DefaultConfig("KnowledgeMemory.db"))
//		defer db.Close()
//
//		ctx := context.Background()
//		quick := db.Quick()
//
//		_, _ = quick.Add(ctx, []float32{0.1, 0.2, 0.9}, "SQLite is a single-file database.")
//		_, _ = quick.Search(ctx, []float32{0.1, 0.2, 0.8}, 3)
//	}
//
// # Knowledge and Memory
//
// Durable knowledge and scoped memory are available directly on DB:
//
//	_, _ = db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
//		KnowledgeID: "apollo-plan",
//		Title:       "Apollo launch plan",
//		Content:     "Alice owns Apollo. Apollo ships on Friday.",
//		Keywords:    nil,
//	})
//
//	_, _ = db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
//		Query:         "Who owns Apollo?",
//		Keywords:      []string{"Apollo", "Alice", "owns"},
//		RetrievalMode: cortexdb.RetrievalModeLexical,
//		TopK:          3,
//	})
//
//	_, _ = db.SaveMemory(ctx, cortexdb.MemorySaveRequest{
//		MemoryID:  "style",
//		UserID:    "user-1",
//		Scope:     cortexdb.MemoryScopeUser,
//		Namespace: "assistant",
//		Content:   "User prefers concise status updates.",
//	})
//
// # Knowledge Graph
//
// The high-level knowledge graph API supports RDF triples/quads, import/export,
// SPARQL, RDFS-lite inference, and SHACL-lite validation:
//
//	_, _ = db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
//		Triples: []cortexdb.KnowledgeGraphTriple{
//			{
//				Subject:   graph.NewIRI("https://example.com/alice"),
//				Predicate: graph.NewIRI(graph.RDFType),
//				Object:    graph.NewIRI("https://example.com/Person"),
//			},
//		},
//	})
//
//	_, _ = db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
//		Query: `SELECT ?o WHERE { <https://example.com/alice> ?p ?o . }`,
//	})
//
//	_, _ = db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{
//		Mode: cortexdb.KnowledgeGraphInferenceRefreshModeIncremental,
//	})
//
// SPARQL support is a practical embedded subset. It includes SELECT, ASK,
// CONSTRUCT, DESCRIBE, update forms, OPTIONAL, UNION, MINUS, VALUES, BIND,
// FILTER, EXISTS, NOT EXISTS, aggregates, subqueries, and constrained property
// paths such as ^pred, p|q, p+, and p*.
//
// # MemoryFlow
//
// Use pkg/memoryflow for chat/session memory workflows:
//
//	flow, _ := memoryflow.New(db, planner, extractor)
//	_, _ = flow.IngestTranscript(ctx, memoryflow.IngestTranscriptRequest{...})
//	_, _ = flow.WakeUpLayers(ctx, memoryflow.WakeUpLayersRequest{...})
//
// Hindsight can be used as an optional recall strategy plugin without replacing
// memoryflow:
//
//	flow, _ := memoryflow.New(
//		db,
//		planner,
//		extractor,
//		memoryflow.WithRecallStrategy(hindsight.NewStrategy(db, hindsight.StrategyOptions{
//			BankID:      "apollo-agent",
//			EntityNames: []string{"Apollo"},
//			Keywords:    []string{"deadline"},
//			UseKG:       true,
//		})),
//	)
//
// LLM-dependent parts are interfaces: QueryPlanner, SessionExtractor, and
// PromotionPolicy.
//
// # GraphFlow
//
// Use pkg/graphflow for corpus-to-graph workflows:
//
//	// ExtractionResult can come from deterministic code or an LLM.
//	_, _ = graphflow.Build(ctx, db, []graphflow.ExtractionResult{extraction}, graphflow.BuildOptions{})
//	report, _ := graphflow.Analyze(ctx, db, graphflow.AnalyzeRequest{TopN: 10})
//	_, _ = graphflow.Export(ctx, db, graphflow.ExportRequest{OutputDir: "graphflow-out", Analysis: report})
//
// LLM extraction depends only on graphflow.JSONGenerator. The example
// examples/05_graphflow demonstrates github.com/openai/openai-go/v3 with JSON
// Schema structured output.
//
// # Tools and MCP
//
// In-process tool calling:
//
//	tools := db.GraphRAGTools()
//	defs := tools.Definitions()
//	resp, err := tools.Call(ctx, "knowledge_graph_query", payload)
//	_, _, _ = defs, resp, err
//
// MCP:
//
//	server := db.NewMCPServer(cortexdb.MCPServerOptions{})
//	_ = server
//
// # Examples
//
// The examples directory is organized by architecture:
//
//	go run ./examples/01_core
//	go run ./examples/02_rag
//	go run ./examples/03_memoryflow
//	go run ./examples/04_knowledge_graph
//	go run ./examples/05_graphflow
//	go run ./examples/06_tools_mcp
package cortexdb
