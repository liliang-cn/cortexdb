package cortexdb

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
)

// rerankReference is Rerank as it was before each text was tokenized once:
// redundancy recomputed against every selected item, both sides tokenized
// inside the loop. It is kept as the oracle for the faster version, which must
// return the same items, in the same order, with the same scores.
func rerankReference(query string, items []RerankItem, opts RerankOptions) []RerankItem {
	if len(items) == 0 {
		return nil
	}
	opts.withDefaults()
	queryTerms := tokenSet(query)
	queryEntities := tokenSet(strings.Join(extractEntityNames(extractTitleEntities(query)), " "))
	normalized := normalizeScores(items)
	scored := make([]RerankItem, len(items))
	for i, it := range items {
		it.RerankScore = normalized[i]*opts.BaseWeight +
			overlapScore(queryTerms, tokenSet(it.Text))*opts.TermWeight +
			overlapScore(queryEntities, tokenSet(strings.Join(it.Entities, " ")))*opts.EntityWeight
		scored[i] = it
	}
	limit := opts.TopN
	if limit <= 0 || limit > len(scored) {
		limit = len(scored)
	}
	selected := make([]RerankItem, 0, limit)
	remaining := append([]RerankItem(nil), scored...)
	for len(remaining) > 0 && len(selected) < limit {
		bestIdx, bestScore := 0, -math.MaxFloat64
		for i := range remaining {
			worst := 0.0
			for _, s := range selected {
				score := overlapScore(tokenSet(remaining[i].Text), tokenSet(s.Text))
				if remaining[i].GroupKey != "" && remaining[i].GroupKey == s.GroupKey {
					score = math.Max(score, 0.85)
				}
				worst = math.Max(worst, score)
			}
			if score := opts.DiversityLambda*remaining[i].RerankScore - (1-opts.DiversityLambda)*worst; score > bestScore {
				bestScore, bestIdx = score, i
			}
		}
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}
	return selected
}

func randomRerankItems(r *rand.Rand, n int) []RerankItem {
	vocab := []string{"raft", "leader", "log", "replica", "Apollo", "Alice", "ships", "friday", "disk", "quorum", "node", "primary", "backup", "vault"}
	items := make([]RerankItem, n)
	for i := range items {
		var words []string
		for w := 0; w < 3+r.Intn(12); w++ {
			words = append(words, vocab[r.Intn(len(vocab))])
		}
		items[i] = RerankItem{
			ID:   fmt.Sprintf("c%d", i),
			Text: strings.Join(words, " "),
			// Few distinct scores, so ties are common and the tie-break is
			// exercised rather than assumed.
			Score:    float64(r.Intn(4)),
			Entities: []string{vocab[r.Intn(len(vocab))]},
			GroupKey: fmt.Sprintf("doc%d", r.Intn(n/3+1)),
		}
	}
	return items
}

func TestRerankReturnsWhatTheFullRecomputationReturned(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 400; trial++ {
		items := randomRerankItems(r, 1+r.Intn(40))
		opts := RerankOptions{TopN: r.Intn(len(items) + 2), DiversityLambda: []float64{0, 0.3, 0.75, 1}[r.Intn(4)]}
		query := "Who is the raft leader for Apollo quorum"
		want := rerankReference(query, append([]RerankItem(nil), items...), opts)
		got := Rerank(query, append([]RerankItem(nil), items...), opts)
		if len(got) != len(want) {
			t.Fatalf("trial %d: %d results, want %d", trial, len(got), len(want))
		}
		for i := range want {
			if got[i].ID != want[i].ID || got[i].RerankScore != want[i].RerankScore {
				t.Fatalf("trial %d, rank %d: got %s (%.6f), want %s (%.6f)", trial, i, got[i].ID, got[i].RerankScore, want[i].ID, want[i].RerankScore)
			}
		}
	}
}

func BenchmarkRerankMMR(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	for _, n := range []int{20, 50, 200} {
		items := randomRerankItems(r, n)
		for i := range items {
			// Chunk-sized texts: the cost being measured is tokenization.
			items[i].Text = strings.Repeat(items[i].Text+" ", 40)
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for b.Loop() {
				Rerank("raft leader Apollo", items, RerankOptions{TopN: n})
			}
		})
		b.Run(fmt.Sprintf("reference/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				rerankReference("raft leader Apollo", items, RerankOptions{TopN: n})
			}
		})
	}
}
