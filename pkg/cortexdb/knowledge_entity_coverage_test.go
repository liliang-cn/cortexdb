package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Two transcripts. Each guest's own company fills their chunks with the
// question's words; the one chunk where each says they worked at Oracle
// shares few of them.
var coverageDocs = []struct {
	id, text string
	names    []string
	rels     []ToolRelationInput
}{
	{"khan", strings.Join([]string{
		"Sal Khan said the company Khan Academy was the work he cared about.",
		"Sal Khan said the company grew because people work hard there every day.",
		"Sal Khan said the company hired people to work on free courses for everyone.",
		"Sal Khan said a company like this needs work on its mission all the time.",
		"After the masters Sal Khan moved west as a product manager at Oracle.",
	}, " "), []string{"Sal Khan", "Khan Academy", "Oracle"}, []ToolRelationInput{{From: "Sal Khan", To: "Oracle", Type: "worked_at"}, {From: "Sal Khan", To: "Khan Academy", Type: "founded"}}},
	{"narkhede", strings.Join([]string{
		"Neha Narkhede said the company Confluent was the work she cared about.",
		"Neha Narkhede said the company grew because people work hard there every day.",
		"Neha Narkhede said the company hired people to work on streaming for everyone.",
		"Neha Narkhede said a company like this needs work on its culture all the time.",
		"Before graduating Neha Narkhede interned in the database group at Oracle.",
	}, " "), []string{"Neha Narkhede", "Confluent", "Oracle"}, []ToolRelationInput{{From: "Neha Narkhede", To: "Oracle", Type: "worked_at"}, {From: "Neha Narkhede", To: "Confluent", Type: "founded"}}},
}

const coverageQuestion = "What company did both Sal Khan and Neha Narkhede work for?"

// openCoverageBrain saves the transcripts in sentence-sized chunks, each
// entity linked to the chunks that name it.
func openCoverageBrain(t *testing.T, embedder Embedder) *DB {
	t.Helper()
	cfg := DefaultConfig(filepath.Join(t.TempDir(), "coverage.db"))
	var opts []Option
	if embedder != nil {
		cfg.Dimensions = embedder.Dim()
		opts = append(opts, WithEmbedder(embedder))
	}
	db, err := Open(cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const size = 13
	for _, d := range coverageDocs {
		chunks := splitGraphRAGText(d.text, size, 0)
		var entities []ToolEntityInput
		for _, name := range d.names {
			var ids []string
			for i, c := range chunks {
				if strings.Contains(c, name) {
					ids = append(ids, graphChunkNodeID(d.id, i))
				}
			}
			entities = append(entities, ToolEntityInput{Name: name, Type: "entity", ChunkIDs: ids})
		}
		if _, err := db.SaveKnowledge(context.Background(), KnowledgeSaveRequest{
			KnowledgeID: d.id, Title: d.id, Content: d.text, ChunkSize: size,
			Entities: entities, Relations: d.rels,
		}); err != nil {
			t.Fatalf("save %s: %v", d.id, err)
		}
	}
	return db
}

func coverageEmbedder() Embedder {
	return newKeywordEmbedder("company", "work", "people", "oracle", "masters", "database", "sal", "khan", "neha", "narkhede")
}

// A question naming two entities gets, for what they share, the chunk where
// each of them is said to be related to it (#251): both Oracle chunks, which
// the question's wording ranks below each guest's own company.
func TestSharedNeighbourEvidenceReachesTheResult(t *testing.T) {
	for _, tc := range []struct {
		name     string
		embedder Embedder
	}{{"hybrid", coverageEmbedder()}, {"lexical", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			db := openCoverageBrain(t, tc.embedder)
			resp, err := db.SearchKnowledge(context.Background(), KnowledgeSearchRequest{Query: coverageQuestion, TopK: 4, MaxContextChunks: 4})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, c := range resp.Chunks {
				got = append(got, c.Content)
			}
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, "product manager at Oracle") || !strings.Contains(joined, "database group at Oracle") {
				t.Fatalf("the result lacks a guest's Oracle chunk:\n%s", joined)
			}
			if len(resp.Chunks) > 4 {
				t.Fatalf("%d chunks, over the limit of 4", len(resp.Chunks))
			}
		})
	}
}

// return_paths returns the relation path between the entities the question
// names, through what they share (#251).
func TestKnowledgeSearchReturnsPathsBetweenNamedEntities(t *testing.T) {
	db := openCoverageBrain(t, coverageEmbedder())
	resp, err := db.SearchKnowledge(context.Background(), KnowledgeSearchRequest{Query: coverageQuestion, TopK: 4, ReturnPaths: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range resp.Paths {
		text := strings.ToLower(p.Sentence)
		if strings.Contains(text, "sal khan") && strings.Contains(text, "oracle") && strings.Contains(text, "neha narkhede") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no path Sal Khan - Oracle - Neha Narkhede in %+v", resp.Paths)
	}
	plain, err := db.SearchKnowledge(context.Background(), KnowledgeSearchRequest{Query: coverageQuestion, TopK: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Paths) != 0 {
		t.Fatalf("paths returned unasked: %+v", plain.Paths)
	}
}

// A question naming one entity is left as retrieval ranked it.
func TestOneNamedEntityIsLeftAlone(t *testing.T) {
	db := openCoverageBrain(t, coverageEmbedder())
	resp, err := db.SearchKnowledge(context.Background(), KnowledgeSearchRequest{Query: "What company did Sal Khan work for?", TopK: 2, MaxContextChunks: 2})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := db.coverQueryEntities(context.Background(), "What company did Sal Khan work for?", resp.Plan, ToolSearchGraphRAGLexicalRequest{Query: "What company did Sal Khan work for?"}, &GraphRAGQueryResult{Chunks: resp.Chunks}, GraphRAGQueryOptions{TopK: 2, MaxContextChunks: 2, MaxContextChars: 4000, PerDocumentLimit: 2})
	if err != nil || changed {
		t.Fatalf("one entity: changed=%v err=%v", changed, err)
	}
}

// Recall's graph facts lead with those relating the entities the question
// names: each one's relation to what they share (#251).
func TestRecallGraphFactsLeadWithWhatRelatesTheNamedEntities(t *testing.T) {
	db := openCoverageBrain(t, nil)
	resp, err := db.KnowledgeMemory().Recall(context.Background(), KnowledgeMemoryRecallRequest{Query: coverageQuestion})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GraphFacts) < 2 {
		t.Fatalf("graph facts %+v", resp.GraphFacts)
	}
	for _, f := range resp.GraphFacts[:2] {
		if f.Predicate != "worked_at" || f.Object != "Oracle" {
			t.Fatalf("the facts lead with %+v, want the two worked_at Oracle", resp.GraphFacts[:2])
		}
	}
}
