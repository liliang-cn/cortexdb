package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures are assembled at run time so no line of this file has the shape of
// a credential: a scanner reading the repository should find none here.
func fakeKey(prefix string, n int) string { return prefix + strings.Repeat("a1B2", n/4) }

func TestRedactCredentialsTakesIssuedShapesAndLeavesTheRest(t *testing.T) {
	provider := fakeKey("sk"+"-", 32)
	proxy := fakeKey("sk"+"-cpa-", 32)
	github := fakeKey("gh"+"p_", 36)
	bearer := "Authorization: Bearer " + strings.Repeat("z9", 16)
	text := "key " + provider + " and " + proxy + ", token " + github + "; " + bearer +
		"\ncommit 1a71299c0ffee1a71299c0ffee1a71299c0ffee12, id 68c43534-61d2-4cf9-b67f-dfdc46886a9e, max_tokens: 100000, task-notification"

	got, n := RedactCredentials(text)
	if n != 4 {
		t.Fatalf("redacted %d, want 4: %s", n, got)
	}
	for _, secret := range []string{provider, proxy, github, strings.Repeat("z9", 16)} {
		if strings.Contains(got, secret) {
			t.Errorf("still holds %q: %s", secret, got)
		}
	}
	for _, kept := range []string{"1a71299c0ffee1a71299c0ffee1a71299c0ffee12", "68c43534-61d2-4cf9-b67f-dfdc46886a9e", "max_tokens: 100000", "task-notification"} {
		if !strings.Contains(got, kept) {
			t.Errorf("lost %q: %s", kept, got)
		}
	}
}

// A key pasted into a memory or a knowledge item is not stored, on save or on
// update, and the response says how many were taken out.
func TestSavedTextDoesNotKeepCredentials(t *testing.T) {
	ctx := context.Background()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "c.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := fakeKey("sk"+"-cpa-", 32)

	m, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m1", Scope: "global", Content: "the proxy key is " + key + " on linode"})
	if err != nil {
		t.Fatal(err)
	}
	if m.RedactedSecrets != 1 || strings.Contains(m.Memory.Content, key) || !strings.Contains(m.Memory.Content, "the proxy key is "+RedactedCredential+" on linode") {
		t.Fatalf("saved memory: %d %q", m.RedactedSecrets, m.Memory.Content)
	}
	next := "rotated to " + key
	u, err := db.UpdateMemory(ctx, MemoryUpdateRequest{MemoryID: "m1", Content: &next})
	if err != nil {
		t.Fatal(err)
	}
	if u.RedactedSecrets != 1 || strings.Contains(u.Memory.Content, key) {
		t.Fatalf("updated memory: %d %q", u.RedactedSecrets, u.Memory.Content)
	}

	k, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Title: "proxy " + key, Content: "export KEY=" + key + "\nmore text about the proxy"})
	if err != nil {
		t.Fatal(err)
	}
	if k.RedactedSecrets != 2 || strings.Contains(k.Knowledge.Content, key) || strings.Contains(k.Knowledge.Title, key) {
		t.Fatalf("saved knowledge: %d %q %q", k.RedactedSecrets, k.Knowledge.Title, k.Knowledge.Content)
	}
	ku, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "k1", Content: &next})
	if err != nil {
		t.Fatal(err)
	}
	if ku.RedactedSecrets != 1 || strings.Contains(ku.Knowledge.Content, key) {
		t.Fatalf("updated knowledge: %d %q", ku.RedactedSecrets, ku.Knowledge.Content)
	}
	// Nothing the search reads kept it either.
	hits, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "proxy", Keywords: []string{"proxy", "rotated"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits.Results {
		if strings.Contains(h.Title, key) || strings.Contains(h.Snippet, key) {
			t.Fatalf("a search hit holds the key: %+v", h)
		}
	}
}

func TestAStoreMeantToHoldCredentialsKeepsThem(t *testing.T) {
	ctx := context.Background()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "c.db")), WithoutCredentialRedaction())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := fakeKey("sk"+"-", 32)
	m, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m1", Scope: "global", Content: "key " + key})
	if err != nil {
		t.Fatal(err)
	}
	if m.RedactedSecrets != 0 || !strings.Contains(m.Memory.Content, key) {
		t.Fatalf("kept store: %d %q", m.RedactedSecrets, m.Memory.Content)
	}
}
