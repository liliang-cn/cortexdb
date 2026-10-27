package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	"log"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// DefaultMemorySemanticFloor is the cosine similarity below which a semantic
// memory hit is noise. A vector search has a nearest neighbour to every query,
// including queries the store holds nothing about; the floor is what lets it
// answer "nothing relevant".
//
// Calibrated on embeddinggemma (768-dim) over two stores. A live one of ~2k
// memories: unrelated probes (weather, cooking, novels) peaked at 0.263, the
// weakest genuine hit scored 0.311. A 43-memory store built from five days of
// a simulated user's chat: nine unrelated probes (weather, recipes, football,
// Kubernetes…) topped out at 0.163–0.307 — "今天天气怎么样" reached 0.307 on a
// note about New Zealand's winter roads — while twenty paraphrased questions
// found their answer as the top hit at 0.342–0.677, and the noise trailing
// genuine answers sat at 0.24–0.29. 0.28 let the 0.28–0.31 band through;
// 0.31 drops it and keeps every genuine top hit seen. It also drops one
// genuine second-best hit, at 0.282 — keyword search is what still finds such
// a memory when it shares the words asked about.
//
// It is a floor for noise, not a relevance oracle: one unrelated probe scored
// 0.331 against a store that held nothing on its subject, above the weakest
// genuine top hit's neighbours, and no single number separates those. Another
// embedding model has another scale; set the floor with
// WithMemorySemanticFloor (CORTEXDB_MEMORY_SEMANTIC_FLOOR in the binaries).
const DefaultMemorySemanticFloor = 0.31

// WithMemorySemanticFloor sets the cosine similarity below which semantic
// memory hits are dropped. Keyword matches are never subject to it: a memory
// that contains the words asked about is still returned. A value <= 0 or >= 1
// keeps the default.
func WithMemorySemanticFloor(floor float64) Option {
	return func(db *DB) {
		if floor > 0 && floor < 1 {
			db.memoryFloor = floor
		}
	}
}

// semanticFloor is the floor in force: the default for a DB not built by Open.
func (db *DB) semanticFloor() float64 {
	if db.memoryFloor <= 0 {
		return DefaultMemorySemanticFloor
	}
	return db.memoryFloor
}

// bareKeywordFloorMargin raises the semantic floor for a one-word query.
//
// The embedding of a single word means little, and its nearest memories are
// the shortest ones whatever the word is. On the shared brain (2,786
// memories, embeddinggemma) the user's city, seven characters, and the user's
// name cleared 0.31 for three of every four queries, and filled the results
// of 48 of 60 one-word queries after the memories that contain the word.
// Across those queries the two scored 0.39 at the median and 0.46 at the 90th
// percentile, while a memory that does not contain the word but means it —
// 在持美股 for the user's VOO and OKLO holdings, linting for a lint bug,
// restriction for a hard constraint — scored 0.47–0.65. At floor + 0.16
// (0.47) they reached 5 of the 60 and those 13 fills stayed. The margin is
// added to the floor in force, so a floor set for another model moves it too.
const bareKeywordFloorMargin = 0.16

// memoryFloorFor is the semantic floor for this query.
func (db *DB) memoryFloorFor(query string) float64 {
	if isBareKeyword(query) {
		return db.semanticFloor() + bareKeywordFloorMargin
	}
	return db.semanticFloor()
}

type memoryRow struct {
	record MemoryRecord
	vector []byte
}

