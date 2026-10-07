package cortexdb

// Vectors a save could not get.
//
// SaveMemory remembers even while the embedder is down — a memory refused is
// gone, a vector missing is a gap a later pass can fill (see
// embedMemoryContent). Until this file nothing filled it: the pass existed as
// ReembedMemoryVectors, reachable only from a command-line flag nobody runs
// after an outage. Measured on a live setup: an embeddings gateway answered
// 503 ("no node currently serves model") for a whole session, every memory
// written in it was stored without a vector, and they stayed that way after
// the gateway recovered — out of semantic recall for good, while keyword
// search still found them and hid the gap. The only trace was a log line.
//
// So the DB now heals itself, and says when it cannot: every call to the
// configured embedder is watched, a save that had to go without a vector wakes
// a background pass that fills it in, a pass that cannot reach the embedder
// backs off instead of hammering it, and EmbedderStatus — surfaced in recall's
// decision, graph_health and the server's Info — reports the failure and the
// backlog while they last.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// VectorHealOptions tunes the background pass that embeds memories saved
// while the embedder was unreachable.
type VectorHealOptions struct {
	// Disabled leaves healing to explicit ReembedMemoryVectors calls.
	Disabled bool
	// MinBackoff is the wait after a pass that could not reach the embedder;
	// it doubles on every further failure up to MaxBackoff. Defaults 30s and
	// 30m: an embeddings outage is measured in minutes to hours, and a pass
	// that retried at once would only add load to whatever is struggling.
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// Idle is how often to look for missing vectors when there is no sign of
	// any. A save that had to go without one wakes the pass at once, so this
	// only matters for a backlog another process left behind. Default 10m.
	Idle time.Duration
	// Batch caps how many memories one pass embeds. Default 256.
	Batch int
}

func (o VectorHealOptions) withDefaults() VectorHealOptions {
	if o.MinBackoff <= 0 {
		o.MinBackoff = 30 * time.Second
	}
	if o.MaxBackoff < o.MinBackoff {
		o.MaxBackoff = 30 * time.Minute
		if o.MaxBackoff < o.MinBackoff {
			o.MaxBackoff = o.MinBackoff
		}
	}
	if o.Idle <= 0 {
		o.Idle = 10 * time.Minute
	}
	if o.Batch <= 0 {
		o.Batch = 256
	}
	return o
}

// WithVectorHealing tunes, or with Disabled turns off, the background pass
// that embeds memories saved while the embedder was down. On by default
// whenever an embedder is configured.
func WithVectorHealing(opts VectorHealOptions) Option {
	return func(db *DB) { db.heal.opts = opts }
}

// EmbedderStatus is how the configured embedder has been answering, and how
// many memories are still waiting for the vector it owes them.
type EmbedderStatus struct {
	Configured bool `json:"configured"`
	// Healthy is false from the first failed call until the next one that
	// succeeds. Calls cancelled by their caller do not count either way.
	Healthy      bool       `json:"healthy"`
	LastError    string     `json:"last_error,omitempty"`
	FailingSince *time.Time `json:"failing_since,omitempty"`
	LastSuccess  *time.Time `json:"last_success,omitempty"`
	// MemoriesWithoutVector are found by keyword search only until they are
	// embedded.
	MemoriesWithoutVector int `json:"memories_without_vector"`
}

