package core

import (
	"context"
	"time"
)

// Close closes the database connection and releases resources
func (s *SQLiteStore) Close() error {
	// Outside the lock, and before it. The checkpoint goroutine takes a read lock of its own, so
	// waiting for it to finish while holding the write lock would wait forever.
	s.stopWALCheckpointer()

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
		// One last fold of the write-ahead log, before the pool goes away. A clean shutdown should
		// leave a database and nothing beside it; SQLite only deletes the log when the last
		// connection closes, and only if it managed to check-point it first.
		s.checkpointWALLocked(context.Background())
		if err := s.db.Close(); err != nil {
			return err
		}
	}

	s.logger.Info("database connection closed")

	return nil
}