// SaveMemory stores a memory record in a resolved memory bucket.
func (db *DB) SaveMemory(ctx context.Context, req MemorySaveRequest) (*MemorySaveResponse, error) {
	if req.MemoryID == "" {
		return nil, fmt.Errorf("memory_id is required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, ErrEmptyText
	}

	scope, bucketID, err := resolveMemoryBucket(req.Scope, req.UserID, req.SessionID, req.Namespace)
	if err != nil {
		return nil, err
	}
	// The global bucket belongs to nobody, whoever happens to write to it
	// first: an owner there would make the shared pool one user's rows.
	owner := req.UserID
	if scope == MemoryScopeGlobal {
		owner = ""
	}
	namespace := normalizeMemoryNamespace(req.Namespace)
	if err := db.checkMemoryWrite(ctx, bucketID, req.UserID, MemoryBucket{UserID: owner, Scope: scope, Namespace: namespace}); err != nil {
		return nil, err
	}
	// A supersede target that does not exist is refused before anything is
	// written, so the new row is absent after a missing target exactly as
	// after a foreign one, which the ports refuse before the call.
	if err := db.checkSupersedeTargets(ctx, req.MemoryID, req.Supersedes); err != nil {
		return nil, err
	}
	if err := db.ensureMemoryBucket(ctx, bucketID, owner, scope, namespace); err != nil {
		return nil, err
	}

	metadata := buildMemoryMetadata(scope, normalizeMemoryNamespace(req.Namespace), req.Metadata, req.Importance, req.TTLSeconds)
	stampWriter(ctx, metadata, anyString)
	// A re-save over an existing id keeps its decision, as an update does:
	// a proposed memory saved again is still proposed.
	var stored map[string]any
	if row, err := db.loadMemoryRow(ctx, req.MemoryID); err == nil {
		stored = row.record.Metadata
	} else if !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	if err := setReviewAny(ctx, metadata, stored, req.review()); err != nil {
		return nil, err
	}
	vectorBytes, err := db.embedMemoryContent(ctx, req.Content)
	if err != nil {
		return nil, err
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal memory metadata: %w", err)
	}

	role := firstNonEmpty(req.Role, defaultMemoryRole)
	if _, err := db.exec(ctx, `
		INSERT INTO messages (id, session_id, role, content, vector, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			session_id = excluded.session_id,
			role = excluded.role,
			content = excluded.content,
			vector = excluded.vector,
			metadata = excluded.metadata
	`, req.MemoryID, bucketID, role, req.Content, memoryVectorArg(db.Dialect(), vectorBytes), metadataJSON); err != nil {
		return nil, fmt.Errorf("save memory: %w", err)
	}

	row, err := db.loadMemoryRow(ctx, req.MemoryID)
	if err != nil {
		return nil, err
	}
	if err := db.markMemoriesSuperseded(ctx, req.MemoryID, req.Supersedes); err != nil {
		return nil, err
	}
	if err := db.saveMemoryGraph(ctx, req); err != nil {
		return nil, err
	}
	return &MemorySaveResponse{Memory: row.record}, nil
}

// UpdateMemory updates a memory record and refreshes its vector when needed.
func (db *DB) UpdateMemory(ctx context.Context, req MemoryUpdateRequest) (*MemorySaveResponse, error) {
	if req.MemoryID == "" {
		return nil, fmt.Errorf("memory_id is required")
	}

	row, err := db.loadMemoryRow(ctx, req.MemoryID)
	if err != nil {
		return nil, err
	}

	content := row.record.Content
	vectorBytes := row.vector
	if req.Content != nil {
		if strings.TrimSpace(*req.Content) == "" {
			return nil, ErrEmptyText
		}
		content = *req.Content
		vectorBytes, err = db.embedMemoryContent(ctx, content)
		if err != nil {
			return nil, err
		}
	}

	metadata := cloneAnyMap(row.record.Metadata)
	if req.Metadata != nil {
		for key, value := range req.Metadata {
			metadata[key] = value
		}
	}
	importance := row.record.Importance
	if req.Importance != nil {
		importance = *req.Importance
	}
	ttlSeconds := row.record.TTLSeconds
	if req.TTLSeconds != nil {
		ttlSeconds = *req.TTLSeconds
	}
	metadata = buildMemoryMetadata(row.record.Scope, row.record.Namespace, metadata, importance, ttlSeconds)
	stampUpdater(ctx, metadata, row.record.Metadata, anyString)
	if err := setReviewAny(ctx, metadata, row.record.Metadata, req.review()); err != nil {
		return nil, err
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal memory metadata: %w", err)
	}

	if _, err := db.exec(ctx, `
		UPDATE messages
		SET content = ?, vector = ?, metadata = ?
		WHERE id = ?
	`, content, memoryVectorArg(db.Dialect(), vectorBytes), metadataJSON, req.MemoryID); err != nil {
		return nil, fmt.Errorf("update memory: %w", err)
	}

	updated, err := db.loadMemoryRow(ctx, req.MemoryID)
	if err != nil {
		return nil, err
	}
	return &MemorySaveResponse{Memory: updated.record}, nil
}

// GetMemory fetches a memory record by ID.
//
// The list form takes at most MaxListedIDs ids and answers each distinct id
// once (listedIDs). It lists a row the caller's scope does not reach under missing,
// in the order asked, exactly as a row that does not exist: a confined
// caller cannot tell the two apart. The single form stays unconfined on
// purpose: the row guard of the ports (authz.Scope.GuardToolRows) reads
// through it to refuse a write to a foreign row, and a facade that hid the
// row there would hand the guard a row that does not exist.
func (db *DB) GetMemory(ctx context.Context, req MemoryGetRequest) (*MemoryGetResponse, error) {
	if len(req.MemoryIDs) > 0 {
		ids, err := listedIDs(req.MemoryIDs)
		if err != nil {
			return nil, err
		}
		resp := &MemoryGetResponse{}
		for _, id := range ids {
			row, err := db.loadMemoryRow(ctx, id)
			if errors.Is(err, core.ErrNotFound) {
				resp.Missing = append(resp.Missing, id)
				continue
			}
			if err != nil {
				return nil, err
			}
			if ok, err := db.memoryReadPermitted(ctx, row.record.SessionID); err != nil {
				return nil, err
			} else if !ok {
				resp.Missing = append(resp.Missing, id)
				continue
			}
			resp.Memories = append(resp.Memories, row.record)
		}
		if len(resp.Memories) > 0 {
			resp.Memory = resp.Memories[0]
		}
		return resp, nil
	}
	row, err := db.loadMemoryRow(ctx, req.MemoryID)
	if err != nil {
		return nil, err
	}
	return &MemoryGetResponse{Memory: row.record}, nil
}

// SearchMemory searches a resolved memory bucket, using semantic session search when an embedder is available.
// queryEmbedTimeout bounds embedding a search's query (searchMemory,
// SearchGraphRAG). Past it the search answers by keyword, which runs beside
// the embedding, so a busy model costs a recall this long and no longer.
// Agents give a recall a budget of a second or two; the default leaves the
// rest of it to the search. CORTEXDB_QUERY_EMBED_TIMEOUT_MS overrides it.
var queryEmbedTimeout = envDurationMS("CORTEXDB_QUERY_EMBED_TIMEOUT_MS", 800*time.Millisecond)

// embedCallTimeout bounds one call to the embedder made on behalf of the
// cache (embedder_cache.go), which outlives the request that started it.
const embedCallTimeout = 30 * time.Second

func envDurationMS(name string, def time.Duration) time.Duration {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && v > 0 {
		return time.Duration(v) * time.Millisecond
	}
	return def
}

func (db *DB) SearchMemory(ctx context.Context, req MemorySearchRequest) (*MemorySearchResponse, error) {
	return db.searchMemoryShared(ctx, req)
}

// searchMemory is SearchMemory with the recall counters optional: the PPR
// path runs it as a first stage over a wide pool, and a memory that was only
// a candidate there was not recalled.
func (db *DB) searchMemory(ctx context.Context, req MemorySearchRequest, recordRecalls bool) (*MemorySearchResponse, error) {
	resolution := resolveRetrievalPlan(retrievalPlanInput{
		Query:            req.Query,
		Plan:             req.Plan,
		Keywords:         req.Keywords,
		AlternateQueries: req.AlternateQueries,
		RetrievalMode:    req.RetrievalMode,
		Filters:          &RetrievalFilters{UserID: req.UserID, SessionID: req.SessionID, Scope: req.Scope, Namespace: req.Namespace},
		// Memories now have graph nodes of their own, so entity hints can be
		// followed back to them. Until they did, this said false and told the
		// truth: there was no edge leading to a memory to walk.
		SupportsGraph: true,
		EntityNames:   req.EntityNames,
		// Carries the job the unsupported branch used to do. That branch is what
		// returned "auto" so an embedder got used at all; reaching the real switch
		// without this would route every embedder-backed search to lexical.
		PreferSemantic: db.HasEmbedder(),
		// A memory only reaches the graph if it was saved with entities, so
		// guessing entities out of the query would usually route to a graph that
		// has nothing to say about them.
		GraphRequiresEntityNames: true,
	})
	if strings.TrimSpace(resolution.Plan.Query) == "" {
		return nil, ErrEmptyText
	}

	filters := resolution.Plan.Filters
	scope := req.Scope
	userID := req.UserID
	sessionID := req.SessionID
	namespace := req.Namespace
	if filters != nil {
		scope = firstNonEmpty(scope, filters.Scope)
		userID = firstNonEmpty(userID, filters.UserID)
		sessionID = firstNonEmpty(sessionID, filters.SessionID)
		namespace = firstNonEmpty(namespace, filters.Namespace)
	}

	_, bucketID, err := resolveMemoryBucket(scope, userID, sessionID, namespace)
	if err != nil {
		return nil, err
	}
	// A bucket the caller may not read answers the way an empty one does, so a
	// confined caller cannot tell another user's session from no session.
	if ok, err := db.memoryReadPermitted(ctx, bucketID); err != nil {
		return nil, err
	} else if !ok {
		return &MemorySearchResponse{Query: resolution.Plan.Query, Plan: resolution.Plan, Decision: resolution.Decision}, nil
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}
	ex := recallExplainerOf(ctx)
	var degraded []string

	if resolution.Decision.EffectiveMode == RetrievalModePPR {
		hits, reason, err := db.searchMemoryPPR(ctx, req, resolution, bucketID)
		if err != nil {
			return nil, err
		}
		ex.candidates(explainMemory, explainPPR, memoryCandidates(hits, func(h MemorySearchHit) float64 { return h.Score }))
		if recordRecalls {
			db.recordMemoryRecalls(ctx, hits)
		}
		decision := resolution.Decision
		decision.Reason = reason
		return &MemorySearchResponse{
			Query:    resolution.Plan.Query,
			Plan:     resolution.Plan,
			Decision: decision,
			Results:  hits,
		}, nil
	}

	// Was "!= lexical", which meant "== auto" while graph was unsupported here.
	// Spelled out now that graph is a real outcome, so entity hints are not
	// answered by a vector search that ignores them.
	// The semantic path merges with lexical rather than replacing it. It used
	// to return exclusively whenever it had any results, scored by list
	// position — so a query whose best answer was an exact keyword match lost
	// it to five vaguely-similar neighbours, and a query about nothing the
	// store knows still got five of them: a vector search without a floor has
	// a nearest neighbour to every question ever asked.
	// The keyword search runs beside the semantic one rather than after it:
	// a recall used to wait for the embedder and then for FTS, and when the
	// embedder was slow it gave up on both. Now the slower of the two sets the
	// time, and a semantic path that times out finds the keyword answer ready.
	type lexicalResult struct {
		hits []MemorySearchHit
		err  error
	}
	lexical := make(chan lexicalResult, 1)
	go func() {
		doneLexical := observability.Time(ctx, "lexical")
		hits, err := db.searchMemoryLexical(ctx, bucketID, resolution.Plan.Query, resolution.Plan.Keywords, resolution.Plan.AlternateQueries, req.TopK)
		doneLexical()
		lexical <- lexicalResult{hits, err}
	}()

	var semanticHits []MemorySearchHit
	if db.HasEmbedder() && resolution.Decision.EffectiveMode == RetrievalModeAuto {
		// Bounded: a recall waits on this, and the embedder can be a model on
		// another machine that has to load first. Past the bound the recall
		// answers by keyword, which is what it did anyway once the caller gave
		// up — only now it is 3 seconds in, not 24.
		embedCtx, cancelEmbed := context.WithTimeout(ctx, queryEmbedTimeout)
		queryVec, err := db.embedder.Embed(embedCtx, resolution.Plan.Query)
		cancelEmbed()
		if err != nil {
			log.Printf("cortexdb: memory semantic embed fallback to lexical: %v", err)
			degraded = addDegraded(degraded, DegradedEmbedderUnreachable)
			// Said in the answer too, not only the log: the decision used to
			// keep claiming a vector search had run, so a caller could not tell
			// a store with nothing semantically close from one whose embedder
			// was down.
			resolution.Decision.EffectiveMode = RetrievalModeLexical
			resolution.Decision.Reason = fmt.Sprintf("embedder failing (%v); answered by keyword search only", err)
		} else {
			if note := db.vectorDebtNote(); note != "" {
				resolution.Decision.Reason = strings.TrimSpace(resolution.Decision.Reason + "; " + note)
			}
			// Wider than topK on purpose: boosts reorder, and a memory just
			// below the raw-similarity cut is exactly the one importance or
			// recency should be able to lift into view.
			doneVector := observability.Time(ctx, "vector")
			scored, searchErr := db.store.SearchChatHistoryScored(ctx, queryVec, bucketID, req.TopK*4)
			doneVector()
			if searchErr != nil {
				log.Printf("cortexdb: memory semantic search fallback to lexical: %v", searchErr)
				degraded = addDegraded(degraded, DegradedVectorSearchFailed)
			} else {
				now := time.Now().UTC()
				floor := db.memoryFloorFor(resolution.Plan.Query)
				if ex != nil {
					raw := make([]RecallCandidate, 0, min(len(scored), maxExplainCandidates))
					for i, sm := range scored {
						if i == maxExplainCandidates {
							break
						}
						raw = append(raw, RecallCandidate{ID: sm.Message.ID, Score: sm.Score, Rank: i + 1})
					}
					ex.candidates(explainMemory, explainVector, raw)
				}
				for _, sm := range scored {
					if sm.Score < floor {
						ex.dropped(explainMemory, dropMemorySemanticFloor, sm.Message.ID, sm.Score, floor)
						continue
					}
					record := memoryRecordFromMessage(bucketID, "", sm.Message)
					if by := db.memoryFilteredBy(record); by != "" {
						ex.dropped(explainMemory, by, record.ID, sm.Score, 0)
						continue
					}
					semanticHits = append(semanticHits, MemorySearchHit{
						Memory:     record,
						Score:      applyMemoryRecallBoosts(sm.Score, record, now),
						Similarity: sm.Score,
						Via:        []string{memoryViaSemantic},
					})
				}
				sort.SliceStable(semanticHits, func(i, j int) bool { return semanticHits[i].Score > semanticHits[j].Score })
				if len(semanticHits) > req.TopK {
					semanticHits = semanticHits[:req.TopK]
				}
			}
		}
	}

	// Graph first when entity hints resolved the mode that way: an edge from a
	// named entity to a memory is a stronger statement than a word both happen
	// to contain. Lexical still runs to fill the rest of topK, so a graph miss
	// degrades to the old behaviour instead of returning nothing.
	var graphHits []MemorySearchHit
	if resolution.Decision.EffectiveMode == RetrievalModeGraph && resolution.Decision.UseGraph {
		doneGraph := observability.Time(ctx, "graph")
		graphHits, err = db.searchMemoryGraph(ctx, bucketID, resolution.Plan.EntityNames, req.TopK)
		doneGraph()
		if err != nil {
			return nil, err
		}
		tagMemoryHits(graphHits, memoryViaGraph)
		ex.candidates(explainMemory, explainGraph, memoryCandidates(graphHits, func(h MemorySearchHit) float64 { return h.Score }))
		if len(graphHits) >= req.TopK {
			graphHits = graphHits[:req.TopK]
			if recordRecalls {
				db.recordMemoryRecalls(ctx, graphHits)
			}
			return &MemorySearchResponse{
				Query:    resolution.Plan.Query,
				Plan:     resolution.Plan,
				Decision: resolution.Decision,
				Results:  graphHits,
				Degraded: degraded,
			}, nil
		}
	}

	lexicalAnswer := <-lexical
	hits, err := lexicalAnswer.hits, lexicalAnswer.err
	if err != nil {
		if len(semanticHits) == 0 {
			return nil, err
		}
		hits = nil // semantic already answered; lexical failing must not erase it
		degraded = addDegraded(degraded, DegradedKeywordSearchFailed)
	}
	tagMemoryHits(hits, memoryViaKeyword)
	ex.candidates(explainMemory, explainKeyword, memoryCandidates(hits, func(h MemorySearchHit) float64 { return h.Coverage }))
	if len(semanticHits) > 0 {
		if isBareKeyword(resolution.Plan.Query) && len(hits) > 0 {
			hits = mergeMemoryHits(hits, semanticHits, req.TopK)
		} else {
			hits = fuseMemoryHits(semanticHits, hits, req.TopK)
		}
	}
	if len(graphHits) > 0 {
		hits = mergeMemoryHits(graphHits, hits, req.TopK)
	}
	if recordRecalls {
		db.recordMemoryRecalls(ctx, hits)
	}
	return &MemorySearchResponse{
		Query:    resolution.Plan.Query,
		Plan:     resolution.Plan,
		Decision: resolution.Decision,
		Results:  hits,
		Degraded: degraded,
	}, nil
}

// memoryFilteredBy names the filter that keeps record out of a recall —
// expired, superseded, unreviewed — or "" when none does.
func (db *DB) memoryFilteredBy(record MemoryRecord) string {
	switch {
	case memoryExpired(record):
		return dropExpired
	case memorySuperseded(record):
		return dropSuperseded
	case db.memoryUnreviewed(record):
		return dropUnreviewed
	}
	return ""
}

// ListAllMemories returns every stored memory record across all scopes,
// newest first, skipping expired ones. It scans the memory buckets only
// (session ids under the `memory:` prefix), not arbitrary chat history.
// Intended for export/backup — see the --export-memory tool.
func (db *DB) ListAllMemories(ctx context.Context) ([]MemoryRecord, error) {
	rows, err := db.query(ctx, `
		SELECT m.id, m.session_id, s.user_id, m.role, m.content, m.metadata, m.created_at
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE m.session_id LIKE 'memory:%'
		ORDER BY m.created_at DESC, m.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []MemoryRecord
	for rows.Next() {
		var record MemoryRecord
		var metadataJSON []byte
		var createdAt time.Time
		if err := rows.Scan(&record.ID, &record.SessionID, &record.UserID, &record.Role, &record.Content, &metadataJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		record.CreatedAt = createdAt
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &record.Metadata); err != nil {
				return nil, fmt.Errorf("decode memory metadata: %w", err)
			}
		}
		applyMemoryMetadata(&record)
		if memoryExpired(record) {
			continue
		}
		// Derive scope from the bucket id when metadata did not carry it.
		if record.Scope == "" {
			record.Scope = scopeFromBucketID(record.SessionID)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// scopeFromBucketID pulls the scope segment out of a `memory:<scope>:…` id.
func scopeFromBucketID(bucketID string) string {
	parts := strings.SplitN(bucketID, ":", 3)
	if len(parts) >= 2 && parts[0] == "memory" {
		return parts[1]
	}
	return ""
}

// DeleteMemory removes a memory record by ID.
func (db *DB) DeleteMemory(ctx context.Context, req MemoryDeleteRequest) (*MemoryDeleteResponse, error) {
	if req.MemoryID == "" {
		return nil, fmt.Errorf("memory_id is required")
	}

	row, err := db.loadMemoryRow(ctx, req.MemoryID)
	if err != nil {
		return nil, err
	}
	if _, err := db.exec(ctx, `DELETE FROM messages WHERE id = ?`, req.MemoryID); err != nil {
		return nil, fmt.Errorf("delete memory: %w", err)
	}
	if _, err := db.exec(ctx, `
		DELETE FROM sessions
		WHERE id = ?
		  AND NOT EXISTS (SELECT 1 FROM messages WHERE session_id = ?)
	`, row.record.SessionID, row.record.SessionID); err != nil {
		return nil, fmt.Errorf("cleanup empty memory bucket: %w", err)
	}
	// The row is gone; the graph must not keep pointing at it. A memory saved
	// with entities has a memory:<id> node and mention edges, and leaving them
	// behind makes the graph describe a memory nobody can read — 1097 such
	// nodes were found after a bulk delete that only touched `messages`.
	// RetractNodes archives before it deletes, so the node stays readable
	// as of before the delete, and a memory that never had a node costs one
	// no-op statement.
	nodes, _, err := db.graph.RetractNodes(graph.WithInvalidation(ctx,
		graph.Invalidation{Reason: graph.ReasonRetracted, Producer: producerMemoryDelete}),
		[]string{memoryGraphNodeID(req.MemoryID)})
	if err != nil {
		return nil, fmt.Errorf("retract memory graph node: %w", err)
	}
	return &MemoryDeleteResponse{MemoryID: req.MemoryID, Deleted: true, GraphNodeRetracted: nodes > 0}, nil
}

// SaveMemory stores a memory item through the tool surface.
func (t *GraphRAGToolbox) SaveMemory(ctx context.Context, req MemorySaveRequest) (*MemorySaveResponse, error) {
	return t.db.SaveMemory(ctx, req)
}

// UpdateMemory updates a memory item through the tool surface.
func (t *GraphRAGToolbox) UpdateMemory(ctx context.Context, req MemoryUpdateRequest) (*MemorySaveResponse, error) {
	return t.db.UpdateMemory(ctx, req)
}

// GetMemory fetches a memory item through the tool surface.
func (t *GraphRAGToolbox) GetMemory(ctx context.Context, req MemoryGetRequest) (*MemoryGetResponse, error) {
	return t.db.GetMemory(ctx, req)
}

// SearchMemory searches memory through the tool surface.
func (t *GraphRAGToolbox) SearchMemory(ctx context.Context, req MemorySearchRequest) (*MemorySearchResponse, error) {
	return t.db.SearchMemory(ctx, req)
}

// DeleteMemory deletes a memory item through the tool surface.
func (t *GraphRAGToolbox) DeleteMemory(ctx context.Context, req MemoryDeleteRequest) (*MemoryDeleteResponse, error) {
	return t.db.DeleteMemory(ctx, req)
}

func (db *DB) ensureMemoryBucket(ctx context.Context, bucketID, userID, scope, namespace string) error {
	_, err := db.store.GetSession(ctx, bucketID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, core.ErrNotFound) {
		return err
	}
	return db.store.CreateSession(ctx, &core.Session{
		ID:     bucketID,
		UserID: userID,
		Metadata: map[string]any{
			"kind":      "memory_bucket",
			"scope":     scope,
			"namespace": namespace,
		},
	})
}

func (db *DB) embedMemoryContent(ctx context.Context, content string) ([]byte, error) {
	if db.embedder == nil {
		return nil, nil
	}
	vec, err := db.embedder.Embed(ctx, content)
	if err != nil {
		// A memory that cannot be embedded right now must still be remembered.
		// The embedder is a network service; failing the save couples "can I
		// remember" to "is that box awake", and a memory refused is gone —
		// unlike its vector, which a re-embed pass can fill in later. Search
		// already degrades to lexical for exactly this reason; the healer
		// embeds it once the embedder answers again.
		log.Printf("cortexdb: memory save proceeding without vector (embed failed: %v)", err)
		db.noteVectorDebt()
		return nil, nil
	}
	vectorBytes, err := encodeMemoryVector(db.Dialect(), vec)
	if err != nil {
		return nil, fmt.Errorf("encode memory vector: %w", err)
	}
	return vectorBytes, nil
}

func (db *DB) loadMemoryRow(ctx context.Context, memoryID string) (*memoryRow, error) {
	if memoryID == "" {
		return nil, fmt.Errorf("memory_id is required")
	}

	row := memoryRow{}
	var metadataJSON []byte
	var createdAt time.Time
	err := db.queryRow(ctx, `
		SELECT m.id, m.session_id, s.user_id, m.role, m.content, m.vector, m.metadata, m.created_at
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE m.id = ?
	`, memoryID).Scan(
		&row.record.ID,
		&row.record.SessionID,
		&row.record.UserID,
		&row.record.Role,
		&row.record.Content,
		&row.vector,
		&metadataJSON,
		&createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, core.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load memory: %w", err)
	}

	row.record.CreatedAt = createdAt
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &row.record.Metadata); err != nil {
			return nil, fmt.Errorf("decode memory metadata: %w", err)
		}
	}
	applyMemoryMetadata(&row.record)
	return &row, nil
}

// memoryLexicalPool is how many rows each keyword query may contribute before
// rescoring, as a multiple of topK and at least memoryLexicalPoolMin. Wider
// than topK on purpose: the rows are re-ranked by how much of the question
// they cover (see lexical_rarity.go), and each query's bm25 order is not that.
// Twice as wide found nothing more on the recall golden set.
const (
	memoryLexicalPoolFactor = 4
	memoryLexicalPoolMin    = 20
)

// searchMemoryLexical answers a memory search by keyword.
//
// Every keyword query — the question, its keywords OR-ed, each alternate
// query — and, for CJK text, the bigram path contribute candidates; each
// candidate is then scored once, by the rarity-weighted share of the question
// it covers (see lexical_rarity.go), with its best bm25 deciding among rows
// that cover the same. A row that shares only a common word with the question
// scores next to nothing, and one covering less than the keyword floor is not
// returned.
func (db *DB) searchMemoryLexical(ctx context.Context, bucketID, query string, keywords, alternateQueries []string, topK int) ([]MemorySearchHit, error) {
	variants := db.memoryVariantWords(ctx, bucketID, alternateQueries)
	queries := lexicalSearchQueries(query, keywords, alternateQueries, variants.kept)
	if len(queries) == 0 {
		return nil, ErrEmptyText
	}
	if topK <= 0 {
		topK = 5
	}

	type candidate struct {
		record MemoryRecord
		bm25   float64 // best bm25 relevance any query gave it, in [0,1)
	}
	candidates := make(map[string]*candidate)
	consider := func(record MemoryRecord, bm25 float64) {
		if c, ok := candidates[record.ID]; ok {
			c.bm25 = max(c.bm25, bm25)
			return
		}
		candidates[record.ID] = &candidate{record: record, bm25: bm25}
	}
	var firstErr error
	pool := max(topK*memoryLexicalPoolFactor, memoryLexicalPoolMin)

	for idx, searchQuery := range queries {
		lexSQL, lexArgs := memoryLexicalQuery(db.Dialect(), searchQuery, bucketID, pool)
		rows, err := db.query(ctx, lexSQL, lexArgs...)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("search memory lexical: %w", err)
			}
			continue
		}

		for rows.Next() {
			var record MemoryRecord
			var metadataJSON []byte
			var createdAt time.Time
			var rawRank float64
			if err := rows.Scan(&record.ID, &record.SessionID, &record.UserID, &record.Role, &record.Content, &metadataJSON, &createdAt, &rawRank); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan lexical memory: %w", err)
			}
			record.CreatedAt = createdAt
			if len(metadataJSON) > 0 {
				if err := json.Unmarshal(metadataJSON, &record.Metadata); err != nil {
					_ = rows.Close()
					return nil, fmt.Errorf("decode lexical memory metadata: %w", err)
				}
			}
			applyMemoryMetadata(&record)
			if memoryExpired(record) || memorySuperseded(record) || db.memoryUnreviewed(record) {
				continue
			}

			scoreWeight := 1.0 - float64(idx)*0.05
			if scoreWeight < 0.8 {
				scoreWeight = 0.8
			}
			// bm25() is negative and the better the match the lower it goes, so
			// relevance rises as the raw rank falls. Taking the absolute value and
			// dividing into 1 reversed that: the ORDER BY handed back the best
			// match first and the scoring then sorted it last, so a long memory
			// mentioning the term once outranked a short one about nothing else.
			relevance := -rawRank
			if relevance < 0 {
				relevance = 0
			}
			consider(record, (relevance/(1+relevance))*scoreWeight)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate lexical memory rows: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close lexical memory rows: %w", err)
		}
	}
	cjkQuery := core.ContainsCJK(query)
	if len(candidates) == 0 && !cjkQuery {
		return nil, firstErr
	}

	// One scan of the bucket measures it for the rarity score and for the
	// bigram path's BM25; the bigram path counts its own bigrams.
	// The words the keyword OR query searched for are credited first (see
	// lexicalORSynonyms), so the cap on synonyms does not leave one out.
	rq := newRarityQuery(query, append(append(lexicalORSynonyms(query, keywords, variants.kept), keywords...), alternateQueries...))
	rq.common = variants.commonBeside(keywords)
	var stats memoryBucketStats
	if cjkQuery || !rq.empty() {
		var err error
		if stats, err = db.memoryBucketStatsFor(ctx, bucketID, rq.patternsBesideBigrams()); err != nil {
			return nil, err
		}
	}
	// A CJK sentence is one token to the word index; see lexical_cjk.go.
	if cjkQuery {
		cjk, err := db.searchMemoryCJK(ctx, bucketID, query, &stats)
		if err != nil {
			log.Printf("cortexdb: cjk bigram memory search skipped: %v", err)
		}
		for _, c := range cjk {
			consider(c.record, c.relevance)
		}
	}
	if len(candidates) == 0 {
		return nil, firstErr
	}
	scorer := newRarityScorer(rq, stats.df, stats.n)
	floor := db.memoryKeywordFloor
	now := time.Now().UTC()
	results := make([]MemorySearchHit, 0, len(candidates))
	for _, c := range candidates {
		match := scorer.coverage(c.record.Content)
		if !scorer.answers(match, floor) {
			recallExplainerOf(ctx).dropped(explainMemory, dropMemoryKeywordFloor, c.record.ID, match.share, floor)
			continue
		}
		results = append(results, MemorySearchHit{
			Memory:   c.record,
			Score:    applyMemoryRecallBoosts(rarityRelevance(match.share, c.bm25), c.record, now),
			Coverage: match.share,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Memory.ID < results[j].Memory.ID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func resolveMemoryBucket(scope, userID, sessionID, namespace string) (string, string, error) {
	namespace = normalizeMemoryNamespace(namespace)
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch scope {
	case "":
		if strings.TrimSpace(sessionID) != "" {
			scope = MemoryScopeSession
		} else if strings.TrimSpace(userID) != "" {
			scope = MemoryScopeUser
		} else {
			scope = MemoryScopeGlobal
		}
	case MemoryScopeGlobal, MemoryScopeUser, MemoryScopeSession:
	default:
		return "", "", fmt.Errorf("unsupported memory scope: %s", scope)
	}

	switch scope {
	case MemoryScopeGlobal:
		return scope, fmt.Sprintf("memory:%s:%s", scope, namespace), nil
	case MemoryScopeUser:
		if strings.TrimSpace(userID) == "" {
			return "", "", fmt.Errorf("user_id is required for %s scope", scope)
		}
		return scope, fmt.Sprintf("memory:%s:%s:%s", scope, userID, namespace), nil
	case MemoryScopeSession:
		if strings.TrimSpace(sessionID) == "" {
			return "", "", fmt.Errorf("session_id is required for %s scope", scope)
		}
		return scope, fmt.Sprintf("memory:%s:%s:%s", scope, sessionID, namespace), nil
	default:
		return "", "", fmt.Errorf("unsupported memory scope: %s", scope)
	}
}

func normalizeMemoryNamespace(namespace string) string {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return defaultMemoryNamespace
	}
	return namespace
}

func buildMemoryMetadata(scope, namespace string, metadata map[string]any, importance float64, ttlSeconds int) map[string]any {
	out := cloneAnyMap(metadata)
	out["kind"] = "memory"
	out["scope"] = scope
	out["namespace"] = namespace
	out["importance"] = importance
	out["ttl_seconds"] = ttlSeconds
	if ttlSeconds > 0 {
		out["expires_at"] = time.Now().UTC().Add(time.Duration(ttlSeconds) * time.Second).Format(time.RFC3339)
	} else {
		delete(out, "expires_at")
	}
	return out
}

func applyMemoryMetadata(record *MemoryRecord) {
	if record.Metadata == nil {
		record.Metadata = map[string]any{}
	}
	if scope, ok := stringFromAny(record.Metadata["scope"]); ok {
		record.Scope = scope
	}
	if namespace, ok := stringFromAny(record.Metadata["namespace"]); ok {
		record.Namespace = namespace
	}
	if importance, ok := floatFromAny(record.Metadata["importance"]); ok {
		record.Importance = importance
	}
	if ttlSeconds, ok := intFromAny(record.Metadata["ttl_seconds"]); ok {
		record.TTLSeconds = ttlSeconds
	}
	if expiresAt, ok := stringFromAny(record.Metadata["expires_at"]); ok && expiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			record.ExpiresAt = &parsed
		}
	}
	record.ReviewStatus, _ = stringFromAny(record.Metadata[reviewStatusKey])
	record.ReviewedBy, _ = stringFromAny(record.Metadata[reviewedByKey])
}

func memoryRecordFromMessage(sessionID, userID string, message *core.Message) MemoryRecord {
	record := MemoryRecord{
		ID:        message.ID,
		UserID:    userID,
		SessionID: sessionID,
		Role:      message.Role,
		Content:   message.Content,
		CreatedAt: message.CreatedAt,
		Metadata:  cloneAnyMap(message.Metadata),
	}
	applyMemoryMetadata(&record)
	return record
}

func memoryExpired(record MemoryRecord) bool {
	if record.ExpiresAt == nil {
		return false
	}
	return record.ExpiresAt.Before(time.Now().UTC())
}

// saveMemoryGraph writes the entities and relations a caller attached to a
// memory. Best-effort ordering: entities first so relation endpoints exist,
// though UpsertRelations backfills either way.
func (db *DB) saveMemoryGraph(ctx context.Context, req MemorySaveRequest) error {
	if len(req.Entities) == 0 && len(req.Relations) == 0 {
		return nil
	}
	tools := db.GraphRAGTools()
	if len(req.Entities) > 0 {
		// The entities used to be written with nothing joining them to the memory
		// that asserted them: mention edges hang off ChunkIDs, and this passed
		// none. The graph grew entity nodes that no memory pointed at, so entity
		// hints could never lead back to a memory and memory search had no graph
		// to search. Giving the memory a node of its own and mentioning it from
		// each entity puts memories on the same footing as knowledge chunks.
		nodeID := memoryGraphNodeID(req.MemoryID)
		if err := db.upsertMemoryNode(ctx, tools, nodeID, req.Content); err != nil {
			return err
		}
		entities := make([]ToolEntityInput, 0, len(req.Entities))
		for _, entity := range req.Entities {
			entity.ChunkIDs = appendUniqueString(entity.ChunkIDs, nodeID)
			entities = append(entities, entity)
		}
		if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
			return fmt.Errorf("save memory entities: %w", err)
		}
	}
	if len(req.Relations) > 0 {
		if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: req.Relations}); err != nil {
			return fmt.Errorf("save memory relations: %w", err)
		}
	}
	return nil
}

// memoryGraphNodePrefix namespaces memory nodes in the graph the way entity: and
// chunk: namespace theirs.
const memoryGraphNodePrefix = "memory:"

// memoryGraphNodeID is the graph node standing for a stored memory.
func memoryGraphNodeID(memoryID string) string {
	return memoryGraphNodePrefix + memoryID
}

// memoryIDFromGraphNode reverses memoryGraphNodeID, reporting whether the node
// was a memory at all.
func memoryIDFromGraphNode(nodeID string) (string, bool) {
	if !strings.HasPrefix(nodeID, memoryGraphNodePrefix) {
		return "", false
	}
	id := strings.TrimPrefix(nodeID, memoryGraphNodePrefix)
	if id == "" {
		return "", false
	}
	return id, true
}

// upsertMemoryNode gives a memory a node so entities have something to mention.
//
// Written explicitly rather than left to the mention-edge stub filler, which
// would create it typed "chunk": a memory is not a chunk, and a graph view that
// says otherwise is a graph view nobody can read.
func (db *DB) upsertMemoryNode(ctx context.Context, tools *GraphRAGToolbox, nodeID, content string) error {
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return fmt.Errorf("save memory graph: init schema: %w", err)
	}
	vectorDim, err := tools.lexicalVectorDim(ctx, defaultGraphRAGCollection)
	if err != nil {
		return fmt.Errorf("save memory graph: vector dim: %w", err)
	}
	result, err := db.graph.UpsertNodesBatch(ctx, []*graph.GraphNode{{
		ID:         nodeID,
		Vector:     lexicalVectorForText(content, vectorDim),
		Content:    content,
		NodeType:   "memory",
		Properties: map[string]interface{}{"memory": true},
	}})
	if err != nil {
		return fmt.Errorf("save memory node: %w", err)
	}
	if err := result.Err(); err != nil {
		return fmt.Errorf("save memory node: %w", err)
	}
	return nil
}

// searchMemoryGraph finds memories by walking mention edges back from entities.
//
// This is the half of recall that lexical search cannot do: "what do I know
// about X" where the memory never spells X the way the question does. Scored by
// how many of the named entities a memory mentions, so a memory about two of
// them outranks one that merely touches one.
func (db *DB) searchMemoryGraph(ctx context.Context, bucketID string, entityNames []string, topK int) ([]MemorySearchHit, error) {
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("search memory graph: init schema: %w", err)
	}
	nodeIDs := make([]string, 0, len(entityNames))
	seen := make(map[string]struct{}, len(entityNames))
	for _, name := range entityNames {
		id := db.resolveEntityNameToNode(ctx, strings.TrimSpace(name))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		nodeIDs = append(nodeIDs, id)
	}
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	if topK <= 0 {
		topK = 5
	}

	placeholders := make([]string, len(nodeIDs))
	args := make([]interface{}, 0, len(nodeIDs)+1)
	for i, id := range nodeIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, memoryGraphNodePrefix+"%")

	rows, err := db.query(ctx, `
		SELECT from_node_id, COUNT(DISTINCT to_node_id)
		FROM graph_edges
		WHERE edge_type = 'mentions'
		  AND to_node_id IN (`+strings.Join(placeholders, ",")+`)
		  AND from_node_id LIKE ?
		GROUP BY from_node_id
		ORDER BY COUNT(DISTINCT to_node_id) DESC, from_node_id
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("search memory graph: %w", err)
	}
	type candidate struct {
		memoryID string
		mentions int
	}
	candidates := make([]candidate, 0, topK)
	for rows.Next() {
		var nodeID string
		var mentions int
		if err := rows.Scan(&nodeID, &mentions); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan memory graph hit: %w", err)
		}
		if memoryID, ok := memoryIDFromGraphNode(nodeID); ok {
			candidates = append(candidates, candidate{memoryID: memoryID, mentions: mentions})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate memory graph hits: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close memory graph hits: %w", err)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// Cut only after boosting. Mention counts tie constantly — on a well-
	// indexed graph a popular entity has hundreds of one-mention neighbours —
	// and truncating on the SQL ordering handed those ties to whichever memory
	// node id sorted first, which is an alphabet, not a ranking.
	candidateCap := topK * 8
	if len(candidates) > candidateCap {
		candidates = candidates[:candidateCap]
	}
	now := time.Now().UTC()
	hits := make([]MemorySearchHit, 0, len(candidates))
	for _, c := range candidates {
		row, err := db.loadMemoryRow(ctx, c.memoryID)
		if err != nil {
			// A node can outlive its memory — the memory was deleted and the graph
			// still remembers it. Skip it rather than failing the whole recall.
			continue
		}
		if row.record.SessionID != bucketID || memoryExpired(row.record) || memorySuperseded(row.record) || db.memoryUnreviewed(row.record) {
			continue
		}
		// Normalised so a memory mentioning every named entity scores 1.
		hits = append(hits, MemorySearchHit{
			Memory: row.record,
			Score:  applyMemoryRecallBoosts(float64(c.mentions)/float64(len(nodeIDs)), row.record, now),
		})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

func appendUniqueString(list []string, value string) []string {
	if value == "" {
		return list
	}
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

// mergeMemoryHits puts graph hits ahead of lexical ones without repeating a
// memory that both found, and keeps the result inside topK.
func mergeMemoryHits(graphHits, lexicalHits []MemorySearchHit, topK int) []MemorySearchHit {
	if topK <= 0 {
		topK = len(graphHits) + len(lexicalHits)
	}
	merged := make([]MemorySearchHit, 0, topK)
	seen := make(map[string]int, topK)
	for _, group := range [][]MemorySearchHit{graphHits, lexicalHits} {
		for _, hit := range group {
			if i, ok := seen[hit.Memory.ID]; ok {
				merged[i].Via = unionVia(merged[i].Via, hit.Via)
				continue
			}
			if len(merged) >= topK {
				continue
			}
			seen[hit.Memory.ID] = len(merged)
			merged = append(merged, hit)
		}
	}
	return merged
}

// fuseMemoryHits ranks the semantic and keyword answers together by
// reciprocal rank, the way knowledge search fuses its two lists.
//
// The semantic list used to go first and the keyword list only filled what
// was left, so five vague neighbours always beat an exact match: asking "HA"
// returned the user's name and city, every one above the 0.31 floor, while the
// memories about HA came back only to a keyword search.
//
// A memory both lists found ranks first, whatever its ranks: two retrievers
// agreeing is the strongest evidence there is. Among the rest a tie goes to
// the semantic hit. It used to go to the keyword hit, on the theory that it
// names what was asked; but a keyword hit the semantic search did not return
// is one it ranked below its own hits or below the floor, and "CortexDB
// 是什么，能用来做什么" put a memory sharing only 是什么 (rank 1 by keyword)
// above one about CortexDB (rank 1 by meaning, 0.54). Keyword scores now
// weigh rarity (see lexical_rarity.go), which is what removed that hit; on
// the recall golden set the direction of the tie is a wash either way.
//
// Going further — every semantic hit ahead of every keyword-only one that
// does not cover the whole question — cost two labelled answers of 48, so a
// keyword-only hit still interleaves with the semantic ones by rank.
// Each hit keeps the score its own search gave it.
func fuseMemoryHits(semanticHits, lexicalHits []MemorySearchHit, topK int) []MemorySearchHit {
	type fused struct {
		hit      MemorySearchHit
		rrf      float64
		semantic bool
		lexical  bool
		firstPos int
	}
	byID := make(map[string]*fused, len(semanticHits)+len(lexicalHits))
	var order []*fused
	add := func(hits []MemorySearchHit, lexical bool) {
		for rank, hit := range hits {
			f, ok := byID[hit.Memory.ID]
			if !ok {
				f = &fused{hit: hit, firstPos: len(order)}
				byID[hit.Memory.ID] = f
				order = append(order, f)
			} else {
				f.hit.Via = unionVia(f.hit.Via, hit.Via)
				f.hit.Coverage = max(f.hit.Coverage, hit.Coverage)
				f.hit.Similarity = max(f.hit.Similarity, hit.Similarity)
			}
			f.rrf += 1.0 / (hybridRRFK + float64(rank+1))
			if lexical {
				f.lexical = true
			} else {
				f.semantic = true
			}
		}
	}
	add(semanticHits, false)
	add(lexicalHits, true)
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if ab, bb := a.semantic && a.lexical, b.semantic && b.lexical; ab != bb {
			return ab
		}
		if a.rrf != b.rrf {
			return a.rrf > b.rrf
		}
		if a.semantic != b.semantic {
			return a.semantic
		}
		return a.firstPos < b.firstPos
	})
	if topK <= 0 || topK > len(order) {
		topK = len(order)
	}
	out := make([]MemorySearchHit, 0, topK)
	for _, f := range order[:topK] {
		out = append(out, f.hit)
	}
	return out
}

// The retrievers a memory hit can owe its place to (MemorySearchHit.Via).
const (
	memoryViaSemantic = "semantic"
	memoryViaKeyword  = "keyword"
	memoryViaGraph    = "graph"
)

func tagMemoryHits(hits []MemorySearchHit, via string) {
	for i := range hits {
		hits[i].Via = unionVia(hits[i].Via, []string{via})
	}
}

// unionVia adds the retrievers in more to via, each once, in the order found.
func unionVia(via, more []string) []string {
	for _, v := range more {
		if !slices.Contains(via, v) {
			via = append(via, v)
		}
	}
	return via
}

// isBareKeyword reports a query that is a single term — an acronym, a name,
// a word or two of Chinese — rather than a question. An embedding of "HA" or
// "大脑" carries next to no meaning, and its nearest neighbours are whatever
// short memories sit near the middle of the space, so for such a query the
// memories that contain the term come first and semantic hits only fill.
func isBareKeyword(query string) bool {
	fields := strings.Fields(query)
	if len(fields) != 1 {
		return false
	}
	runes := []rune(fields[0])
	cjk := 0
	for _, r := range runes {
		switch {
		case unicode.Is(unicode.Han, r):
			cjk++
		case unicode.IsLetter(r), unicode.IsDigit(r), strings.ContainsRune("-_./+#", r):
		default:
			return false
		}
	}
	if cjk > 0 {
		return cjk == len(runes) && cjk <= 4
	}
	return len(runes) <= 32
}

// applyMemoryRecallBoosts folds importance and age into a relevance score as a
// bounded tie-breaker: the multiplier lives in [0.85, 1.0], so equal matches
// are ordered by importance and recency while a genuinely better match cannot
// be dethroned by being old. agentmem multiplies the raw decay in — right for
// working memory that goes stale in weeks, wrong here, where a July memory is
// still the correct answer in August: measured on the golden set, the raw form
// pushed correct months-old answers out of the top ranks.
func applyMemoryRecallBoosts(relevance float64, record MemoryRecord, now time.Time) float64 {
	importance := record.Importance
	if importance <= 0 {
		importance = 0.5
	}
	days := now.Sub(record.CreatedAt).Hours() / 24
	if days < 0 {
		days = 0
	}
	inner := (0.5 + importance*0.5) * math.Exp(-0.007*days)
	return relevance * (0.85 + 0.15*inner)
}

// memorySuperseded reports whether a newer memory has replaced this one. A
// superseded memory stays stored — the export shows it, and its id explains
// where a correction came from — but recall must not present it as current:
// the whole point of superseding is that the old wording answers with stale
// facts in a confident voice.
func memorySuperseded(record MemoryRecord) bool {
	if record.Metadata == nil {
		return false
	}
	v, _ := record.Metadata["superseded_by"].(string)
	return strings.TrimSpace(v) != ""
}

// checkSupersedeTargets refuses a supersede target that does not exist,
// before the save writes anything.
func (db *DB) checkSupersedeTargets(ctx context.Context, newID string, targets []string) error {
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" || target == newID {
			continue
		}
		row, err := db.loadMemoryRow(ctx, target)
		if err != nil {
			return fmt.Errorf("supersede %q: %w", target, err)
		}
		// A target outside the caller's scope is not found, as a missing
		// one is, the way memory_get lists it.
		ok, err := db.memoryReadPermitted(ctx, row.record.SessionID)
		if err != nil {
			return fmt.Errorf("supersede %q: %w", target, err)
		}
		if !ok {
			return fmt.Errorf("supersede %q: %w", target, core.ErrNotFound)
		}
	}
	return nil
}

// markMemoriesSuperseded stamps each target as replaced by newID.
func (db *DB) markMemoriesSuperseded(ctx context.Context, newID string, targets []string) error {
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" || target == newID {
			continue
		}
		row, err := db.loadMemoryRow(ctx, target)
		if err != nil {
			// A supersede aimed at an id that does not exist is a caller mistake
			// worth hearing about: silently succeeding would leave them sure the
			// old fact is retired when nothing changed.
			return fmt.Errorf("supersede %q: %w", target, err)
		}
		metadata := cloneAnyMap(row.record.Metadata)
		metadata["superseded_by"] = newID
		// When, so a mistaken pass can be found and undone by time.
		metadata["superseded_at"] = time.Now().UTC().Format(time.RFC3339)
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("supersede %q: marshal metadata: %w", target, err)
		}
		if _, err := db.exec(ctx,
			`UPDATE messages SET metadata = ? WHERE id = ?`, metadataJSON, target); err != nil {
			return fmt.Errorf("supersede %q: %w", target, err)
		}
	}
	return nil
}

