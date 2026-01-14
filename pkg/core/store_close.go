package core

import (
	"context"
	"time"
)

// Close closes the database connection and releases resources
func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	// Try to save index snapshot before closing
	// Use a new context with timeout since the original context might be cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.saveIndexSnapshot(ctx); err != nil {
		s.logger.Error("failed to save index snapshot before closing", "error", err)
		// Continue with close despite snapshot failure
	}

	s.closed = true

	if s.db != nil {
		if err := s.db.Close(); err != nil {
			return err
		}
	}

	s.logger.Info("database connection closed")

	return nil
}
