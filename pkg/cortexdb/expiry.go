package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// expiresAtKey is the engine's own metadata key for a document's expiry.
// It is written from the explicit expires_at field of ingest_document,
// knowledge_save and knowledge_update, and from nowhere else: a copy in a
// caller's metadata is dropped, and a caller's key named expires_at is the
// caller's, stored as it came and never read. The leading underscore marks
// the engine's keys, as importflow's _table does.
const expiresAtKey = "_expires_at"

// normalizeExpiresAt checks an explicit expiry and returns it as a UTC
// instant in the one form the gate compares as text.
func normalizeExpiresAt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", fmt.Errorf("expires_at must be an RFC 3339 instant such as 2031-10-18T00:00:00Z, got %q", value)
	}
	// Stored as text and compared as text: the year must keep four digits
	// in UTC, or "10000-01-01" would sort before "2026".
	if y := t.UTC().Year(); y < 1 || y > 9999 {
		return "", fmt.Errorf("expires_at must fall within the years 0001 to 9999 in UTC, got %q", value)
	}
	return t.UTC().Format(time.RFC3339), nil
}

// setExpiry puts the explicit expiry on the metadata a write is about to
// store, after dropping any copy of the engine's key the caller sent. An
// empty explicit value means no expiry.
func setExpiry(metadata map[string]string, explicit string) error {
	delete(metadata, expiresAtKey)
	normalized, err := normalizeExpiresAt(explicit)
	if err != nil {
		return err
	}
	if normalized != "" {
		metadata[expiresAtKey] = normalized
	}
	return nil
}

// carryExpiry is setExpiry for every write over a stored record: an explicit
// value sets or, when empty, removes the expiry; no explicit value keeps the
// stored one, whatever the write does with the caller's metadata. A record
// that does not exist yet has nothing stored, and gets no expiry.
func carryExpiry(metadata map[string]string, stored map[string]any, explicit *string) error {
	if explicit != nil {
		return setExpiry(metadata, *explicit)
	}
	delete(metadata, expiresAtKey)
	if value, ok := stored[expiresAtKey].(string); ok && value != "" {
		metadata[expiresAtKey] = value
	}
	return nil
}

// expiredPredicate is the SQL that says a document has expired at the
// instant bound to its placeholder. Every path that hides, skips or deletes
// an expired document uses this one predicate: the search gate, the dedupe
// lookup, the aggregates and the sweep. It is written on the very
// expression idx_documents_expires_at is built on (store_init.go,
// store_postgres.go): the planner answers it from the index, and a
// predicate on any other expression, even an equivalent one, is a scan.
// The instants are UTC RFC 3339 of one width, so text order is time order.
func (db *DB) expiredPredicate() string {
	expr := db.Dialect().JSONTextGuarded("metadata", expiresAtKey)
	return `(` + expr + ` > '' AND ` + expr + ` <= ?)`
}

// hiddenPredicate is the whole gate in SQL: expired at the instant bound to
// the placeholder, or hidden by the review predicate given, which is empty
// when nothing is hidden for its review status (the sweep, which removes
// what has expired and nothing else; an aggregate whose caller filters the
// status itself).
func (db *DB) hiddenPredicate(review string) string {
	where := db.expiredPredicate()
	if review != "" {
		where = `(` + where + ` OR ` + review + `)`
	}
	return where
}

// visiblePredicate is the negation of expiredPredicate, spelled out because
// a document with no expiry makes the expression NULL, and NOT NULL is not
// true: no expiry, an empty one, or one past the instant bound to the
// placeholder. The review gate is not part of it: the dedupe lookup, its
// one caller, is about content, and a proposed or rejected document still
// holds its content.
func (db *DB) visiblePredicate() string {
	expr := db.Dialect().JSONTextGuarded("metadata", expiresAtKey)
	return `(` + expr + ` IS NULL OR ` + expr + ` <= '' OR ` + expr + ` > ?)`
}

// recordOnlyKey reports whether a metadata key lives on the document record
// alone and is not copied onto its chunks or its graph node: the gates and
// the aggregates' status filter read the documents table, and a copy would
// go stale when knowledge_update changes the key and nothing else.
func recordOnlyKey(key string) bool {
	switch key {
	case expiresAtKey, reviewStatusKey, reviewedByKey, reviewedAtKey:
		return true
	}
	return false
}

// instant is the form of now the predicate compares against.
func instant(now time.Time) string { return now.UTC().Format(time.RFC3339) }

