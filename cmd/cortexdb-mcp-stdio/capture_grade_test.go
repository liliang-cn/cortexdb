package main

import (
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// A captured fact keeps the grade the model gave it when the store will take
// it: verified with the check named, asserted for a guess. A claim of
// verified that names no check is a claim nobody can point to, saved as
// asserted rather than lost; a preference or a decision is not graded.
func TestACapturedFactKeepsAGradeTheStoreTakes(t *testing.T) {
	for _, c := range []struct {
		fact  capturedFact
		grade string
		why   string
	}{
		{capturedFact{Type: "lesson", Grade: "verified", GradeWhy: "go test ./pkg/x went red without the fix"}, cortexdb.GradeVerified, "go test ./pkg/x went red without the fix"},
		{capturedFact{Type: "fact", Grade: "Verified"}, cortexdb.GradeAsserted, ""},
		{capturedFact{Type: "fact", Grade: "asserted"}, cortexdb.GradeAsserted, ""},
		{capturedFact{Type: "fact", Grade: "probably"}, "", ""},
		{capturedFact{Type: "preference", Grade: "verified", GradeWhy: "said so"}, "", ""},
		{capturedFact{Type: "decision", Grade: "asserted"}, "", ""},
	} {
		c.fact.Slug, c.fact.Content = "s", "content"
		req := capturedRequest("11111111-session", "2026-10-27", c.fact)
		if req.Grade != c.grade || req.GradeWhy != c.why {
			t.Errorf("%+v: got %q %q, want %q %q", c.fact, req.Grade, req.GradeWhy, c.grade, c.why)
		}
	}
}

// A recalled guess says so first in its bracket, and a verified hit names
// its check; an untagged one reads as before.
func TestARecalledGuessSaysItIsUnverified(t *testing.T) {
	resp := &cortexdb.KnowledgeMemoryRecallResponse{Memories: []cortexdb.MemorySearchHit{
		{Memory: cortexdb.MemoryRecord{ID: "guess", Content: "the switch probably dropped quorum", Grade: cortexdb.GradeAsserted}},
		{Memory: cortexdb.MemoryRecord{ID: "fix", Content: "the fix holds", Grade: cortexdb.GradeVerified, GradeWhy: "failover drill passed"}},
		{Memory: cortexdb.MemoryRecord{ID: "plain", Content: "lunch at noon"}},
	}}
	out := formatRecallHits(hitsFromRecall(resp))
	for _, want := range []string{
		"- guess [UNVERIFIED (asserted)",
		"- fix [verified: failover drill passed",
		"- plain [",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("block lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "- plain [UNVERIFIED") || strings.Contains(out, "- plain [verified") {
		t.Errorf("an untagged hit was graded:\n%s", out)
	}
}
