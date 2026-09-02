package cortexdb

// Retrieval lanes that are not this database.
//
// The four built-in prefetch kinds all read the same file: vector, lexical and
// hybrid go to the store, graph walks the edges next to it. That is the point
// of CortexDB, and it is also a ceiling. A team already running Meilisearch, or
// a Weaviate cluster holding embeddings from a model this brain does not have,
// has recall that CortexDB cannot reach.
//
// This is the seam for that, and it is deliberately narrow: an external system
// **names candidates**, it does not supply them. It returns ids and scores; the
// content, metadata, collection and doc id all come from the brain, which stays
// the system of record. Three things follow, and each is a test:
//
//   - A source cannot inject text CortexDB never stored.
//   - An id the brain does not have is dropped rather than fabricated into a
//     result. Stale external indexes are normal, not exceptional.
//   - Whatever a source returns still passes the request's filter, and still
//     goes through fusion with the built-in lanes rather than around it.
//
// This is not a storage backend, and the distinction is the useful part. A
// storage backend has to hold documents, sessions, the graph tables and the
// ontology in one transaction — see core.BrainStore, which hands out a
// *sql.DB precisely because the graph and agentmem keep their own tables in it.
// A retrieval source has to answer one question: given this text, which ids
// look relevant? Weaviate and Meilisearch can answer that. Neither can be a
// brain.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// QuerySourceHit is one candidate: an id this brain should look at, and the
// score the external system gave it. Scores are only compared within a lane —
// fusion ranks them, so a Meilisearch relevance and a cosine similarity never
// have to mean the same thing.
type QuerySourceHit struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// QuerySourceRequest is what a lane is asked. It carries the same query
// material the lexical lane gets, so a source that can use keywords or
// alternate phrasings is not forced to re-derive them.
type QuerySourceRequest struct {
	Query            string   `json:"query,omitempty"`
	Collection       string   `json:"collection,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	AlternateQueries []string `json:"alternate_queries,omitempty"`
}

// QuerySource is an external retrieval lane.
//
// Implementations live outside this package — CortexDB depends on no search
// SDK, the same way it depends on no LLM SDK. examples/17_query_source has one
// written against Meilisearch's HTTP API with nothing but net/http.
type QuerySource interface {
	// Name identifies the lane. It is what a prefetch asks for, and what
	// appears in SourceRanks/SourceScores on every result the lane voted for.
	Name() string

	// Search returns candidates best-first. Returning an error fails the
	// query: a lane that vanishes quietly would change what retrieval means
	// without saying so.
	Search(ctx context.Context, req QuerySourceRequest) ([]QuerySourceHit, error)
}

// WithQuerySource registers an external retrieval lane under its own name.
// Registering the same name twice replaces the earlier one, which is what a
// caller reconfiguring a client means by it.
func WithQuerySource(src QuerySource) Option {
	return func(db *DB) {
		if src == nil {
			return
		}
		name := strings.TrimSpace(src.Name())
		if name == "" {
			return
		}
		if db.querySources == nil {
			db.querySources = map[string]QuerySource{}
		}
		db.querySources[name] = src
	}
}

// QuerySources lists the registered lanes, sorted. Useful in a health endpoint,
// and in the error a misnamed prefetch produces.
func (db *DB) QuerySources() []string {
	names := make([]string, 0, len(db.querySources))
	for name := range db.querySources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// runQuerySourcePrefetch asks one external lane, then hydrates what it named
// from this brain.
func (db *DB) runQuerySourcePrefetch(ctx context.Context, req QueryRequest, prefetch QueryPrefetch, limit int) ([]core.ScoredEmbedding, error) {
	name := strings.TrimSpace(prefetch.Source)
	if name == "" {
		return nil, fmt.Errorf("query prefetch kind %q requires a source name (have: %v)", QueryPrefetchSource, db.QuerySources())
	}
	src, ok := db.querySources[name]
	if !ok {
		return nil, fmt.Errorf("no query source %q registered (have: %v)", name, db.QuerySources())
	}

	hits, err := src.Search(ctx, QuerySourceRequest{
		Query:            firstNonEmpty(prefetch.Query, req.Query),
		Collection:       req.Collection,
		Limit:            limit,
		Keywords:         prefetch.Keywords,
		AlternateQueries: prefetch.AlternateQueries,
	})
	if err != nil {
		return nil, fmt.Errorf("query source %q: %w", name, err)
	}

	results := make([]core.ScoredEmbedding, 0, len(hits))
	for _, hit := range hits {
		id := strings.TrimSpace(hit.ID)
		if id == "" {
			continue
		}
		emb, err := db.store.GetByID(ctx, id)
		// A miss is the expected case for a stale external index, not an
		// error: the row was deleted here and the other system has not caught
		// up. Dropping it is the only honest answer — there is no content to
		// show, and nothing to authorize.
		if err != nil || emb == nil {
			continue
		}
		if req.Collection != "" && emb.Collection != "" && emb.Collection != req.Collection {
			continue
		}
		results = append(results, core.ScoredEmbedding{Embedding: *emb, Score: hit.Score})
	}
	return results, nil
}