// hiddenDocuments returns, among the given document ids, or among every
// document when among is nil, those that have expired at now. It is one
// query per call, through the expression index, and nothing is kept across
// calls: a second process on the same file or server, and the sweep, see an
// expiry the moment it is written.
func (db *DB) hiddenDocuments(ctx context.Context, now time.Time, review string, among []string) (map[string]struct{}, error) {
	query := `SELECT id FROM documents WHERE ` + db.hiddenPredicate(review)
	args := []any{instant(now)}
	if among != nil {
		if len(among) == 0 {
			return map[string]struct{}{}, nil
		}
		placeholders, ids := sqlPlaceholders(among)
		query += ` AND id IN (` + placeholders + `)`
		args = append(args, ids...)
	}
	rows, err := db.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load expired documents: %w", err)
	}
	hidden := map[string]struct{}{}
	err = eachRow(rows, func() error {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan document expiry: %w", err)
		}
		hidden[id] = struct{}{}
		return nil
	})
	return hidden, err
}

// documentIDsOf lists the distinct, non-empty document ids a read path met,
// in the order it met them: what hiddenDocuments is asked about.
func documentIDsOf(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; id == "" || dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// hiddenAmong is hiddenDocuments for a read path, with the review status
// the filters ask for, plus the documents the caller's scope does not
// reach (outOfScopeDocuments). On a brain whose documents cannot be read it
// is advisory for the expiry, because the sweep is the guarantee, and closed
// under the review gate, a status filter or a caller scope, whose whole
// point is to hide, and which an outage must not lift: every document met
// is then hidden.
func (db *DB) hiddenAmong(ctx context.Context, filters *RetrievalFilters, ids []string) map[string]struct{} {
	want := ""
	if filters != nil {
		want = filters.ReviewStatus
	}
	ids = documentIDsOf(ids)
	hidden, err := db.hiddenDocuments(ctx, time.Now(), db.reviewHiddenPredicateFor(ctx, want), ids)
	if err != nil && !db.reviewGate && want == "" {
		hidden, err = map[string]struct{}{}, nil
	}
	if err == nil {
		var scoped map[string]struct{}
		scoped, err = db.outOfScopeDocuments(ctx, ids)
		maps.Copy(hidden, scoped)
	}
	if err != nil {
		hidden = make(map[string]struct{}, len(ids))
		for _, id := range ids {
			hidden[id] = struct{}{}
		}
	}
	return hidden
}

// outOfScopeDocuments returns, among the given document ids, those the
// caller's scope may not read. Without a caller scope, or under one with no
// knowledge rule, it reads nothing and hides nothing.
func (db *DB) outOfScopeDocuments(ctx context.Context, ids []string) (map[string]struct{}, error) {
	scope, ok := callerScopeOf(ctx)
	if !ok || scope.Knowledge == nil || len(ids) == 0 {
		return nil, nil
	}
	query, args := documentCollectionsQuery(ids)
	rows, err := db.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load document collections: %w", err)
	}
	collections := make(map[string][]string, len(ids))
	err = eachRow(rows, func() error {
		var id, name string
		err := rows.Scan(&id, &name)
		collections[id] = append(collections[id], name)
		return err
	})
	return outOfScope(ids, collections, scope.Knowledge), err
}

// documentCollectionsQuery reads the collection of every chunk of the given
// documents. The documents table has no collection: a document is in the
// collection of its chunks, on embeddings.collection_id.
func documentCollectionsQuery(ids []string) (string, []any) {
	placeholders, args := sqlPlaceholders(ids)
	return `SELECT e.doc_id, COALESCE(c.name, '') FROM embeddings e
		LEFT JOIN collections c ON c.id = e.collection_id
		WHERE e.doc_id IN (` + placeholders + `)`, args
}

// outOfScope is the scope rule on the collections each document's chunks
// are in: a document is out of scope when one of its chunks is in a
// collection permits refuses, or when it has no chunk, since then nothing
// says which collection it is in.
func outOfScope(ids []string, collections map[string][]string, permits func(collection string) bool) map[string]struct{} {
	out := map[string]struct{}{}
	for _, id := range ids {
		names := collections[id]
		if len(names) == 0 || slices.ContainsFunc(names, func(name string) bool { return !permits(name) }) {
			out[id] = struct{}{}
		}
	}
	return out
}

// allowDocument is allowDocumentID plus the expiry rule: a chunk of an
// expired document is left out of every retrieval path that reads chunks.
func allowDocument(filters *RetrievalFilters, hidden map[string]struct{}, documentID string) bool {
	_, isHidden := hidden[documentID]
	return allowDocumentID(filters, documentID) && !isHidden
}

