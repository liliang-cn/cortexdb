package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// Minimum relevance for keyword memory hits and for knowledge hits.
//
// A search that always fills topK answers every question, including the ones
// the store knows nothing about. Memory semantic search has had a floor for a
// while (DefaultMemorySemanticFloor); these are the rest.
//
// All three were set on a snapshot of the shared brain (3,439 memories, 149
// documents in 529 chunks, embeddinggemma 768-d): the 48 labelled memory
// questions and 7 negatives of the recall golden set, 18 knowledge questions
// written against its documents, and 10 more questions about nothing it holds.

// DefaultMemoryKeywordFloor is the share of a question, weighted by term
// rarity (see lexical_rarity.go), that a memory found by keyword search must
// cover to be returned. It also has to share more than one word of the
// question (rarityScorer.answers), which is what removes most noise: with it,
// no negative question returned a keyword hit except a memory that quotes the
// question word for word. The weakest genuine answer covered 0.325 of its
// question; 0.25 sits below that, and cost no labelled hit in either mode.
// What it does not stop is a memory that shares two of the question's words
// and none of its point — "How do I learn to play the piano?" covers 0.39 of
// a note about a language-learning app — and no single number would.
const DefaultMemoryKeywordFloor = 0.25

// DefaultKnowledgeVectorFloor is the cosine below which a knowledge chunk the
// vector search found is noise. The genuine answers' best chunks scored
// 0.434–0.738; the best chunk any of 17 unanswerable questions reached scored
// 0.287. Unrelated documents filling the list behind a genuine answer scored
// 0.24–0.40 (0.37–0.40 is what was seen filling it on the live brain), and
// 0.40 drops them. Related-but-wrong documents at 0.40–0.60 stay, as no
// cosine tells them apart. 0.35 and 0.45 lost no labelled hit either. Like
// the memory floor it is model-specific.
const DefaultKnowledgeVectorFloor = 0.40

// DefaultKnowledgeKeywordFloor is the rarity-weighted share of a question a
// knowledge chunk must cover to stand on keyword evidence, beside the same
// more-than-one-word rule as memory. The weakest genuine keyword answer
// covered 0.274 of its question; 0.30 lost it, 0.20 and 0.25 lost nothing.
const DefaultKnowledgeKeywordFloor = 0.25

// WithMemoryKeywordFloor sets the rarity-weighted coverage a memory found by
// keyword search must reach. 0 turns the floor off; a value < 0 or >= 1 keeps
// the default.
func WithMemoryKeywordFloor(floor float64) Option {
	return func(db *DB) {
		if floor >= 0 && floor < 1 {
			db.memoryKeywordFloor = floor
		}
	}
}

// WithKnowledgeFloors sets the minimum a knowledge chunk must reach to be
// returned: a cosine for the vector search, a rarity-weighted coverage of the
// question for keyword search. A chunk is kept when it reaches either. 0 turns
// that floor off; a value < 0 or >= 1 keeps its default.
func WithKnowledgeFloors(vector, keyword float64) Option {
	return func(db *DB) {
		if vector >= 0 && vector < 1 {
			db.knowledgeVectorFloor = vector
		}
		if keyword >= 0 && keyword < 1 {
			db.knowledgeKeywordFloor = keyword
		}
	}
}

