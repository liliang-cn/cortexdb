package cortexdb

import (
	"context"
	"testing"
	"time"
)

// On PostgreSQL every write and delete of a document id waits for the lock a
// conditional write holds from its check to its write, so neither an
// unconditional ingest nor a delete lands in between: the transaction held
// here stands for a conditional write that has checked and not yet written.
// It then writes the document's chunks, as a real writer does: a writer that
// took the id lock only after touching those rows would deadlock with it.
func TestEveryWriterOfAnIDWaitsForAConditionalWriteOnPostgres(t *testing.T) {
	db := openPostgresBrain(t, 4)
	tools := db.GraphRAGTools()
	ctx := context.Background()
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "Version one."}); err != nil {
		t.Fatalf("first: %v", err)
	}
	cases := []struct {
		name  string
		write func() error
	}{
		{"an unconditional ingest", func() error {
			_, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "Written in the meantime."})
			return err
		}},
		{"a delete", func() error {
			_, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "d1"})
			return err
		}},
	}
	for _, c := range cases {
		held, err := db.SQL().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := held.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1, hashtext($2))`, documentLockSpace, "d1"); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- c.write() }()
		select {
		case err := <-done:
			_ = held.Rollback()
			t.Fatalf("%s did not wait for the conditional write (err %v)", c.name, err)
		case <-time.After(300 * time.Millisecond):
		}
		if _, err := held.ExecContext(ctx, `UPDATE embeddings SET doc_id = doc_id WHERE doc_id = $1`, "d1"); err != nil {
			_ = held.Rollback()
			t.Fatalf("the conditional write could not write its chunks while %s waited: %v", c.name, err)
		}
		_ = held.Rollback()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s after the lock: %v", c.name, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("%s never finished", c.name)
		}
	}
}
