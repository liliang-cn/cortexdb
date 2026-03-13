package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

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
	if strings.TrimSpace(req.Query) == "" {
		return nil, ErrEmptyText
	}

	_, bucketID, err := resolveMemoryBucket(req.Scope, req.UserID, req.SessionID, req.Namespace)
	if err != nil {
		return nil, err
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}

	if db.HasEmbedder() && normalizeRetrievalMode(req.RetrievalMode) != RetrievalModeLexical {
		queryVec, err := db.embedder.Embed(ctx, req.Query)
		if err == nil {
			messages, err := db.store.SearchChatHistory(ctx, queryVec, bucketID, req.TopK)
			if err == nil && len(messages) > 0 {
				hits := make([]MemorySearchHit, 0, len(messages))
				for i, message := range messages {
					record := memoryRecordFromMessage(bucketID, "", message)
					if memoryExpired(record) {
						continue
					}
					score := float64(len(messages)-i) / float64(len(messages))
					hits = append(hits, MemorySearchHit{Memory: record, Score: score})
				}
				if len(hits) > 0 {
					return &MemorySearchResponse{Query: req.Query, Results: hits}, nil
				}
			}
		}
	}

	hits, err := db.searchMemoryLexical(ctx, bucketID, req.Query, req.Keywords, req.AlternateQueries, req.TopK)
	if err != nil {
		return nil, err
	}
	return &MemorySearchResponse{Query: req.Query, Results: hits}, nil
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
		return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
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
		rows, err := db.store.GetDB().QueryContext(ctx, `
			SELECT m.id, m.session_id, s.user_id, m.role, m.content, m.metadata, m.created_at, bm25(messages_fts)
			FROM messages_fts
			JOIN messages m ON m.rowid = messages_fts.rowid
			JOIN sessions s ON s.id = m.session_id
			WHERE messages_fts MATCH ?
			  AND m.session_id = ?
			ORDER BY bm25(messages_fts)
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
			if memoryExpired(record) {
				continue
			}

			scoreWeight := 1.0 - float64(idx)*0.05
			if scoreWeight < 0.8 {
				scoreWeight = 0.8
			}
			score := (1 / (1 + math.Abs(rawRank))) * scoreWeight
			if existing, ok := merged[record.ID]; !ok || score > existing.score {
				merged[record.ID] = scoredMemory{record: record, score: score}
			}
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