// applyKnowledgeFloors drops the chunks of a knowledge result that no
// retriever has evidence for. A chunk stays when
//
//   - the vector search found it at a cosine of at least the vector floor,
//   - it covers at least the keyword floor of the question, terms weighted by
//     rarity among the chunks searched, and more than one of its words, or
//   - the caller asked for graph retrieval and the chunk is linked to an
//     entity the question names: that link is what graph mode returns.
//
// Below all three it is noise however high it ranked: a ranked list has a
// first place whether or not anything in it answers the question.
//
// The question's keywords, alternate queries and entity names count as
// synonyms (see newRarityQuery), so a chunk found through a supplied keyword
// is credited for it. A floor of 0 accepts whatever that retriever found.
func (db *DB) applyKnowledgeFloors(ctx context.Context, result *GraphRAGQueryResult, plan RetrievalPlan, decision RetrievalDecision, collection string, opts GraphRAGQueryOptions) error {
	if result == nil || len(result.Chunks) == 0 {
		return nil
	}
	vectorFloor, keywordFloor := db.knowledgeVectorFloor, db.knowledgeKeywordFloor
	if vectorFloor <= 0 && keywordFloor <= 0 {
		return nil
	}
	// The keyword score needs a scan of the chunks searched, so it is taken
	// only for a chunk the cheaper evidence did not already keep. The lexical
	// candidate search under this search may have taken it already.
	keyword := knowledgeKeywordFloorOf(ctx)
	if keyword == nil {
		keyword = db.newKnowledgeKeywordFloor(ctx, plan, collection)
	}
	byKeyword := func(chunk GraphRAGChunkResult) (bool, error) {
		if keywordFloor <= 0 {
			return chunk.VectorRank == 0 || chunk.LexicalRank > 0, nil
		}
		return keyword.passes(chunk.Content)
	}
	graphAsked := decision.RequestedMode == RetrievalModeGraph
	kept := result.Chunks[:0:0]
	for _, chunk := range result.Chunks {
		keep := chunk.VectorRank > 0 && (vectorFloor <= 0 || chunk.VectorScore >= vectorFloor)
		keep = keep || (graphAsked && namesQueryEntity(chunk.Entities, plan))
		if !keep {
			ok, err := byKeyword(chunk)
			if err != nil {
				return err
			}
			keep = ok
		}
		if keep {
			kept = append(kept, chunk)
		} else {
			// The cosine against the vector floor when the vector search found
			// the chunk; a chunk only the keyword search found failed the
			// keyword floor, whose score the scorer keeps to itself.
			if chunk.VectorRank > 0 {
				recallExplainerOf(ctx).dropped(explainKnowledge, dropKnowledgeFloor, chunk.ID, chunk.VectorScore, vectorFloor)
			} else {
				recallExplainerOf(ctx).dropped(explainKnowledge, dropKnowledgeKeywordFloor, chunk.ID, 0, keywordFloor)
			}
		}
	}
	if len(kept) == len(result.Chunks) {
		return nil
	}
	result.Chunks = kept
	result.Windows = nil
	result.Context = buildGraphRAGContext(kept)
	return db.widenGraphRAGContext(ctx, result, opts)
}

// knowledgeKeywordFloor is the keyword test of the knowledge floor for one
// search: whether a chunk covers the keyword floor of the question, terms
// weighted by rarity among the chunks searched, and more than one of its
// words. The scorer is built on first use, once, and shared by the lexical
// candidate search under SearchKnowledge (see keywordFloorFirst) and by
// applyKnowledgeFloors.
type knowledgeKeywordFloor struct {
	floor  float64
	once   sync.Once
	build  func() (*rarityScorer, error)
	scorer *rarityScorer
	err    error
}

// newKnowledgeKeywordFloor is the keyword floor of a search for plan over
// collection, or nil when the floor is off.
func (db *DB) newKnowledgeKeywordFloor(ctx context.Context, plan RetrievalPlan, collection string) *knowledgeKeywordFloor {
	if db.knowledgeKeywordFloor <= 0 {
		return nil
	}
	return &knowledgeKeywordFloor{floor: db.knowledgeKeywordFloor, build: func() (*rarityScorer, error) {
		synonyms := append(append(append([]string{}, plan.Keywords...), plan.AlternateQueries...), plan.EntityNames...)
		var variants variantWords
		if len(plan.AlternateQueries) > 0 {
			variants = db.chunkVariantWords(ctx, collection, plan.AlternateQueries)
		}
		// The words the keyword OR query searches for are credited first, so
		// the cap on synonyms does not leave out a word that found the row.
		q := newRarityQuery(plan.Query, append(lexicalORSynonyms(plan.Query, plan.Keywords, variants.kept), synonyms...))
		q.common = variants.commonBeside(append(append([]string{}, plan.Keywords...), plan.EntityNames...))
		return db.chunkRarityScorer(ctx, collection, q)
	}}
}

