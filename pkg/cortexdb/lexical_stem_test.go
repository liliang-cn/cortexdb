package cortexdb

import (
	"context"
	"testing"
)

// "what does Alice own?" found nothing for "Alice owns Apollo and Apollo
// ships Friday.": the question's two words are alice and own, and the floor
// matched own as a whole word only, so the memory held one word of two and
// was dropped. "when does Apollo ship?" passed because ship is four letters
// and containsWord matches such a word as the start of "ships". A word and
// its inflections are one word wherever coverage is counted.
func TestAnInflectedFormOfAQuestionWordCoversIt(t *testing.T) {
	eachLexicalBackend(t, func(t *testing.T, db *DB) {
		ctx := context.Background()
		saveGlobalMemories(t, db, map[string]string{
			"apollo": "Alice owns Apollo and Apollo ships Friday.",
			"boxes":  "The movers packed twelve boxes for the companies running the depot.",
		})
		for _, query := range []string{
			"what does Alice own?",
			"what does Alice owns?",
			"who owned Apollo?",
			"when does Apollo ship?",
			"when was Apollo shipped?",
		} {
			resp, err := db.SearchMemory(ctx, MemorySearchRequest{Query: query, Scope: "global", TopK: 5})
			if err != nil {
				t.Fatalf("search %q: %v", query, err)
			}
			if got := memoryIDs(resp.Results); len(got) != 1 || got[0] != "apollo" {
				t.Errorf("memory search %q: results %v, want the Apollo memory", query, got)
			}
		}
		for _, query := range []string{"which company runs the depot?", "who packed the box?"} {
			resp, err := db.SearchMemory(ctx, MemorySearchRequest{Query: query, Scope: "global", TopK: 5})
			if err != nil {
				t.Fatalf("search %q: %v", query, err)
			}
			if got := memoryIDs(resp.Results); len(got) != 1 || got[0] != "boxes" {
				t.Errorf("memory search %q: results %v, want the movers memory", query, got)
			}
		}

		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "apollo", Content: "Alice owns Apollo and Apollo ships Friday.", Collection: "kb"}); err != nil {
			t.Fatalf("save knowledge: %v", err)
		}
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "pool", Content: "A thin pool that fills up fails every backup volume in it at once.", Collection: "kb"}); err != nil {
			t.Fatalf("save knowledge: %v", err)
		}
		for _, query := range []string{"what does Alice own?", "when was Apollo shipped?"} {
			resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: query, Collection: "kb", TopK: 3, RetrievalMode: RetrievalModeLexical})
			if err != nil {
				t.Fatalf("knowledge search %q: %v", query, err)
			}
			if len(resp.Results) != 1 || resp.Results[0].KnowledgeID != "apollo" {
				var ids []string
				for _, h := range resp.Results {
					ids = append(ids, h.KnowledgeID)
				}
				t.Errorf("knowledge search %q: results %v, want the Apollo document", query, ids)
			}
		}

		tools := db.GraphRAGTools()
		resp, err := tools.KnowledgeMemoryRecall(ctx, KnowledgeMemoryRecallRequest{Query: "what does Alice own?", Scope: "global", RetrievalMode: RetrievalModeLexical})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		if len(resp.Memories) == 0 {
			t.Errorf("knowledge-memory recall of %q returned no memory", "what does Alice own?")
		}
	})
}

// A question that shares one word with a row, in any of its forms, is still
// not answered by it.
func TestAnInflectedFormIsStillOneWord(t *testing.T) {
	eachLexicalBackend(t, func(t *testing.T, db *DB) {
		saveGlobalMemories(t, db, map[string]string{
			"apollo": "Alice owns Apollo and Apollo ships Friday.",
		})
		for _, query := range []string{"which boat did Bob own last summer?", "where did the shipping containers sail?"} {
			resp, err := db.SearchMemory(context.Background(), MemorySearchRequest{Query: query, Scope: "global", TopK: 5})
			if err != nil {
				t.Fatalf("search %q: %v", query, err)
			}
			if got := memoryIDs(resp.Results); len(got) != 0 {
				t.Errorf("search %q: results %v, want none", query, got)
			}
		}
	})
	// own and owns, typed together, are one word of the question.
	q := newRarityQuery("own owns", nil)
	if len(q.words) != 1 {
		t.Errorf("question words %v, want one", q.words)
	}
}

func TestStemConflatesInflections(t *testing.T) {
	for _, group := range [][]string{
		{"own", "owns", "owned", "owning"},
		{"ship", "ships", "shipped", "shipping"},
		{"box", "boxes"},
		{"company", "companies"},
		{"run", "running", "runs"},
		{"hope", "hoped", "hoping", "hopes"},
		{"control", "controlled", "controlling"},
		{"fly", "flies"},
	} {
		want := stemWord(group[0])
		for _, w := range group[1:] {
			if got := stemWord(w); got != want {
				t.Errorf("stem(%q) = %q, stem(%q) = %q; want the same", group[0], want, w, got)
			}
		}
	}
	for _, pair := range [][2]string{{"sing", "s"}, {"need", "ne"}, {"thing", "th"}, {"red", "r"}} {
		if stemWord(pair[0]) == pair[1] {
			t.Errorf("stem(%q) stripped a suffix with no vowel before it", pair[0])
		}
	}
	for _, w := range []string{"les", "des", "its", "uses", "news2"} {
		if got := stemWord(w); got != w {
			t.Errorf("stem(%q) = %q, want it unchanged", w, got)
		}
	}
	if got := stemWord("universität"); got != "universität" {
		t.Errorf("a non-ASCII word is stemmed: %q", got)
	}
}
