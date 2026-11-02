package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// if_absent writes only a document id the brain does not have: a client
// that checks something before it writes a new document (a near copy, a
// name taken) is not overtaken by another write of the same id. A refused
// write changes nothing, even when the request says what is stored.
func TestIfAbsentRefusesAnIDTheBrainHas(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "absent")
	req := ToolIngestDocumentRequest{DocumentID: "d1", Title: "One", Content: "The ledger moves on Sunday."}
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Title: "One", Content: "The ledger moves on Sunday.", IfAbsent: true}); err != nil {
		t.Fatalf("a new id with if_absent: %v", err)
	}
	req.Content = "The ledger moves on Monday."
	req.IfAbsent = true
	if _, err := tools.IngestDocument(ctx, req); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("an id the brain has, with if_absent: %v, want ErrPreconditionFailed", err)
	}
	if doc, _ := db.store.GetDocument(ctx, "d1"); doc.Content != "The ledger moves on Sunday." {
		t.Fatalf("a refused write changed the document: %q", doc.Content)
	}
	// The same text again is not "unchanged" under if_absent: the id is taken.
	req.Content = "The ledger moves on Sunday."
	if _, err := tools.IngestDocument(ctx, req); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("an identical re-ingest with if_absent: %v, want ErrPreconditionFailed", err)
	}
}

// if_sha256 writes only over the content the caller read: the write names
// the content hash it read (_content_sha256, also content_sha256 in the
// response), and a document rewritten since is not overwritten.
func TestIfSHA256WritesOnlyOverTheContentItRead(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "sha")
	first, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "Version one."})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "Version two.", IfSHA256: first.ContentSHA256})
	if err != nil {
		t.Fatalf("over the content read: %v", err)
	}
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "Version three.", IfSHA256: first.ContentSHA256}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("over a content rewritten since: %v, want ErrPreconditionFailed", err)
	}
	if doc, _ := db.store.GetDocument(ctx, "d1"); doc.Content != "Version two." || doc.Metadata[contentHashKey] != second.ContentSHA256 {
		t.Fatalf("a refused write changed the document: %q", doc.Content)
	}
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d2", Content: "Never stored.", IfSHA256: first.ContentSHA256}); !errors.Is(err, ErrPreconditionFailed) || !strings.Contains(err.Error(), "is not stored") {
		t.Fatalf("a missing id with if_sha256: %v, want ErrPreconditionFailed", err)
	}
}

// The condition is checked inside the write transaction, once the write
// lock is held: of conditional writes of one id that race, exactly one
// lands.
func TestConditionalIngestsThatRaceWriteOnce(t *testing.T) {
	_, tools, _ := openIngestBrain(t, "race")
	racingIfAbsent(t, tools)
}

// On PostgreSQL an advisory lock on the id makes the conditional writers of
// one id wait for each other.
func TestConditionalIngestsThatRaceWriteOnceOnPostgres(t *testing.T) {
	racingIfAbsent(t, openPostgresBrain(t, 4).GraphRAGTools())
}

func racingIfAbsent(t *testing.T, tools *GraphRAGToolbox) {
	t.Helper()
	ctx := context.Background()
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: fmt.Sprintf("Writer %d was first.", i), IfAbsent: true})
		}(i)
	}
	wg.Wait()
	landed := 0
	for _, err := range errs {
		switch {
		case err == nil:
			landed++
		case !errors.Is(err, ErrPreconditionFailed):
			t.Fatalf("a racing write failed otherwise: %v", err)
		}
	}
	if landed != 1 {
		t.Fatalf("%d racing if_absent writes landed, want 1", landed)
	}
}

// A condition that cannot be read refuses the write: the check in the
// transaction reports the failure rather than taking the id as free.
func TestAnUnreadableConditionRowFailsTheWrite(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "condition-unreadable")
	if _, err := db.SQL().Exec(`ALTER TABLE documents RENAME COLUMN metadata TO metadata_hidden`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.store.GetDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := db.checkIngestConditionTx(ctx, tx, "d1", &ingestCondition{absent: true}); err == nil || !strings.Contains(err.Error(), "read the condition of document") {
		t.Fatalf("an unreadable condition: %v", err)
	}
}

// Of if_sha256 writes that race over the one hash they all read, exactly
// one lands: the second sees the first's content in the transaction, even
// though every one passed the check made before the transaction.
func TestIfSHA256WritesThatRaceWriteOnce(t *testing.T) {
	_, tools, _ := openIngestBrain(t, "race-sha")
	racingIfSHA256(t, tools)
}

func TestIfSHA256WritesThatRaceWriteOnceOnPostgres(t *testing.T) {
	racingIfSHA256(t, openPostgresBrain(t, 4).GraphRAGTools())
}

func racingIfSHA256(t *testing.T, tools *GraphRAGToolbox) {
	t.Helper()
	ctx := context.Background()
	first, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "Version read by all."})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: fmt.Sprintf("Writer %d rewrote it.", i), IfSHA256: first.ContentSHA256})
		}(i)
	}
	wg.Wait()
	landed := 0
	for _, err := range errs {
		switch {
		case err == nil:
			landed++
		case !errors.Is(err, ErrPreconditionFailed):
			t.Fatalf("a racing write failed otherwise: %v", err)
		}
	}
	if landed != 1 {
		t.Fatalf("%d racing if_sha256 writes landed, want 1", landed)
	}
}

// A document stored without a content hash (written by another route, or
// before the hash was kept) is a document: if_absent refuses its id, and no
// hash matches it under if_sha256. The refusal names the stored hash.
func TestADocumentWithoutAContentHashIsStillStored(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "no-hash")
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "Written before hashes."}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`UPDATE documents SET metadata = '{}' WHERE id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.store.GetDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := db.checkIngestConditionTx(ctx, tx, "d1", &ingestCondition{absent: true}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("if_absent over a document without a hash: %v, want ErrPreconditionFailed", err)
	}
	if err := db.checkIngestConditionTx(ctx, tx, "d1", &ingestCondition{sha256: "0000"}); !errors.Is(err, ErrPreconditionFailed) || !strings.Contains(err.Error(), `stored with content ""`) {
		t.Fatalf("if_sha256 over a document without a hash: %v, want a refusal naming the empty stored hash", err)
	}
	_ = tx.Rollback()
	resp, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d2", Content: "Hashed."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d2", Content: "Again.", IfSHA256: "0000"}); err == nil || !strings.Contains(err.Error(), resp.ContentSHA256) {
		t.Fatalf("a refusal under if_sha256: %v, want it to name the stored hash %s", err, resp.ContentSHA256)
	}
}

// Both conditions together are the caller's mistake, named by a sentinel.
func TestBothConditionsTogetherAreRefusedBySentinel(t *testing.T) {
	_, tools, ctx := openIngestBrain(t, "both")
	_, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "Both.", IfAbsent: true, IfSHA256: "0000"})
	if !errors.Is(err, ErrConflictingConditions) {
		t.Fatalf("if_absent with if_sha256: %v, want ErrConflictingConditions", err)
	}
}