// ExpiredSweepReport lists what a sweep removed, or would remove.
type ExpiredSweepReport struct {
	Now       time.Time `json:"now"`
	DryRun    bool      `json:"dry_run,omitempty"`
	Documents []string  `json:"documents"`
	Memories  []string  `json:"memories"`
}

// SweepExpired removes every document and memory whose expiry has passed at
// now: the record, its chunks, its graph nodes and their edges, as
// knowledge_delete and memory_delete do. A dry run lists them and removes
// nothing. Each document is deleted only if it is still expired when its
// delete runs, so one renewed after the listing is kept; the report names
// what was removed. A failure stops the sweep and the report says how far
// it went.
func (db *DB) SweepExpired(ctx context.Context, now time.Time, dryRun bool) (*ExpiredSweepReport, error) {
	now = now.UTC()
	report := &ExpiredSweepReport{Now: now, DryRun: dryRun, Documents: []string{}, Memories: []string{}}

	hidden, err := db.hiddenDocuments(ctx, now, "", nil)
	if err != nil {
		return nil, err
	}
	candidates := sortedKeys(hidden)
	report.Memories, err = db.expiredMemoryIDs(ctx, now)
	if err != nil {
		return nil, err
	}
	if dryRun {
		report.Documents = candidates
		return report, nil
	}

	for _, id := range candidates {
		deleted, err := db.sweepDocument(ctx, id, now)
		if err != nil {
			report.Memories = nil
			return report, fmt.Errorf("sweep document %s: %w", id, err)
		}
		if deleted {
			report.Documents = append(report.Documents, id)
		}
	}
	listed := report.Memories
	report.Memories = []string{}
	for _, id := range listed {
		deleted, err := db.sweepMemory(ctx, id, now)
		if err != nil {
			return report, fmt.Errorf("sweep memory %s: %w", id, err)
		}
		if deleted {
			report.Memories = append(report.Memories, id)
		}
	}
	return report, nil
}

// sweepMemory deletes one memory if its row still says it has expired at
// now, read just before the delete, and reports whether it did. A memory
// renewed (memory_update with a new ttl_seconds) or deleted since the
// listing is kept, or skipped, as a renewed document is.
func (db *DB) sweepMemory(ctx context.Context, id string, now time.Time) (bool, error) {
	row, err := db.loadMemoryRow(ctx, id)
	if errors.Is(err, core.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.record.ExpiresAt == nil || row.record.ExpiresAt.After(now) {
		return false, nil
	}
	if _, err := db.DeleteMemory(ctx, MemoryDeleteRequest{MemoryID: id}); err != nil {
		return false, err
	}
	return true, nil
}

// sweepDocument deletes one document if its record still says it has
// expired at now, in the transaction that deletes it, and reports whether
// it did.
func (db *DB) sweepDocument(ctx context.Context, id string, now time.Time) (bool, error) {
	err := db.cleanupKnowledgeArtifactsIf(ctx, id, `AND `+db.expiredPredicate(), instant(now))
	if errors.Is(err, errKept) {
		return false, nil
	}
	return err == nil, err
}

// expiredMemoryIDs lists the memories whose expires_at has passed at now,
// in creation order. A memory's expiry is the one memory_save sets from
// ttl_seconds, read the way recall reads it.
func (db *DB) expiredMemoryIDs(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := db.query(ctx, `
		SELECT m.id, m.metadata
		FROM messages m
		WHERE m.session_id LIKE 'memory:%'
		  AND m.metadata IS NOT NULL
		ORDER BY m.created_at, m.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list memory expiries: %w", err)
	}
	ids := []string{}
	err = eachRow(rows, func() error {
		var id string
		var metadataJSON []byte
		if err := rows.Scan(&id, &metadataJSON); err != nil {
			return fmt.Errorf("scan memory expiry: %w", err)
		}
		record := MemoryRecord{ID: id}
		if json.Unmarshal(metadataJSON, &record.Metadata) != nil {
			return nil // not a memory record; nothing to expire
		}
		applyMemoryMetadata(&record)
		if record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
			ids = append(ids, id)
		}
		return nil
	})
	return ids, err
}

// eachRow calls each for every row, then closes the rows and reports the
// error the iteration ended on, if any.
func eachRow(rows *sql.Rows, each func() error) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := each(); err != nil {
			return err
		}
	}
	return rows.Err()
}
