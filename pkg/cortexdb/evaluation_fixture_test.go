package cortexdb

import (
	"context"
	"path/filepath"
	"testing"
)

type evaluationCorpusDocument struct {
	ID       string
	Title    string
	Content  string
	Entities []ToolEntityInput
}

type evaluationFixture struct {
	db       *DB
	tools    *GraphRAGToolbox
	dbPath   string
	chunkIDs map[string][]string
}

func newEvaluationCorpus() []evaluationCorpusDocument {
	return []evaluationCorpusDocument{
		{
			ID:      "employment",
			Title:   "Alice at Acme",
			Content: "Alice works at Acme.",
			Entities: []ToolEntityInput{
				{Name: "Alice", Type: "person"},
				{Name: "Acme", Type: "organization"},
			},
		},
		{
			ID:      "employer-profile",
			Title:   "Acme in Berlin",
			Content: "Acme is headquartered in Berlin.",
			Entities: []ToolEntityInput{
				{Name: "Acme", Type: "organization"},
				{Name: "Berlin", Type: "city"},
			},
		},
		{
			ID:      "irrelevant-profile",
			Title:   "Beta Labs in Madrid",
			Content: "Beta Labs is headquartered in Madrid.",
			Entities: []ToolEntityInput{
				{Name: "Beta Labs", Type: "organization"},
				{Name: "Madrid", Type: "city"},
			},
		},
	}
}

func newLexicalEvaluationFixture(t testing.TB) *evaluationFixture {
	t.Helper()

	// t.TempDir(), not the package directory. A unique name is enough to stop
	// two parallel fixtures opening one file, but the database was also being
	// written next to the source and only half cleaned up: cleanup removed the
	// .db and left the -wal and -shm beside it, run after run. TempDir is
	// removed whole, by the test framework, including the files this code does
	// not know the names of.
	dbPath := filepath.Join(t.TempDir(), "evaluation_lexical.db")
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open lexical evaluation db: %v", err)
	}

	fixture := &evaluationFixture{
		db:       db,
		tools:    db.GraphRAGTools(),
		dbPath:   dbPath,
		chunkIDs: make(map[string][]string),
	}
	fixture.seedLexical(t, context.Background())
	return fixture
}

func newVectorEvaluationFixture(t testing.TB) *evaluationFixture {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "evaluation_vector.db")
	db, err := Open(DefaultConfig(dbPath), WithEmbedder(newKeywordEmbedder(
		"alice", "acme", "works", "headquartered", "berlin", "beta", "labs", "madrid",
	)))
	if err != nil {
		t.Fatalf("open vector evaluation db: %v", err)
	}

	fixture := &evaluationFixture{
		db:       db,
		tools:    db.GraphRAGTools(),
		dbPath:   dbPath,
		chunkIDs: make(map[string][]string),
	}
	fixture.seedVector(t, context.Background())
	return fixture
}

func (f *evaluationFixture) cleanup(t testing.TB) {
	t.Helper()
	if f == nil {
		return
	}
	if f.db != nil {
		_ = f.db.Close()
	}
	// The file itself is inside t.TempDir(), which the framework removes along
	// with the -wal and -shm this used to leave behind.
}

func (f *evaluationFixture) seedLexical(t testing.TB, ctx context.Context) {
	t.Helper()

	for _, doc := range newEvaluationCorpus() {
		ingestResp, err := f.tools.IngestDocument(ctx, ToolIngestDocumentRequest{
			DocumentID: doc.ID,
			Title:      doc.Title,
			Content:    doc.Content,
			ChunkSize:  24,
		})
		if err != nil {
			t.Fatalf("ingest lexical evaluation document %s: %v", doc.ID, err)
		}

		f.chunkIDs[doc.ID] = append([]string(nil), ingestResp.ChunkNodeIDs...)
		entities := make([]ToolEntityInput, 0, len(doc.Entities))
		for _, entity := range doc.Entities {
			entity.ChunkIDs = append([]string(nil), ingestResp.ChunkNodeIDs...)
			entities = append(entities, entity)
		}
		if _, err := f.tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
			DocumentID: doc.ID,
			Entities:   entities,
		}); err != nil {
			t.Fatalf("upsert lexical evaluation entities for %s: %v", doc.ID, err)
		}
	}
}

func (f *evaluationFixture) seedVector(t testing.TB, ctx context.Context) {
	t.Helper()

	for _, doc := range newEvaluationCorpus() {
		ingestResp, err := f.db.InsertGraphDocument(ctx, GraphRAGDocument{
			ID:      doc.ID,
			Title:   doc.Title,
			Content: doc.Content,
		}, GraphRAGIngestOptions{
			ChunkSize: 24,
			Extractor: fixtureExtractor{},
		})
		if err != nil {
			t.Fatalf("ingest vector evaluation document %s: %v", doc.ID, err)
		}
		f.chunkIDs[doc.ID] = append([]string(nil), ingestResp.ChunkNodeIDs...)
	}
}
