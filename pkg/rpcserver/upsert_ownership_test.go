package rpcserver

import (
	"testing"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// An upsert names an id, and on a shared brain ids are guessable strings. These
// tests exist because the hole was real and demonstrated against a running
// server before the guard existed: a key confined to user_id="hermes" saved a
// memory using zeus's id, and the row came back owned by hermes with hermes's
// content. The theft covered its own tracks — the owner check that would have
// caught it reads the field the write had already overwritten.

func TestAConfinedKeyCannotOverwriteAnothersMemoryByGuessingItsID(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	client := rpcv1.NewMemoryServiceClient(conn)

	if _, err := client.SaveMemory(asKey("op-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "z-secret", UserId: "zeus", Scope: "user", Content: "Thunderbolts, second drawer.",
	}); err != nil {
		t.Fatalf("operator save: %v", err)
	}

	if _, err := client.SaveMemory(asKey("hermes-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "z-secret", UserId: "hermes", Scope: "user", Content: "OVERWRITTEN",
	}); err == nil {
		t.Fatal("a confined key overwrote another user's memory by naming its id")
	}

	// The row must be untouched — both its content and, just as important, its
	// owner. An upsert that re-homed it would leave hermes holding zeus's row.
	got, err := client.GetMemory(asKey("op-secret"), &rpcv1.GetMemoryRequest{MemoryId: "z-secret"})
	if err != nil {
		t.Fatalf("operator lost the row: %v", err)
	}
	if got.GetMemory().GetContent() != "Thunderbolts, second drawer." {
		t.Fatalf("content was overwritten: %q", got.GetMemory().GetContent())
	}
	if got.GetMemory().GetUserId() != "zeus" {
		t.Fatalf("the row was re-homed to %q", got.GetMemory().GetUserId())
	}
}

func TestAConfinedKeyStillCreatesNewMemoriesFreely(t *testing.T) {
	// The guard must not turn every save into a lookup-and-refuse: an id that
	// names nothing is a create, which is the common case.
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	client := rpcv1.NewMemoryServiceClient(conn)

	if _, err := client.SaveMemory(asKey("hermes-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "brand-new", UserId: "hermes", Scope: "user", Content: "First writing.",
	}); err != nil {
		t.Fatalf("create refused: %v", err)
	}
	// And updating its own row again still works.
	if _, err := client.SaveMemory(asKey("hermes-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "brand-new", UserId: "hermes", Scope: "user", Content: "Second writing.",
	}); err != nil {
		t.Fatalf("hermes was refused its own row on re-save: %v", err)
	}
}

func TestAConfinedKeyCannotStealADocumentByUpdatingItsCollection(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	client := rpcv1.NewKnowledgeServiceClient(conn)

	if _, err := client.SaveKnowledge(asKey("op-secret"), &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k-private", Title: "Private", Content: "Not for notes.",
		Collection: "vault",
	}); err != nil {
		t.Fatalf("operator save: %v", err)
	}

	moved := "notes"
	if _, err := client.UpdateKnowledge(asKey("notes-secret"), &rpcv1.UpdateKnowledgeRequest{
		KnowledgeId: "k-private", Collection: &moved,
	}); err == nil {
		t.Fatal("a collection-confined key moved another collection's document into its own")
	}
	if _, err := client.SaveKnowledge(asKey("notes-secret"), &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k-private", Title: "Mine now", Content: "Taken.", Collection: "notes",
	}); err == nil {
		t.Fatal("a collection-confined key overwrote another collection's document by id")
	}

	got, err := client.GetKnowledge(asKey("op-secret"), &rpcv1.GetKnowledgeRequest{KnowledgeId: "k-private"})
	if err != nil {
		t.Fatalf("operator lost the document: %v", err)
	}
	if got.GetKnowledge().GetContent() != "Not for notes." {
		t.Fatalf("content was overwritten: %q", got.GetKnowledge().GetContent())
	}
}

func TestAnUnconfinedKeyUpsertsAnythingExactlyAsBefore(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	client := rpcv1.NewMemoryServiceClient(conn)

	if _, err := client.SaveMemory(asKey("op-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "shared", UserId: "zeus", Scope: "user", Content: "One.",
	}); err != nil {
		t.Fatalf("operator save: %v", err)
	}
	if _, err := client.SaveMemory(asKey("op-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "shared", UserId: "hermes", Scope: "user", Content: "Two.",
	}); err != nil {
		t.Fatalf("an unconfined key must still be able to re-home a row: %v", err)
	}
}
