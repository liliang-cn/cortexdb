package core

import (
	"context"
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

	// Save the index snapshot before closing, on a context of its own since
	// the caller's may be cancelled. The bound is generous on purpose: a
	// snapshot is written in chunks, each statement honours the deadline, and
	// a deadline that falls mid-write rolls the whole snapshot back. Five
	// seconds did, at 100,000 768-d vectors, and the next open rebuilt the
	// index from every row.
	ctx, cancel := context.WithTimeout(context.Background(), snapshotWriteTimeout)
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
