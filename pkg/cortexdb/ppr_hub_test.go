package cortexdb

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// saveHubStore writes 100 passages: 30 name "Hubbert", as a speaker is named
// in every turn of a conversation, one of those also names "Zelkova", and 70
// name no one.
func saveHubStore(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		content := fmt.Sprintf("the harbour ledger records crate %d and cargo %d", i, i*7)
		if i < 30 {
			content = fmt.Sprintf("Hubbert logged crate %d and cargo %d at the harbour", i, i*7)
		}
		if i == 29 {
			content = "Hubbert met Zelkova at the harbour gate"
		}
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: fmt.Sprintf("p%03d", i), Content: content}); err != nil {
			t.Fatalf("save p%03d: %v", i, err)
		}
	}
}

// An entity three passages in ten mention is not one a walk can start from:
// the walk would spread over most of the store.
func TestAnEntityMostPassagesMentionIsNotASeed(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			saveHubStore(t, db)
			opts := (&PPRRetrievalOptions{}).resolved()
			_, entities, err := db.pprSeeds(ctx, "Did Hubbert meet Zelkova?", nil, "chunk", nil, nil, opts)
			if err != nil {
				t.Fatalf("pprSeeds: %v", err)
			}
			if want := []string{EntityNodeID("Zelkova")}; !reflect.DeepEqual(entities, want) {
				t.Fatalf("seed entities = %v, want only %v", entities, want)
			}
		})
	}
}

// When the only entity a question names is a hub, auto returns lexical
// search's ranking unchanged.
func TestAutoKeepsTheLexicalRankingWhenTheQuestionNamesOnlyAHub(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			saveHubStore(t, db)
			search := func(mode string) ([]string, string) {
				res, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "Which crate did Hubbert log as cargo 63?", TopK: 5, RetrievalMode: mode})
				if err != nil {
					t.Fatalf("search %s: %v", mode, err)
				}
				var out []string
				for _, h := range res.Results {
					out = append(out, h.KnowledgeID)
				}
				return out, res.Decision.EffectiveMode
			}
			lexical, _ := search(RetrievalModeLexical)
			auto, mode := search(RetrievalModeAuto)
			if len(lexical) == 0 {
				t.Fatal("lexical search found nothing")
			}
			if mode != RetrievalModeLexical || !reflect.DeepEqual(auto, lexical) {
				t.Fatalf("auto (%s) ranked %v, lexical %v", mode, auto, lexical)
			}
		})
	}
}
