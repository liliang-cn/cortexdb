package main

import (
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// The auto-recall line of a memory that replaced another says when it
// changed and what it said before; one that replaced nothing reads as before.
func TestARecalledChangedFactSaysWhatItReplaced(t *testing.T) {
	resp := &cortexdb.KnowledgeMemoryRecallResponse{Memories: []cortexdb.MemorySearchHit{
		{Memory: cortexdb.MemoryRecord{ID: "port-v2", Content: "The dev server listens on port 6759."},
			Replaces: []cortexdb.MemoryReplaced{{ID: "port-v1", Excerpt: "The dev server listens on port 4173.",
				SupersededAt: time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC), SupersededBy: "port-v2"}}},
		{Memory: cortexdb.MemoryRecord{ID: "plain", Content: "lunch at noon"}},
	}}
	out := formatRecallHits(hitsFromRecall(resp))
	if !strings.Contains(out, "The dev server listens on port 6759. (changed 2026-10-02; before: The dev server listens on port 4173.)") {
		t.Errorf("block lacks the change:\n%s", out)
	}
	if strings.Contains(out, "lunch at noon (changed") {
		t.Errorf("an unchanged hit says it changed:\n%s", out)
	}
}
