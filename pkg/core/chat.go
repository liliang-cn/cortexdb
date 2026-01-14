package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liliang-cn/sqvect/internal/encoding"
)

// Session represents a chat session or conversation thread
type Session struct {
	ID        string                 `json:"id"`
	UserID    string                 `json:"user_id"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// Message represents a single message in a chat session
type Message struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"session_id"`
	Role      string                 `json:"role"` // 'user', 'assistant', 'system'
	Content   string                 `json:"content"`
	Vector    []float32              `json:"vector,omitempty"` // Embedding for long-term memory
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// CreateSession creates a new chat session
func (s *SQLiteStore) CreateSession(ctx context.Context, session *Session) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("create_session", ErrStoreClosed)
	}

	metadataJSON, _ := json.Marshal(session.Metadata)

	query := `
		INSERT INTO sessions (id, user_id, metadata, created_at, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`
	_, err := s.db.ExecContext(ctx, query, session.ID, session.UserID, metadataJSON)
	return err
}

// GetSession retrieves a session by ID
func (s *SQLiteStore) GetSession(ctx context.Context, id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sess Session
	var metadataJSON []byte

	err := s.db.QueryRowContext(ctx, "SELECT id, user_id, metadata, created_at, updated_at FROM sessions WHERE id = ?", id).
		Scan(&sess.ID, &sess.UserID, &metadataJSON, &sess.CreatedAt, &sess.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, wrapError("get_session", ErrNotFound)
	}
	if err != nil {
		return nil, err
	}

	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &sess.Metadata)
	}
	return &sess, nil
}

// AddMessage adds a message to a session
// If vector is provided, it can be used for semantic search over chat history
func (s *SQLiteStore) AddMessage(ctx context.Context, msg *Message) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("add_message", ErrStoreClosed)
	}

	metadataJSON, _ := json.Marshal(msg.Metadata)
	
	// Encode vector if present
	var vectorBytes []byte
	var err error
	if len(msg.Vector) > 0 {
		vectorBytes, err = encoding.EncodeVector(msg.Vector)
		if err != nil {
			return fmt.Errorf("failed to encode message vector: %w", err)
		}
	}

	query := `
		INSERT INTO messages (id, session_id, role, content, vector, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	_, err = s.db.ExecContext(ctx, query, msg.ID, msg.SessionID, msg.Role, msg.Content, vectorBytes, metadataJSON, time.Now().UTC())
	
	// If vector is present, we should also index it in the main embeddings table or HNSW
	// However, for simplicity, we treat message vectors separately or rely on user to also call Upsert()
	// if they want it in the global search index.
	// TODO: Consider an option to auto-index messages into a "chat_memory" collection.

	return err
}

// GetSessionHistory retrieves recent messages from a session
func (s *SQLiteStore) GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]*Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT id, session_id, role, content, vector, metadata, created_at
		FROM messages 
		WHERE session_id = ? 
		ORDER BY created_at DESC 
		LIMIT ?
	`
	
	rows, err := s.db.QueryContext(ctx, query, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*Message
	for rows.Next() {
		var msg Message
		var vectorBytes, metadataJSON []byte
		
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &vectorBytes, &metadataJSON, &msg.CreatedAt); err != nil {
			continue
		}
		
		if len(vectorBytes) > 0 {
			msg.Vector, _ = encoding.DecodeVector(vectorBytes)
		}
		if len(metadataJSON) > 0 {
			_ = json.Unmarshal(metadataJSON, &msg.Metadata)
		}
		
		messages = append(messages, &msg)
	}
	
	// Reverse to return chronological order (oldest first)
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

// SearchChatHistory performs semantic search over messages
// This requires messages to have vectors stored
func (s *SQLiteStore) SearchChatHistory(ctx context.Context, queryVec []float32, sessionID string, limit int) ([]*Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// This is a linear scan over the session's messages.
	// For huge history, we should use HNSW, but session history is usually small (<1000 items).
	
	query := `
		SELECT id, session_id, role, content, vector, metadata, created_at
		FROM messages 
		WHERE session_id = ? AND vector IS NOT NULL
	`
	
	rows, err := s.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scoredMsg struct {
		msg   *Message
		score float32
	}
	var scored []scoredMsg

	for rows.Next() {
		var msg Message
		var vectorBytes, metadataJSON []byte
		
		rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &vectorBytes, &metadataJSON, &msg.CreatedAt)
		
		if len(vectorBytes) > 0 {
			msg.Vector, _ = encoding.DecodeVector(vectorBytes)
			score := s.similarityFn(queryVec, msg.Vector)
			
			if len(metadataJSON) > 0 {
				_ = json.Unmarshal(metadataJSON, &msg.Metadata)
			}
			
			scored = append(scored, scoredMsg{msg: &msg, score: float32(score)})
		}
	}
	
	// Sort by score
	// Note: Implement simple sort
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}
	
	result := make([]*Message, 0, limit)
	for i := 0; i < len(scored) && i < limit; i++ {
		result = append(result, scored[i].msg)
	}
	
	return result, nil
}
