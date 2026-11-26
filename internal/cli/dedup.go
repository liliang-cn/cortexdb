package cli

import (
	"context"
	"strings"
	"unicode"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// A capture distils each session on its own, so the same standing fact — "the
// user sets up Python with uv" — comes out of every session that touches it,
// each under that session's own id. Superseding (capture_supersede.go) asks a
// model which memories a fact makes untrue; a fact that repeats an existing
// one word for word is not a contradiction, and saving it again only makes
// recall return the same line several times.
//
// This check needs no model and no embedder: a fact whose words, ignoring
// case, punctuation and spacing, are those of a current memory under another
// id is not saved again.

// normalizedFact is the comparable form of a memory's text.
func normalizedFact(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

// duplicateOf is the id of a current memory, other than req's own, that says
// what req says; "" when there is none or the lookup fails.
func duplicateOf(ctx context.Context, brain brainClient, req cortexdb.MemorySaveRequest) string {
	key := normalizedFact(req.Content)
	if key == "" {
		return ""
	}
	hits, err := brain.SearchMemory(ctx, cortexdb.MemorySearchRequest{
		Query:         req.Content,
		Scope:         req.Scope,
		TopK:          5,
		RetrievalMode: cortexdb.RetrievalModeLexical,
	})
	if err != nil {
		return ""
	}
	for _, hit := range hits {
		m := hit.Memory
		if m.ID == req.MemoryID {
			continue
		}
		if s, _ := m.Metadata["superseded_by"].(string); strings.TrimSpace(s) != "" {
			continue
		}
		if normalizedFact(m.Content) == key {
			return m.ID
		}
	}
	return ""
}
