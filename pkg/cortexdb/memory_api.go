package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// memorySemanticFloor is the cosine similarity below which a semantic hit is
// noise. A vector search has a nearest neighbour to every query, including
// queries the store holds nothing about; the floor is what lets it answer
// "nothing relevant". Calibrated on a live store of ~2k memories with
// embeddinggemma: unrelated probes (weather, cooking, novels) peaked at 0.263
// while the weakest genuine hit scored 0.311.
const memorySemanticFloor = 0.28

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
	if err := db.ensureMemoryBucket(ctx, bucketID, req.UserID, scope, normalizeMemoryNamespace(req.Namespace)); err != nil {
		return nil, err
	}

	metadata := buildMemoryMetadata(scope, normalizeMemoryNamespace(req.Namespace), req.Metadata, req.Importance, req.TTLSeconds)
	vectorBytes, err := db.embedMemoryContent(ctx, req.Content)
	if err != nil {
		return nil, err
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal memory metadata: %w", err)
	}

	role := firstNonEmpty(req.Role, defaultMemoryRole)
	if _, err := db.store.GetDB().ExecContext(ctx, `
		INSERT INTO messages (id, session_id, role, content, vector, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			session_id = excluded.session_id,
			role = excluded.role,
			content = excluded.content,
			vector = excluded.vector,
			metadata = excluded.metadata
	`, req.MemoryID, bucketID, role, req.Content, vectorBytes, metadataJSON); err != nil {
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
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal memory metadata: %w", err)
	}

	if _, err := db.store.GetDB().ExecContext(ctx, `
		UPDATE messages
		SET content = ?, vector = ?, metadata = ?
		WHERE id = ?
	`, content, vectorBytes, metadataJSON, req.MemoryID); err != nil {
		return nil, fmt.Errorf("update memory: %w", err)
	}

	updated, err := db.loadMemoryRow(ctx, req.MemoryID)
	if err != nil {
		return nil, err
	}
	return &MemorySaveResponse{Memory: updated.record}, nil
}

// GetMemory fetches a memory record by ID.
func (db *DB) GetMemory(ctx context.Context, req MemoryGetRequest) (*MemoryGetResponse, error) {
	row, err := db.loadMemoryRow(ctx, req.MemoryID)
	if err != nil {
		return nil, err
	}
	return &MemoryGetResponse{Memory: row.record}, nil
}

