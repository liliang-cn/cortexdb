// Command query_source shows an external search engine acting as one retrieval
// lane inside CortexDB, without becoming CortexDB's storage.
//
// The distinction is the whole point. Meilisearch is excellent at finding text
// and cannot hold a knowledge graph, a session, an ontology or a transaction.
// So it is given the job it is good at — naming candidates — and CortexDB keeps
// the one it is good at: owning the data and fusing the lanes.
//
// Run a Meilisearch first (any port; this defaults to a deliberately unusual one):
//
//	docker run -d --name meili -p 127.0.0.1:43530:7700 \
//	  -e MEILI_MASTER_KEY=dev-key -e MEILI_ENV=development getmeili/meilisearch:v1.11
//	MEILI_URL=http://127.0.0.1:43530 MEILI_KEY=dev-key go run ./examples/17_query_source
//
// With no Meilisearch reachable it says so and exits — a demo that silently
// proved nothing would be worse than no demo.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// The corpus. Small on purpose: what is being demonstrated is the seam, not
// retrieval quality at scale.
var corpus = []struct{ id, text string }{
	{"runbook-1", "Apollo deploy runbook: drain the node, roll the pods, verify the health probe."},
	{"runbook-2", "Apollo rollback: pin the previous image tag and restart the deployment."},
	{"incident-7", "The datacentre lost mains supply on Friday. Batteries carried the racks for nine minutes and nothing was lost."},
	{"handbook-3", "Leo maintains the storage layer and reviews every schema change."},
}

func main() {
	ctx := context.Background()

	meili := &MeiliSource{
		BaseURL: envOr("MEILI_URL", "http://127.0.0.1:43530"),
		Index:   envOr("MEILI_INDEX", "cortexdb"),
		APIKey:  os.Getenv("MEILI_KEY"),
	}

	dbPath := "query_source_demo.db"
	_ = os.Remove(dbPath)
	defer func() { _ = os.Remove(dbPath) }()

	// No embedder is configured and no vector lane is used, which keeps the
	// comparison honest: nothing below wins because it has better embeddings.
	// The placeholder vectors exist only because the store wants one per row.
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath), cortexdb.WithQuerySource(meili))
	if err != nil {
		log.Fatalf("open cortexdb: %v", err)
	}
	defer func() { _ = db.Close() }()

	fmt.Println("registered query sources:", db.QuerySources())

	// The brain owns the documents.
	docs := make([]map[string]any, 0, len(corpus))
	for _, d := range corpus {
		if err := db.InsertTextWithVector(ctx, d.id, d.text, placeholderVector(d.id), nil); err != nil {
			log.Fatalf("insert %s: %v", d.id, err)
		}
		docs = append(docs, map[string]any{"id": d.id, "text": d.text})
	}

	// The search engine gets a copy for finding purposes only.
	if err := meili.Index_(ctx, docs); err != nil {
		log.Fatalf("\nmeilisearch unreachable at %s: %v\n\nStart one first — see the comment at the top of this file.", meili.BaseURL, err)
	}
	if err := meili.WaitIdle(ctx, 20*time.Second); err != nil {
		log.Fatalf("meilisearch indexing: %v", err)
	}

	// Two queries, chosen to be fair to both engines.
	//
	// "apollo" is a word both indexes hold, so it shows fusion: two lanes
	// voting, and each result carrying the lanes that found it.
	//
	// "rollbak" is a typo. CortexDB's FTS5 matches tokens and finds nothing —
	// correctly, there is no such token. Meilisearch is typo-tolerant by
	// design and finds the rollback runbook. That is the honest case for
	// giving a search engine a lane: not that it is smarter, but that it
	// indexed the text differently.
	const question = "apollo"
	const typo = "rollbak"

	// 1. CortexDB alone.
	show(db, ctx, "CortexDB lexical only", cortexdb.QueryRequest{
		Query:      question,
		Prefetch:   []cortexdb.QueryPrefetch{{Name: "lexical", Kind: cortexdb.QueryPrefetchLexical, Query: question}},
		Limit:      3,
		IncludeRaw: true,
	})

	// 2. Meilisearch as a lane, fused with CortexDB's own.
	show(db, ctx, "CortexDB lexical + Meilisearch lane", cortexdb.QueryRequest{
		Query: question,
		Prefetch: []cortexdb.QueryPrefetch{
			{Name: "lexical", Kind: cortexdb.QueryPrefetchLexical, Query: question},
			{Kind: cortexdb.QueryPrefetchSource, Source: "meilisearch"},
		},
		Fusion:     cortexdb.QueryFusionRRF,
		Limit:      3,
		IncludeRaw: true,
	})

	// 3. The typo. This is what the lane is actually buying.
	show(db, ctx, "typo \"rollbak\" — CortexDB lexical only", cortexdb.QueryRequest{
		Query:      typo,
		Prefetch:   []cortexdb.QueryPrefetch{{Name: "lexical", Kind: cortexdb.QueryPrefetchLexical, Query: typo}},
		Limit:      3,
		IncludeRaw: true,
	})
	show(db, ctx, "typo \"rollbak\" — with the Meilisearch lane", cortexdb.QueryRequest{
		Query: typo,
		Prefetch: []cortexdb.QueryPrefetch{
			{Name: "lexical", Kind: cortexdb.QueryPrefetchLexical, Query: typo},
			{Kind: cortexdb.QueryPrefetchSource, Source: "meilisearch"},
		},
		Limit:      3,
		IncludeRaw: true,
	})

	// 4. What a stale external index cannot do. Meilisearch still holds
	//    incident-7; CortexDB no longer does.
	if err := db.Vector().Delete(ctx, "runbook-2"); err != nil {
		log.Fatalf("delete: %v", err)
	}
	show(db, ctx, "after deleting runbook-2 from CortexDB only", cortexdb.QueryRequest{
		Query:      typo,
		Prefetch:   []cortexdb.QueryPrefetch{{Kind: cortexdb.QueryPrefetchSource, Source: "meilisearch"}},
		Limit:      3,
		IncludeRaw: true,
	})
	fmt.Println("\nThe lane still named runbook-2. CortexDB dropped it, because a")
	fmt.Println("candidate it cannot read is not a result. The external index is")
	fmt.Println("recall, never the record.")
}

func show(db *cortexdb.DB, ctx context.Context, title string, req cortexdb.QueryRequest) {
	fmt.Printf("\n== %s ==\n", title)
	resp, err := db.Query(ctx, req)
	if err != nil {
		log.Fatalf("%s: %v", title, err)
	}
	if len(resp.Results) == 0 {
		fmt.Println("  (nothing)")
		return
	}
	for i, r := range resp.Results {
		fmt.Printf("  %d. %-12s %s\n", i+1, r.ID, truncate(r.Content, 62))
		if len(r.SourceScores) > 0 {
			fmt.Printf("     lanes: %v\n", r.SourceScores)
		}
	}
}

// placeholderVector keeps rows insertable without an embedder. It carries no
// meaning and no lane in this example reads it.
func placeholderVector(id string) []float32 {
	var sum float32
	for _, r := range id {
		sum += float32(r)
	}
	return []float32{sum / 1000, 0, 0}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
