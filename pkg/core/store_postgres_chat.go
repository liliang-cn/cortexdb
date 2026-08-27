package core

// Sessions, messages and chat search on PostgreSQL.
//
// The point of a second backend is that a caller cannot tell which one
// answered, so every choice here is made by reading chat.go and copying it
// rather than by asking what PostgreSQL would prefer: the newest N messages
// come back oldest-first, a missing session is ErrNotFound and not an empty
// result, and the search score is a cosine similarity in the same 0..1 sense
// the SQLite path computes in Go. Where the database can do the work the Go
// loop was doing — ranking by distance, applying the limit — it does, because
// that is an implementation detail and not a behaviour.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CreateSession creates a new chat session.
//
// created_at and updated_at come from the database, as they do on SQLite: the
// row's clock is the database's, not the caller's.
func (s *PostgresStore) CreateSession(ctx context.Context, session *Session) error {
	if session == nil {
		return fmt.Errorf("create session: nil session")
	}
	metadataJSON, _ := json.Marshal(session.Metadata)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		session.ID, session.UserID, string(metadataJSON))
	return err
}

// GetSession retrieves a session by ID.
//
// A session that is not there is an error, not a zero value — the same
// wrapped ErrNotFound SQLite returns, so `errors.Is(err, ErrNotFound)` works
// against either backend.
func (s *PostgresStore) GetSession(ctx context.Context, id string) (*Session, error) {
	var (
		sess         Session
		userID       sql.NullString
		metadataJSON []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, metadata, created_at, updated_at
		FROM sessions WHERE id = $1`, id).
		Scan(&sess.ID, &userID, &metadataJSON, &sess.CreatedAt, &sess.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, wrapError("get_session", ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	sess.UserID = userID.String
	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &sess.Metadata)
	}
	return &sess, nil
}

// AddMessage adds a message to a session.
//
// The timestamp is the caller's clock (time.Now().UTC()), not the database's,
// because that is what chat.go does and history ordering depends on it.
func (s *PostgresStore) AddMessage(ctx context.Context, msg *Message) error {
	if msg == nil {
		return fmt.Errorf("add message: nil message")
	}
	metadataJSON, _ := json.Marshal(msg.Metadata)

	// NULL rather than an empty vector: SearchChatHistory selects on
	// `vector IS NOT NULL`, and pgvector has no zero-width value to mean
	// "absent" the way an empty BLOB does.
	var vector any
	if len(msg.Vector) > 0 {
		vector = PgVectorLiteral(msg.Vector)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (id, session_id, role, content, vector, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		msg.ID, msg.SessionID, msg.Role, msg.Content, vector, string(metadataJSON), time.Now().UTC())
	return err
}

// GetSessionHistory retrieves recent messages from a session.
//
// The newest `limit` messages, returned oldest-first — the window is taken
// from the end and then read forwards, which is what a model wants in a
// prompt. A session with no messages is an empty result and not an error; a
// session that does not exist is indistinguishable from one that is empty,
// here as on SQLite.
func (s *PostgresStore) GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]*Message, error) {
	// SQLite reads a negative LIMIT as "no limit"; PostgreSQL rejects it. The
	// caller's meaning is preserved rather than its literal.
	limitClause := "LIMIT ALL"
	args := []any{sessionID}
	if limit >= 0 {
		args = append(args, limit)
		limitClause = fmt.Sprintf("LIMIT $%d", len(args))
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, vector, metadata, created_at
		FROM messages
		WHERE session_id = $1
		ORDER BY created_at DESC
		`+limitClause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*Message
	for rows.Next() {
		msg, err := scanPgMessage(rows.Scan)
		if err != nil {
			// Skipped rather than fatal, as on SQLite: one unreadable row
			// should not cost the caller the rest of its history.
			continue
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Reverse to chronological order (oldest first).
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

// SearchChatHistoryScored performs semantic search over a session's messages,
// keeping the similarity.
//
// The ranking happens in the database — `ORDER BY vector <=> $1` — where
// SQLite scans the session and sorts in Go. The score is converted back from
// cosine distance to cosine similarity so a threshold tuned against one
// backend is right on the other; an exact match scores ~1 on both.
func (s *PostgresStore) SearchChatHistoryScored(ctx context.Context, queryVec []float32, sessionID string, limit int) ([]ScoredMessage, error) {
	// One retry, for a `vector` type replaced under this connection.
	// See IsStaleTypeCache: the statement never ran, and the failure is
	// what clears the cache that caused it.
	var out []ScoredMessage
	err := retryOnStaleTypeCache(func() error {
		var e error
		out, e = s.searchChatHistoryScored(ctx, queryVec, sessionID, limit)
		return e
	})
	return out, err
}

func (s *PostgresStore) searchChatHistoryScored(ctx context.Context, queryVec []float32, sessionID string, limit int) ([]ScoredMessage, error) {
	if limit <= 0 {
		// SQLite's `make([]ScoredMessage, 0, limit)` yields nothing for 0 and
		// panics below it. Nothing is the honest answer for both.
		return []ScoredMessage{}, nil
	}
	if len(queryVec) == 0 {
		return []ScoredMessage{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, vector, metadata, created_at,
		       1 - (vector <=> $1) AS score
		FROM messages
		WHERE session_id = $2 AND vector IS NOT NULL
		-- created_at breaks ties so a tied pair does not come back in a
		-- different order on every call. SQLite's tie order is insertion
		-- order, which is what this reproduces.
		ORDER BY vector <=> $1, created_at
		LIMIT $3`,
		PgVectorLiteral(queryVec), sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ScoredMessage, 0, limit)
	for rows.Next() {
		var score float64
		msg, err := scanPgMessage(func(dest ...any) error {
			return rows.Scan(append(dest, &score)...)
		})
		if err != nil {
			continue
		}
		result = append(result, ScoredMessage{Message: msg, Score: score})
	}
	return result, rows.Err()
}

// SearchChatHistory returns the messages alone, for callers that never needed
// the scores.
func (s *PostgresStore) SearchChatHistory(ctx context.Context, queryVec []float32, sessionID string, limit int) ([]*Message, error) {
	// One retry, for a `vector` type replaced under this connection.
	// See IsStaleTypeCache: the statement never ran, and the failure is
	// what clears the cache that caused it.
	var out []*Message
	err := retryOnStaleTypeCache(func() error {
		var e error
		out, e = s.searchChatHistory(ctx, queryVec, sessionID, limit)
		return e
	})
	return out, err
}

func (s *PostgresStore) searchChatHistory(ctx context.Context, queryVec []float32, sessionID string, limit int) ([]*Message, error) {
	scored, err := s.SearchChatHistoryScored(ctx, queryVec, sessionID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]*Message, 0, len(scored))
	for _, sm := range scored {
		result = append(result, sm.Message)
	}
	return result, nil
}

// scanPgMessage reads the seven message columns in the order every query above
// selects them. It takes the scan function rather than *sql.Rows so a caller
// selecting extra trailing columns (the score) can append to the destinations.
func scanPgMessage(scan func(dest ...any) error) (*Message, error) {
	var (
		msg          Message
		vectorText   sql.NullString
		metadataJSON []byte
	)
	if err := scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content,
		&vectorText, &metadataJSON, &msg.CreatedAt); err != nil {
		return nil, err
	}
	if vectorText.Valid && vectorText.String != "" {
		msg.Vector, _ = parsePgVector(vectorText.String)
	}
	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &msg.Metadata)
	}
	return &msg, nil
}

// parsePgVector reads back what PgVectorLiteral writes: [1,2,3].
func parsePgVector(text string) ([]float32, error) {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimPrefix(trimmed, "[")
	trimmed = strings.TrimSuffix(trimmed, "]")
	if trimmed == "" {
		return nil, nil
	}
	parts := strings.Split(trimmed, ",")
	vec := make([]float32, 0, len(parts))
	for _, part := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return nil, fmt.Errorf("parse vector %q: %w", text, err)
		}
		vec = append(vec, float32(f))
	}
	return vec, nil
}