// resolveEntityNameToNode maps an entity name to its graph node, following
// aliases when the direct node is gone.
//
// Entity resolution merges "K8s" into "Kubernetes" and records the alias on
// the surviving node — but a query naming the alias still derived the old id
// and found nothing. Merging entities without resolving their names at query
// time silently disconnects every caller who knew the entity by the name that
// lost.
func (db *DB) resolveEntityNameToNode(ctx context.Context, name string) string {
	id := EntityNodeID(name)
	if id == "" {
		return ""
	}
	if nodes, err := db.graph.GetNodesBatch(ctx, []string{id}); err == nil && len(nodes) > 0 && nodes[0] != nil {
		return id
	}
	// Nodes carrying aliases are the merged few, so scanning them is cheap.
	rows, err := db.query(ctx, `
		SELECT id, properties FROM graph_nodes
		WHERE id LIKE 'entity:%' AND properties LIKE '%"aliases"%'`)
	if err != nil {
		return id
	}
	defer rows.Close()
	wantKey := entityAliasKey(name)
	for rows.Next() {
		var nodeID, propsJSON string
		if err := rows.Scan(&nodeID, &propsJSON); err != nil {
			continue
		}
		var props struct {
			Aliases []string `json:"aliases"`
		}
		if err := json.Unmarshal([]byte(propsJSON), &props); err != nil {
			continue
		}
		for _, alias := range props.Aliases {
			if entityAliasKey(alias) == wantKey {
				return nodeID
			}
		}
	}
	return id
}

