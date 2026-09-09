package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// An active ontology must not make knowledge ingestion impossible.
//
// Saving prose with an embedder runs the built-in extractor over each chunk,
// and the nodes it produces carry no declared type, so validation saw the
// fallback literal "entity" and refused the write against a schema that had no
// reason to declare it. The two headline features — a governed ontology and
// embedder-backed knowledge — were therefore mutually exclusive, and the error
// blamed the user's schema for a type the library had invented.
func TestSaveKnowledgeWithAnEmbedderSurvivesAnActiveOntology(t *testing.T) {
	dbPath := fmt.Sprintf("test_bookkeeping_%d.db", testname.Nano())
	cfg := DefaultConfig(dbPath)
	cfg.Dimensions = 4
	db, err := Open(cfg, WithEmbedder(newKeywordEmbedder("runbook", "quorum", "replica", "partition")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	activateAviationSchema(t, db)

	ctx := context.Background()
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "rb-1",
		Title:       "Quorum",
		// Proper nouns, because the built-in extractor is what has to produce
		// an untyped entity for this test to be about anything.
		Content: "After a network partition heals, DRBD may refuse to reconnect and " +
			"report the connection state StandAlone. Recovery is a choice about which " +
			"divergence to discard. The LINSTOR controller records the outcome.",
		Collection: "runbooks",
	}); err != nil {
		t.Fatalf("an active ontology blocked knowledge ingestion: %v", err)
	}
}

// The exemption is for the library's own bookkeeping and must not become a way
// to smuggle an undeclared DOMAIN type past the schema.
func TestAnUndeclaredDomainTypeIsStillRefused(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "lto8", Type: "TapeLibrary", Metadata: map[string]string{"slots": "48"}},
	})
	if err == nil {
		t.Fatal("an undeclared object type was accepted")
	}
	if !strings.Contains(err.Error(), "TapeLibrary") {
		t.Fatalf("the refusal should name the type, got %v", err)
	}
}

// A schema that DOES declare a type named "entity" keeps its own meaning: the
// exemption is a fallback for untyped extraction, not an override.
func TestADeclaredEntityTypeIsStillValidated(t *testing.T) {
	db := openOntologyTestDB(t)
	schema := validAviationSchema()
	schema.ObjectTypes = append(schema.ObjectTypes, OntologyObjectType{
		APIName:    "entity",
		PrimaryKey: "key",
		Properties: []OntologyProperty{
			{APIName: "key", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
	})
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{
		Schema: schema, Activate: true,
	}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "x", Type: "entity", Metadata: map[string]string{"nope": "1"}},
	})
	if err == nil {
		t.Fatal("a declared entity type should still have its properties validated")
	}
}