// passes reports whether content passes the keyword floor.
func (f *knowledgeKeywordFloor) passes(content string) (bool, error) {
	f.once.Do(func() { f.scorer, f.err = f.build() })
	if f.err != nil {
		return false, f.err
	}
	return f.scorer.answers(f.scorer.coverage(content), f.floor), nil
}

type knowledgeKeywordFloorKey struct{}

// withKnowledgeKeywordFloor hands floor to the lexical candidate search under
// ctx; a nil floor hands none.
func withKnowledgeKeywordFloor(ctx context.Context, floor *knowledgeKeywordFloor) context.Context {
	if floor == nil {
		return ctx
	}
	return context.WithValue(ctx, knowledgeKeywordFloorKey{}, floor)
}

func knowledgeKeywordFloorOf(ctx context.Context) *knowledgeKeywordFloor {
	floor, _ := ctx.Value(knowledgeKeywordFloorKey{}).(*knowledgeKeywordFloor)
	return floor
}

// keywordFloorFirst puts the rows that pass the keyword floor of the search
// ahead of those that do not, each in its order, before a list of more than
// topK rows is cut to topK: the floor applyKnowledgeFloors takes after the
// cut would otherwise leave fewer than topK, with passing rows below the cut
// unseen. The rows that fail are moved, not dropped: the vector search or a
// named entity may still keep one. Without a floor in ctx (a search other
// than SearchKnowledge, or the floor off) the list is unchanged; when the
// floor cannot be measured it is unchanged too, and applyKnowledgeFloors
// reports the error.
func keywordFloorFirst(ctx context.Context, rows []core.ScoredEmbedding, topK int) []core.ScoredEmbedding {
	floor := knowledgeKeywordFloorOf(ctx)
	if floor == nil || topK <= 0 || len(rows) <= topK {
		return rows
	}
	passed := make([]core.ScoredEmbedding, 0, len(rows))
	var failed []core.ScoredEmbedding
	for _, row := range rows {
		ok, err := floor.passes(row.Content)
		if err != nil {
			return rows
		}
		if ok {
			passed = append(passed, row)
		} else {
			failed = append(failed, row)
		}
	}
	return append(passed, failed...)
}

// namesQueryEntity reports whether one of a chunk's entities is one the
// question names: given as an entity name, or written in the question.
func namesQueryEntity(entities []string, plan RetrievalPlan) bool {
	query := strings.ToLower(plan.Query)
	for _, e := range entities {
		name := strings.ToLower(strings.TrimSpace(e))
		if name == "" {
			continue
		}
		if strings.Contains(query, name) {
			return true
		}
		for _, given := range plan.EntityNames {
			if strings.EqualFold(strings.TrimSpace(given), e) {
				return true
			}
		}
	}
	return false
}

// RelevanceFloorOptionsFromEnv reads the keyword and knowledge floors the
// binaries take from the environment: CORTEXDB_MEMORY_KEYWORD_FLOOR,
// CORTEXDB_KNOWLEDGE_VECTOR_FLOOR and CORTEXDB_KNOWLEDGE_KEYWORD_FLOOR, each
// a number in [0, 1). Unset keeps the default; 0 turns that floor off.
func RelevanceFloorOptionsFromEnv() ([]Option, error) {
	read := func(name string) (float64, bool, error) {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			return 0, false, nil
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f >= 1 {
			return 0, false, fmt.Errorf("invalid %s %q: want a number in [0, 1)", name, v)
		}
		return f, true, nil
	}
	var opts []Option
	if f, ok, err := read("CORTEXDB_MEMORY_KEYWORD_FLOOR"); err != nil {
		return nil, err
	} else if ok {
		opts = append(opts, WithMemoryKeywordFloor(f))
	}
	vector, vok, err := read("CORTEXDB_KNOWLEDGE_VECTOR_FLOOR")
	if err != nil {
		return nil, err
	}
	keyword, kok, err := read("CORTEXDB_KNOWLEDGE_KEYWORD_FLOOR")
	if err != nil {
		return nil, err
	}
	if vok || kok {
		if !vok {
			vector = -1 // keeps the default
		}
		if !kok {
			keyword = -1
		}
		opts = append(opts, WithKnowledgeFloors(vector, keyword))
	}
	return opts, nil
}