// entityAliasKey compares names the way entity resolution groups them:
// case, space and punctuation insensitive, except for a leading dash. It must
// agree with graphflow's canonicalKey — that is what decides which names were
// merged, and a looser key here resolves a plain name onto a flag's node.
func entityAliasKey(name string) string {
	var b strings.Builder
	if strings.HasPrefix(strings.TrimSpace(name), "-") {
		b.WriteRune('-')
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// recordMemoryRecalls bumps recall counters on memories a search returned.
//
// This is the observability half of the feedback loop: which memories actually
// get recalled, and which have never surfaced. It deliberately does NOT feed
// ranking — recall begets recall, and a popularity boost would compound its own
// bias. The numbers exist for the usage report, where a person decides.
func (db *DB) recordMemoryRecalls(ctx context.Context, hits []MemorySearchHit) {
	db.recordMemoryRecallsTimes(ctx, hits, 1)
}

// recordMemoryRecallsTimes counts each hit as recalled times times: the
// requests a shared search answered at once (memory_search_coalesce.go) are
// each a recall, and writing "seen count + 1" once per request would count
// them all as one.
func (db *DB) recordMemoryRecallsTimes(ctx context.Context, hits []MemorySearchHit, times int) {
	if len(hits) == 0 || times <= 0 {
		return
	}
	defer observability.Time(ctx, "recall-writes")()
	now := time.Now().UTC().Format(time.RFC3339)

	// Written as a whole column rather than patched in SQL.
	//
	// It used to be a json_set with two paths at once — SQLite's own spelling,
	// which PostgreSQL does not have — and the errors here are discarded by
	// design, so on PostgreSQL this wrote nothing and said nothing about it.
	// Every memory's recall_count stayed at zero and the usage report showed a
	// brain nobody had ever read from.
	//
	// A dialect-aware json_set would not have been enough either: this column
	// is TEXT on SQLite and JSONB on PostgreSQL, so even the guarded
	// expression built for graph properties is wrong here. Marshalling the map
	// the search already returned sidesteps the column type entirely. Two
	// concurrent recalls of the same memory can lose a count, which is the
	// right trade for accounting that must never fail a search.
	for _, hit := range hits {
		metadata := make(map[string]any, len(hit.Memory.Metadata)+2)
		for k, v := range hit.Memory.Metadata {
			metadata[k] = v
		}
		metadata["recall_count"] = memoryRecallCount(hit.Memory.Metadata) + times
		metadata["last_recalled_at"] = now

		encoded, err := json.Marshal(metadata)
		if err != nil {
			continue
		}
		// Best-effort by design: usage accounting must never fail a search.
		_, _ = db.exec(ctx,
			`UPDATE messages SET metadata = ? WHERE id = ?`, string(encoded), hit.Memory.ID)
	}
}

// memoryLexicalQuery builds the lexical lookup over stored messages, returning
// the same columns whichever backend answers it.
//
// The chunk side of retrieval already had this split (see ftsSearchQuery);
// memory did not, and asked PostgreSQL for `messages_fts MATCH ?` — an FTS5
// virtual table that does not exist there and a MATCH operator PostgreSQL does
// not have. Every memory search, and every recall that reads memory, failed
// with a bare "syntax error at or near MATCH".
func memoryLexicalQuery(d sqldialect.Dialect, query, bucketID string, limit int) (string, []any) {
	columns := `m.id, m.session_id, s.user_id, m.role, m.content, m.metadata, m.created_at`

	if d != nil && d.Kind() == sqldialect.Postgres {
		cond, condArgs, _ := core.PostgresLexicalCondition("m.content", query)
		rank, rankArgs := core.PostgresLexicalRank("m.content", query)

		args := append([]any{}, rankArgs...)
		args = append(args, condArgs...)
		args = append(args, bucketID, limit)
		return `
			SELECT ` + columns + `, ` + rank + ` as score
			FROM messages m
			JOIN sessions s ON s.id = m.session_id
			WHERE ` + cond + ` AND m.session_id = ?
			ORDER BY score, m.id
			LIMIT ?
		`, args
	}

	// A one- or two-character CJK query produces no trigram, so MATCH finds
	// nothing however much of the corpus contains it. The chunk path has had
	// this fallback for a while; memory was still returning an empty list for
	// "风控".
	if core.BelowTrigramFloor(query) {
		return `
			SELECT ` + columns + `, 0 as score
			FROM messages m
			JOIN sessions s ON s.id = m.session_id
			WHERE m.content LIKE ? ESCAPE '\' AND m.session_id = ?
			ORDER BY m.id
			LIMIT ?
		`, []any{core.SubstringPattern(query), bucketID, limit}
	}

	// CJK text needs the trigram companion index; see core.CJKAwareIndex.
	index := core.CJKAwareIndex("messages_fts", query)
	return `
		SELECT ` + columns + `, bm25(` + index + `) as score
		FROM ` + index + `
		JOIN messages m ON m.rowid = ` + index + `.rowid
		JOIN sessions s ON s.id = m.session_id
		WHERE ` + index + ` MATCH ? AND m.session_id = ?
		ORDER BY score
		LIMIT ?
	`, []any{query, bucketID, limit}
}

// memoryRecallCount reads the running recall count out of a memory's metadata.
//
// The value has been through JSON, so it is a float64 here whatever it was
// written as; anything else — absent, null, a string from an older writer —
// counts as zero rather than failing the update.
func memoryRecallCount(metadata map[string]any) int {
	switch v := metadata["recall_count"].(type) {
	case float64:
		if v < 0 {
			return 0
		}
		return int(v)
	case int:
		if v < 0 {
			return 0
		}
		return v
	default:
		return 0
	}
}
