package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Bulk memory listing, for the views that need every record rather than a
// search result: the HTML dashboard and the Markdown export.
//
// Without this the two one-shot modes can only open a local database file,
// which on a machine pointed at a shared brain is the wrong one — they would
// render a file nothing writes to any more.

// MemoryListAllRequest asks for a page of stored memories.
type MemoryListAllRequest struct {
	// Limit caps how many records come back (0 = defaultMemoryListLimit).
	Limit int `json:"limit,omitempty"`
	// Cursor resumes a walk after the row a previous page stopped on. Empty
	// starts from the newest record. The value is opaque; pass back exactly
	// what NextCursor gave.
	Cursor string `json:"cursor,omitempty"`
}

// MemoryListAllResponse carries a page and how to get the next one.
type MemoryListAllResponse struct {
	Memories []MemoryRecord `json:"memories"`
	// Truncated is true when more records remain. It never stands alone:
	// whenever it is set, NextCursor says how to continue. A listing that
	// admitted it stopped and offered no way onward is what stranded a brain
	// that had grown past one message.
	Truncated bool `json:"truncated,omitempty"`
	// NextCursor is the resume point for the next page, set whenever
	// Truncated is.
	NextCursor string `json:"next_cursor,omitempty"`
}

// defaultMemoryListLimit is deliberately well under what one gRPC message
// carries. It used to be 5000 while the 4 MiB transport gave out near 1100 —
// the default was a promise the transport could not keep. The cursor, not a
// large limit, is how a caller gets everything.
const defaultMemoryListLimit = 500

// sqliteTimestampLayout matches the text SQLite's CURRENT_TIMESTAMP writes:
// "YYYY-MM-DD HH:MM:SS" in UTC, second precision, no zone suffix. The keyset
// predicate below binds the cursor's timestamp as a string in this exact
// layout rather than handing the driver a time.Time — see the note on
// listMemoryPage for why that distinction matters.
const sqliteTimestampLayout = "2006-01-02 15:04:05"

// ListAllMemoriesPaged returns one page of memories, newest first.
func (db *DB) ListAllMemoriesPaged(ctx context.Context, req MemoryListAllRequest) (*MemoryListAllResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultMemoryListLimit
	}

	var (
		afterTS time.Time
		afterID string
		resume  bool
	)
	if req.Cursor != "" {
		ts, id, err := decodeListingCursor(req.Cursor)
		if err != nil {
			return nil, err
		}
		afterTS, afterID, resume = ts, id, true
	}

	resp := &MemoryListAllResponse{Memories: []MemoryRecord{}}
	for len(resp.Memories) <= limit {
		// Over-fetch by one so a full page can tell "exactly enough" from
		// "there is more", without a second query.
		batch, lastTS, lastID, err := db.listMemoryPage(ctx, afterTS, afterID, resume, limit+1)
		if err != nil {
			return nil, err
		}
		for _, rec := range batch {
			if memoryExpired(rec) {
				continue
			}
			resp.Memories = append(resp.Memories, rec)
		}
		if len(batch) < limit+1 {
			// The table is exhausted; whatever survived filtering is the rest.
			if len(resp.Memories) > limit {
				resp.Memories = resp.Memories[:limit]
				resp.Truncated = true
				last := resp.Memories[limit-1]
				resp.NextCursor = encodeListingCursor(last.CreatedAt, last.ID)
			}
			return resp, nil
		}
		// Resume from the last row *scanned*, not the last kept: expired rows
		// must not be rescanned on every page.
		afterTS, afterID, resume = lastTS, lastID, true
	}

	resp.Memories = resp.Memories[:limit]
	resp.Truncated = true
	last := resp.Memories[limit-1]
	resp.NextCursor = encodeListingCursor(last.CreatedAt, last.ID)
	return resp, nil
}

// listMemoryPage reads one SQL page in the listing's order, returning the rows
// and the sort key of the last one scanned.
func (db *DB) listMemoryPage(ctx context.Context, afterTS time.Time, afterID string, resume bool, limit int) ([]MemoryRecord, time.Time, string, error) {
	const base = `
		SELECT m.id, m.session_id, s.user_id, m.role, m.content, m.metadata, m.created_at
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE m.session_id LIKE 'memory:%'`
	// The listing orders created_at DESC but id ASC, so the keyset predicate
	// has to match that mixed direction exactly or a page boundary that lands
	// inside one timestamp will skip or repeat the rest of it.
	const tail = `
		ORDER BY m.created_at DESC, m.id
		LIMIT ?`

	var (
		rows *sql.Rows
		err  error
	)
	if resume {
		// created_at is written by `INSERT ... VALUES (..., CURRENT_TIMESTAMP)`
		// and modernc.org/sqlite stores that as the literal text SQLite's
		// CURRENT_TIMESTAMP produces: "YYYY-MM-DD HH:MM:SS" UTC, no fractional
		// seconds, no zone suffix. If afterTS were bound as a bare time.Time,
		// the driver formats it with time.Time.String() instead — e.g.
		// "2026-09-12 12:34:56 +0000 UTC" — and that string is never equal,
		// nor lexically consistent, with the column's stored text: since the
		// stored value is a strict prefix of the driver's formatted value,
		// SQLite's text comparison ranks the stored row as "less than" a
		// cutoff drawn from its own timestamp, so `m.created_at < ?` matches
		// every row sharing that instant and `m.created_at = ?` never matches
		// at all. That silently breaks the id tie-break and reintroduces
		// duplicates precisely when multiple memories share a created_at
		// second, which happens often in fast writes. Formatting the cursor's
		// timestamp into the same layout the column already holds keeps the
		// comparison textual on both sides.
		afterTSParam := afterTS.UTC().Format(sqliteTimestampLayout)
		rows, err = db.query(ctx, base+`
		  AND (m.created_at < ? OR (m.created_at = ? AND m.id > ?))`+tail,
			afterTSParam, afterTSParam, afterID, limit)
	} else {
		rows, err = db.query(ctx, base+tail, limit)
	}
	if err != nil {
		return nil, time.Time{}, "", fmt.Errorf("list memories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		out    []MemoryRecord
		lastTS time.Time
		lastID string
	)
	for rows.Next() {
		var record MemoryRecord
		var metadataJSON []byte
		var createdAt time.Time
		if err := rows.Scan(&record.ID, &record.SessionID, &record.UserID, &record.Role, &record.Content, &metadataJSON, &createdAt); err != nil {
			return nil, time.Time{}, "", fmt.Errorf("scan memory: %w", err)
		}
		record.CreatedAt = createdAt
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &record.Metadata); err != nil {
				return nil, time.Time{}, "", fmt.Errorf("decode memory metadata: %w", err)
			}
		}
		applyMemoryMetadata(&record)
		if record.Scope == "" {
			record.Scope = scopeFromBucketID(record.SessionID)
		}
		out = append(out, record)
		lastTS, lastID = createdAt, record.ID
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, "", err
	}
	return out, lastTS, lastID, nil
}