// SearchMemory searches a resolved memory bucket, using semantic session search when an embedder is available.
func (db *DB) SearchMemory(ctx context.Context, req MemorySearchRequest) (*MemorySearchResponse, error) {
	resolution := resolveRetrievalPlan(retrievalPlanInput{
		Query:                    req.Query,
		Plan:                     req.Plan,
		Keywords:                 req.Keywords,
		AlternateQueries:         req.AlternateQueries,
		RetrievalMode:            req.RetrievalMode,
		Filters:                  &RetrievalFilters{UserID: req.UserID, SessionID: req.SessionID, Scope: req.Scope, Namespace: req.Namespace},
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
	if req.TopK <= 0 {
		req.TopK = 5
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
	var semanticHits []MemorySearchHit
	if db.HasEmbedder() && resolution.Decision.EffectiveMode == RetrievalModeAuto {
		queryVec, err := db.embedder.Embed(ctx, resolution.Plan.Query)
		if err != nil {
			log.Printf("cortexdb: memory semantic embed fallback to lexical: %v", err)
		} else {
			// Wider than topK on purpose: boosts reorder, and a memory just
			// below the raw-similarity cut is exactly the one importance or
			// recency should be able to lift into view.
			scored, searchErr := db.store.SearchChatHistoryScored(ctx, queryVec, bucketID, req.TopK*4)
			if searchErr != nil {
				log.Printf("cortexdb: memory semantic search fallback to lexical: %v", searchErr)
			} else {
				now := time.Now().UTC()
				for _, sm := range scored {
					if sm.Score < memorySemanticFloor {
						continue
					}
					record := memoryRecordFromMessage(bucketID, "", sm.Message)
					if memoryExpired(record) || memorySuperseded(record) {
						continue
					}
					semanticHits = append(semanticHits, MemorySearchHit{
						Memory: record,
						Score:  applyMemoryRecallBoosts(sm.Score, record, now),
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
		graphHits, err = db.searchMemoryGraph(ctx, bucketID, resolution.Plan.EntityNames, req.TopK)
		if err != nil {
			return nil, err
		}
		if len(graphHits) >= req.TopK {
			return &MemorySearchResponse{
				Query:    resolution.Plan.Query,
				Plan:     resolution.Plan,
				Decision: resolution.Decision,
				Results:  graphHits[:req.TopK],
			}, nil
		}
	}

	hits, err := db.searchMemoryLexical(ctx, bucketID, resolution.Plan.Query, resolution.Plan.Keywords, resolution.Plan.AlternateQueries, req.TopK)
	if err != nil {
		if len(semanticHits) == 0 {
			return nil, err
		}
		hits = nil // semantic already answered; lexical failing must not erase it
	}
	if len(semanticHits) > 0 {
		hits = mergeMemoryHits(semanticHits, hits, req.TopK)
	}
	if len(graphHits) > 0 {
		hits = mergeMemoryHits(graphHits, hits, req.TopK)
	}
	return &MemorySearchResponse{
		Query:    resolution.Plan.Query,
		Plan:     resolution.Plan,
		Decision: resolution.Decision,
		Results:  hits,
	}, nil
}

// ListAllMemories returns every stored memory record across all scopes,
// newest first, skipping expired ones. It scans the memory buckets only
// (session ids under the `memory:` prefix), not arbitrary chat history.
// Intended for export/backup — see the --export-memory tool.
func (db *DB) ListAllMemories(ctx context.Context) ([]MemoryRecord, error) {
	rows, err := db.store.GetDB().QueryContext(ctx, `
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
	if _, err := db.store.GetDB().ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, req.MemoryID); err != nil {
		return nil, fmt.Errorf("delete memory: %w", err)
	}
	if _, err := db.store.GetDB().ExecContext(ctx, `
		DELETE FROM sessions
		WHERE id = ?
		  AND NOT EXISTS (SELECT 1 FROM messages WHERE session_id = ?)
	`, row.record.SessionID, row.record.SessionID); err != nil {
		return nil, fmt.Errorf("cleanup empty memory bucket: %w", err)
	}
	return &MemoryDeleteResponse{MemoryID: req.MemoryID, Deleted: true}, nil
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
		// already degrades to lexical for exactly this reason.
		log.Printf("cortexdb: memory save proceeding without vector (embed failed: %v)", err)
		return nil, nil
	}
	vectorBytes, err := encoding.EncodeVector(vec)
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
	err := db.store.GetDB().QueryRowContext(ctx, `
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

func (db *DB) searchMemoryLexical(ctx context.Context, bucketID, query string, keywords, alternateQueries []string, topK int) ([]MemorySearchHit, error) {
	queries := lexicalSearchQueries(query, keywords, alternateQueries)
	if len(queries) == 0 {
		return nil, ErrEmptyText
	}
	if topK <= 0 {
		topK = 5
	}

	type scoredMemory struct {
		record MemoryRecord
		score  float64
	}
	merged := make(map[string]scoredMemory)
	var firstErr error

	for idx, searchQuery := range queries {
		// CJK text needs the trigram companion index; see core.CJKAwareIndex.
		index := core.CJKAwareIndex("messages_fts", searchQuery)
		rows, err := db.store.GetDB().QueryContext(ctx, `
			SELECT m.id, m.session_id, s.user_id, m.role, m.content, m.metadata, m.created_at, bm25(`+index+`)
			FROM `+index+`
			JOIN messages m ON m.rowid = `+index+`.rowid
			JOIN sessions s ON s.id = m.session_id
			WHERE `+index+` MATCH ?
			  AND m.session_id = ?
			ORDER BY bm25(`+index+`)
			LIMIT ?
		`, searchQuery, bucketID, topK*4)
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
			if memoryExpired(record) || memorySuperseded(record) {
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
			score := applyMemoryRecallBoosts((relevance/(1+relevance))*scoreWeight, record, time.Now().UTC())
			if existing, ok := merged[record.ID]; !ok || score > existing.score {
				merged[record.ID] = scoredMemory{record: record, score: score}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate lexical memory rows: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close lexical memory rows: %w", err)
		}
	}
	if len(merged) == 0 && firstErr != nil {
		return nil, firstErr
	}

	ordered := make([]scoredMemory, 0, len(merged))
	for _, hit := range merged {
		ordered = append(ordered, hit)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].score == ordered[j].score {
			return ordered[i].record.ID < ordered[j].record.ID
		}
		return ordered[i].score > ordered[j].score
	})
	if len(ordered) > topK {
		ordered = ordered[:topK]
	}

	results := make([]MemorySearchHit, 0, len(ordered))
	for _, hit := range ordered {
		results = append(results, MemorySearchHit{Memory: hit.record, Score: hit.score})
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
	nodeIDs := make([]string, 0, len(entityNames))
	seen := make(map[string]struct{}, len(entityNames))
	for _, name := range entityNames {
		id := EntityNodeID(strings.TrimSpace(name))
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
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("search memory graph: init schema: %w", err)
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

	rows, err := db.store.GetDB().QueryContext(ctx, `
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

	hits := make([]MemorySearchHit, 0, topK)
	for _, c := range candidates {
		if len(hits) >= topK {
			break
		}
		row, err := db.loadMemoryRow(ctx, c.memoryID)
		if err != nil {
			// A node can outlive its memory — the memory was deleted and the graph
			// still remembers it. Skip it rather than failing the whole recall.
			continue
		}
		if row.record.SessionID != bucketID || memoryExpired(row.record) || memorySuperseded(row.record) {
			continue
		}
		// Normalised so a memory mentioning every named entity scores 1.
		hits = append(hits, MemorySearchHit{
			Memory: row.record,
			Score:  applyMemoryRecallBoosts(float64(c.mentions)/float64(len(nodeIDs)), row.record, time.Now().UTC()),
		})
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
	seen := make(map[string]struct{}, topK)
	for _, group := range [][]MemorySearchHit{graphHits, lexicalHits} {
		for _, hit := range group {
			if len(merged) >= topK {
				return merged
			}
			if _, ok := seen[hit.Memory.ID]; ok {
				continue
			}
			seen[hit.Memory.ID] = struct{}{}
			merged = append(merged, hit)
		}
	}
	return merged
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
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("supersede %q: marshal metadata: %w", target, err)
		}
		if _, err := db.store.GetDB().ExecContext(ctx,
			`UPDATE messages SET metadata = ? WHERE id = ?`, metadataJSON, target); err != nil {
			return fmt.Errorf("supersede %q: %w", target, err)
		}
	}
	return nil
}
