package core

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Keeping the write-ahead log from eating the disk.
//
// The store opens every pooled connection with `journal_mode(WAL)`, which is right: it is what lets
// readers and the writer work at the same time. What WAL also does is grow a `<db>-wal` file until
// something checkpoints it back into the database — and SQLite's own automatic checkpoint, which
// fires when a write transaction commits and the log is past `wal_autocheckpoint` (1000 pages, 4 MB),
// can only copy frames older than the *oldest reader still holding a snapshot*. This store keeps a
// pool of up to 25 connections with 10 held idle for two hours, so during any sustained ingest there
// is essentially always a reader in flight, every automatic checkpoint gives up, and the log grows
// without bound.
//
// It was measured rather than reasoned about: an Athanor instance ingesting 248 documents produced an
// 11 GB `brain.db-wal` against a 636 MB database, filled the 57 GB disk it shared with other
// services, and took down a PostgreSQL belonging to an unrelated application — which answered every
// connection with "could not write init file: No space left on device" until the disk was freed.
// Nothing in this package had ever called `wal_checkpoint`, and nothing else was going to.
//
// So the store checkpoints on a clock of its own. TRUNCATE rather than PASSIVE, because the problem
// is the file's *size*: PASSIVE copies frames out but leaves the log as long as it ever got, so a log
// that reached 11 GB once stays 11 GB on disk forever after.

// walCheckpointInterval is how often the store tries to fold its write-ahead log back into the
// database. Short enough that a busy period cannot run the log up to gigabytes; long enough to be
// invisible next to the work itself, since a checkpoint with nothing to do costs a lock and returns.
const walCheckpointInterval = 30 * time.Second

// startWALCheckpointer runs periodic checkpoints until the store is closed. Called with the store
// lock held, from `Init`.
//
// In-memory databases have no write-ahead log and no file to grow, so they get no goroutine.
//
// Two details that are not decoration:
//
//   - `Init` is called more than once on the same store. `hindsight.New` opens the database — which
//     inits it — and then inits the vector store again itself; there are other callers like it. A
//     second start would overwrite the channels the first goroutine is selecting on, leaving it
//     running forever on a store nobody can stop, so the first one owns the job.
//   - The goroutine closes over the channels as locals rather than reading `s.checkpointStop`. The
//     field is written here under the lock and the goroutine holds no lock at all, so reading it
//     there is a race — and it is exactly the race the detector caught: the write from the second
//     `Init` against the read in the first goroutine's select.
func (s *SQLiteStore) startWALCheckpointer() {
	if isMemoryPath(s.config.Path) || s.checkpointStop != nil {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.checkpointStop = stop
	s.checkpointDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(walCheckpointInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.checkpointWAL(context.Background())
			}
		}
	}()
	s.logger.Debug("wal checkpointer started", "interval", walCheckpointInterval)
}

// stopWALCheckpointer ends the checkpoint goroutine and waits for it, so a closed store leaves
// nothing running behind it. Safe to call twice, and on a store that never started one.
//
// Called *without* the store lock — waiting for a goroutine that takes a read lock of its own while
// holding the write lock would wait forever — so the fields are read under the lock and let go of
// before the wait.
func (s *SQLiteStore) stopWALCheckpointer() {
	s.mu.Lock()
	stop, done := s.checkpointStop, s.checkpointDone
	s.mu.Unlock()
	if stop == nil {
		return
	}
	s.checkpointOnce.Do(func() { close(stop) })
	<-done
}

// checkpointWAL folds the write-ahead log back into the database and truncates it.
//
// A checkpoint that cannot get the locks it needs is not a failure: it means readers or a writer are
// mid-flight, and the next tick will find a quieter moment. SQLite reports that as `busy = 1` in the
// result row rather than as an error, and it is left at debug for exactly that reason — a warning
// every thirty seconds during a long ingest would say nothing and be read as a fault.
func (s *SQLiteStore) checkpointWAL(ctx context.Context) {
	s.mu.RLock()
	db, closed := s.db, s.closed
	s.mu.RUnlock()
	if closed || db == nil {
		return
	}
	s.checkpoint(ctx, db)
}

// checkpointWALLocked is the same thing for a caller that already holds the store's lock — `Close`,
// folding the log one last time before the pool goes away.
func (s *SQLiteStore) checkpointWALLocked(ctx context.Context) {
	if s.db == nil {
		return
	}
	s.checkpoint(ctx, s.db)
}

func (s *SQLiteStore) checkpoint(ctx context.Context, db *sql.DB) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var busy, walPages, movedPages sql.NullInt64
	err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &walPages, &movedPages)
	switch {
	case err != nil:
		s.logger.Warn("wal checkpoint failed", "error", err)
	case busy.Int64 != 0:
		s.logger.Debug("wal checkpoint skipped, database busy", "walPages", walPages.Int64)
	default:
		s.logger.Debug("wal checkpoint completed", "pages", movedPages.Int64)
	}
}

// isMemoryPath reports whether this store is backed by memory rather than by a file.
//
// Both spellings, because both open an in-memory database: the bare `:memory:` and the shared-cache
// URI form `file:name?mode=memory`.
func isMemoryPath(path string) bool {
	return strings.Contains(path, ":memory:") || strings.Contains(path, "mode=memory")
}
