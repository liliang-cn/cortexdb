// Cortex Query — composable retrieval over one local CortexDB file.
//
// This example shows the "third path" CortexDB is aiming for: no Qdrant,
// Neo4j, or service stack, but still a practical agent retrieval pipeline:
//
//	dense vector prefetch + lexical FTS5 prefetch + graph/entity prefetch
//	                  -> weighted RRF fusion -> optional payload/formula ranking
//
// It runs fully offline with precomputed toy vectors.
//
//	go run ./examples/15_cortex_query
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

type document struct {
	id       string
	title    string
	content  string
	metadata map[string]string
	entities []string
}

var docs = []document{
	{
		id:      "apollo",
		title:   "Apollo Launch Plan",
		content: "Apollo launch readiness review is scheduled for Friday. Alice owns telemetry, rollback, and incident response.",
		metadata: map[string]string{
			"project":    "Apollo",
			"category":   "ops",
			"importance": "10",
		},
		entities: []string{"Apollo", "Alice"},
	},
	{
		id:      "atlas",
		title:   "Atlas Billing Migration",
		content: "Atlas billing migration moves invoices to the new ledger. Bob owns customer notifications and reconciliation.",
		metadata: map[string]string{
			"project":    "Atlas",
			"category":   "billing",
			"importance": "6",
		},
		entities: []string{"Atlas", "Bob"},
	},
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "cortex-query")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "brain.db")))
	if err != nil {
		return err
	}
	defer db.Close()

	tools := db.GraphRAGTools()
	for _, doc := range docs {
		resp, err := tools.IngestDocument(ctx, cortexdb.ToolIngestDocumentRequest{
			DocumentID: doc.id,
			Title:      doc.title,
			Content:    doc.content,
			ChunkSize:  64,
			Metadata:   doc.metadata,
		})
		if err != nil {
			return err
		}

		entities := make([]cortexdb.ToolEntityInput, 0, len(doc.entities))
		for _, name := range doc.entities {
			entities = append(entities, cortexdb.ToolEntityInput{
				Name:     name,
				Type:     "entity",
				ChunkIDs: resp.ChunkNodeIDs,
			})
		}
		if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{
			DocumentID: doc.id,
			Entities:   entities,
		}); err != nil {
			return err
		}
	}

	query := "Apollo launch readiness Alice"
	resp, err := db.Query(ctx, cortexdb.QueryRequest{
		Query:       query,
		QueryVector: unitVector(64, 0),
		EntityNames: []string{"Apollo", "Alice"},
		Fusion:      cortexdb.QueryFusionWeightedRRF,
		Limit:       3,
		IncludeRaw:  true,
		Prefetch: []cortexdb.QueryPrefetch{
			{Name: "dense", Kind: cortexdb.QueryPrefetchVector, Weight: 1.0, Limit: 4},
			{Name: "lexical", Kind: cortexdb.QueryPrefetchLexical, Query: query, Weight: 1.0, Limit: 4},
			{Name: "graph", Kind: cortexdb.QueryPrefetchGraph, EntityNames: []string{"Apollo", "Alice"}, MaxHops: 1, Weight: 1.4, Limit: 4},
		},
		Filter: &cortexdb.QueryFilter{
			MustNot: []cortexdb.QueryCondition{{Field: "category", Op: cortexdb.QueryFilterEqual, Value: "archived"}},
		},
		Formula: &cortexdb.QueryScoreFormula{
			NumericBoosts: []cortexdb.QueryNumericBoost{{Field: "importance", MaxValue: 10, Weight: 0.1}},
		},
	})
	if err != nil {
		return err
	}

	fmt.Printf("=== cortex_query: %q ===\n", query)
	fmt.Printf("fusion=%s prefetches=%s\n\n", resp.Fusion, strings.Join(resp.Prefetches, ", "))
	for i, hit := range resp.Results {
		fmt.Printf("%d. %s score=%.4f project=%s\n", i+1, hit.ID, hit.Score, hit.Metadata["project"])
		fmt.Printf("   %s\n", hit.Content)
		fmt.Printf("   source ranks: %s\n", formatRanks(hit.SourceRanks))
	}
	fmt.Println("\n✓ vector + lexical + graph retrieval in one embedded CortexDB file")
	return nil
}

func unitVector(dim int, hot int) []float32 {
	vector := make([]float32, dim)
	if hot >= 0 && hot < dim {
		vector[hot] = 1
	}
	return vector
}

func formatRanks(ranks map[string]int) string {
	if len(ranks) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(ranks))
	for name := range ranks {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=#%d", name, ranks[name]))
	}
	return strings.Join(parts, ", ")
}