// vectorHealRuntime is the embedder's watch and the healing pass's state.
type vectorHealRuntime struct {
	opts VectorHealOptions

	mu           sync.Mutex
	lastErr      string
	failingSince time.Time
	lastSuccess  time.Time

	// debt is the healer's running count of memories without a vector: set by
	// each pass, raised by every save that had to go without one. -1 until
	// first counted. Kept so a recall can mention the gap without a COUNT.
	debt atomic.Int64

	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

func (h *vectorHealRuntime) note(err error) {
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		// The caller gave up, which says nothing about the embedder.
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	if err == nil {
		h.lastErr, h.failingSince, h.lastSuccess = "", time.Time{}, now
		return
	}
	h.lastErr = err.Error()
	if h.failingSince.IsZero() {
		h.failingSince = now
	}
}

func (h *vectorHealRuntime) failing() (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastErr, !h.failingSince.IsZero()
}

// watchedEmbedder reports every call's outcome to the heal runtime. It wraps
// whatever WithEmbedder was given, so every path that embeds — saves, recall,
// knowledge ingest, the healer itself — keeps the status current.
type watchedEmbedder struct {
	Embedder
	heal *vectorHealRuntime
}

func (w watchedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	v, err := w.Embedder.Embed(ctx, text)
	w.heal.note(err)
	return v, err
}

func (w watchedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	v, err := w.Embedder.EmbedBatch(ctx, texts)
	w.heal.note(err)
	return v, err
}

// unwrapEmbedder returns the embedder the caller configured.
func unwrapEmbedder(e Embedder) Embedder {
	if w, ok := e.(watchedEmbedder); ok {
		return w.Embedder
	}
	return e
}

// EmbedderStatus reports the embedder's recent answers and the memories still
// waiting for a vector. The count is taken now, not from the healer's tally.
func (db *DB) EmbedderStatus(ctx context.Context) (EmbedderStatus, error) {
	status := EmbedderStatus{Configured: db.embedder != nil, Healthy: true}
	if db.embedder == nil {
		return status, nil
	}
	db.heal.mu.Lock()
	status.LastError = db.heal.lastErr
	if !db.heal.failingSince.IsZero() {
		t := db.heal.failingSince
		status.FailingSince = &t
		status.Healthy = false
	}
	if !db.heal.lastSuccess.IsZero() {
		t := db.heal.lastSuccess
		status.LastSuccess = &t
	}
	db.heal.mu.Unlock()
	n, err := db.countMemoriesWithoutVector(ctx)
	if err != nil {
		return status, err
	}
	status.MemoriesWithoutVector = n
	return status, nil
}

// missingMemoryVectorClause selects memory rows whose vector is absent or, on
// SQLite, the wrong size for the embedder: a stored vector there is a 4-byte
// length header plus float32s, so the byte length names the dimension without
// decoding. A PostgreSQL column is vector(N) and cannot hold the wrong size.
func (db *DB) missingMemoryVectorClause() (string, []any) {
	if db.Dialect().Kind() == sqldialect.Postgres {
		return `session_id LIKE 'memory:%' AND vector IS NULL`, nil
	}
	return `session_id LIKE 'memory:%' AND (vector IS NULL OR length(vector) != ?)`, []any{4 + 4*db.embedder.Dim()}
}

func (db *DB) countMemoriesWithoutVector(ctx context.Context) (int, error) {
	if db.embedder == nil {
		return 0, nil
	}
	clause, args := db.missingMemoryVectorClause()
	var n int
	if err := db.queryRow(ctx, `SELECT COUNT(*) FROM messages WHERE `+clause, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count memories without vector: %w", err)
	}
	return n, nil
}

// noteVectorDebt records a memory saved without a vector and wakes the
// healer, which will wait out its backoff first if the embedder is failing.
func (db *DB) noteVectorDebt() {
	if db.heal.debt.Load() >= 0 {
		db.heal.debt.Add(1)
	}
	if db.heal.wake == nil {
		return
	}
	select {
	case db.heal.wake <- struct{}{}:
	default:
	}
}

// vectorDebtNote is what a recall says about memories it can only find by
// keyword, or "" when there are none it knows of.
func (db *DB) vectorDebtNote() string {
	n := db.heal.debt.Load()
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%d memories have no vector yet and are found by keyword only until they are embedded", n)
}

// startVectorHealer runs the healing pass in the background. Called once by
// Open, after options are applied.
func (db *DB) startVectorHealer() {
	db.heal.debt.Store(-1)
	if db.embedder == nil || db.heal.opts.Disabled || isMemoryDSN(db.store.Config().Path) {
		// An in-memory database is one per connection; a background worker
		// would heal an empty database of its own (see startChangeRuntime).
		return
	}
	opts := db.heal.opts.withDefaults()
	bg, cancel := context.WithCancel(context.Background())
	db.heal.cancel = cancel
	db.heal.wake = make(chan struct{}, 1)
	db.heal.done = make(chan struct{})
	go func() {
		defer close(db.heal.done)
		backoff := opts.MinBackoff
		wait := time.Duration(0) // the backlog an earlier process left is looked at on start
		timer := time.NewTimer(wait)
		defer timer.Stop()
		for {
			select {
			case <-bg.Done():
				return
			case <-timer.C:
			case <-db.heal.wake:
				// A save just went without a vector. While the embedder is
				// failing that is no news: the backoff stands.
				if _, failing := db.heal.failing(); failing {
					continue
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			remaining, healed, err := db.healMemoryVectors(bg, opts.Batch)
			if bg.Err() != nil {
				return
			}
			switch {
			case err != nil:
				log.Printf("cortexdb: memory vectors still missing (%d), retrying in %s: %v", remaining, backoff, err)
				wait = backoff
				backoff *= 2
				if backoff > opts.MaxBackoff {
					backoff = opts.MaxBackoff
				}
			case remaining > 0 && healed > 0:
				backoff, wait = opts.MinBackoff, 0
			default:
				backoff, wait = opts.MinBackoff, opts.Idle
			}
			timer.Reset(wait)
		}
	}()
}

// healMemoryVectors embeds one batch of memories without a vector and returns
// how many remain. An error means the embedder could not be used.
func (db *DB) healMemoryVectors(ctx context.Context, batch int) (remaining, healed int, err error) {
	before, err := db.countMemoriesWithoutVector(ctx)
	if err != nil {
		return 0, 0, err
	}
	db.heal.debt.Store(int64(before))
	if before == 0 {
		return 0, 0, nil
	}
	report, err := db.ReembedMemoryVectors(ctx, ReembedOptions{Limit: batch})
	if err == nil && report.Failed > 0 && report.Reembedded == 0 {
		err = errors.New(strings.Join(report.Errors, "; "))
	}
	after, cerr := db.countMemoriesWithoutVector(ctx)
	if cerr != nil {
		after = before
	}
	db.heal.debt.Store(int64(after))
	if report != nil {
		healed = report.Reembedded
	}
	return after, healed, err
}

func (db *DB) stopVectorHealer() {
	if db.heal.cancel != nil {
		db.heal.cancel()
		<-db.heal.done
		db.heal.cancel = nil
	}
}
