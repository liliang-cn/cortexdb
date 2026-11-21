package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// What a recall says about a fact that changed.
//
// A memory saved with supersedes retires the memories it names: they stay
// stored, stamped superseded_by and superseded_at, and recall stops returning
// them as current. That used to be all a reader learnt — the new wording,
// with no sign the value had ever been different, when it changed or who
// changed it. The stored chain is now read back: a hit that replaced other
// memories carries them in Replaces, oldest first; a memory read by id that
// was replaced names the memory that holds the current value (CurrentID);
// and as_of answers as the store stood at an earlier instant.
//
// What counts as a change is never guessed here. Supersession stays an
// explicit act of whoever saved the new memory (or of the capture's
// contradiction check); this file only makes the stored chain visible.

// MemoryReplaced is one memory a hit replaced, directly or through the
// memories it replaced in turn.
type MemoryReplaced struct {
	ID string `json:"id"`
	// Excerpt is the replaced memory's text, cut to its first
	// replacedExcerptRunes runes on one line: what the value was.
	Excerpt string `json:"excerpt"`
	// SavedAt is when the replaced memory was saved.
	SavedAt time.Time `json:"saved_at"`
	// SupersededAt is when it was replaced, from its superseded_at stamp;
	// zero when a store from before the stamp did not record it.
	SupersededAt time.Time `json:"superseded_at,omitempty"`
	// SupersededBy is the memory that replaced it: the hit itself for the
	// last step of the chain, an intermediate version for earlier ones.
	SupersededBy string `json:"superseded_by"`
	// WrittenBy and Source are the replaced memory's writer and source,
	// as it recorded them.
	WrittenBy string `json:"written_by,omitempty"`
	Source    string `json:"source,omitempty"`
}

const (
	// maxReplacesDepth bounds how many steps back a chain is followed, and
	// maxReplacesPerHit how many replaced memories one hit lists: a fact
	// changed often keeps its latest history, which is the part asked about.
	maxReplacesDepth  = 4
	maxReplacesPerHit = 6
	// maxCurrentHops bounds the walk forward from a replaced memory to the
	// one now current. A loop cannot be written through supersedes (a memory
	// cannot supersede itself, and the chain only ever points at newer
	// saves), but a hand-edited store can hold one.
	maxCurrentHops = 16
	// replacedExcerptRunes is how much of a replaced memory a hit carries.
	replacedExcerptRunes = 160
	// changeNotePrevRunes is how much of the previous value a context-pack
	// line quotes.
	changeNotePrevRunes = 90
)

type memoryAsOfKey struct{}

// withMemoryAsOf puts the as_of instant of a memory search into ctx. Empty
// leaves ctx as it is; anything but RFC 3339 is refused, never read as now.
func withMemoryAsOf(ctx context.Context, value string) (context.Context, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ctx, nil
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ctx, fmt.Errorf("as_of must be RFC 3339 (e.g. 2026-06-01T12:00:00Z): %w", err)
	}
	return context.WithValue(ctx, memoryAsOfKey{}, at.UTC()), nil
}

// memoryAsOf is the instant a memory search answers at; zero for now.
func memoryAsOf(ctx context.Context) time.Time {
	at, _ := ctx.Value(memoryAsOfKey{}).(time.Time)
	return at
}

// memorySupersededAt is when the memory was replaced, from its stamp, and
// whether it carries one at all.
func memorySupersededAt(record MemoryRecord) (time.Time, bool) {
	s, _ := stringFromAny(record.Metadata["superseded_at"])
	if s = strings.TrimSpace(s); s == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return at.UTC(), true
}

// memorySupersededAsOf reports whether the memory had been replaced at the
// instant at (now when zero). A memory replaced after it was still current
// then. One replaced without a recorded instant counts as replaced at any
// instant: when is unknown, and presenting it as current is the error the
// stamp exists to prevent.
func memorySupersededAsOf(record MemoryRecord, at time.Time) bool {
	if !memorySuperseded(record) {
		return false
	}
	if at.IsZero() {
		return true
	}
	when, ok := memorySupersededAt(record)
	return !ok || !when.After(at)
}

// memoryExpiredAsOf is memoryExpired at the instant at (now when zero).
func memoryExpiredAsOf(record MemoryRecord, at time.Time) bool {
	if at.IsZero() {
		return memoryExpired(record)
	}
	return record.ExpiresAt != nil && record.ExpiresAt.Before(at)
}

// memorySavedAfter reports whether the memory did not exist yet at the
// instant at. Never for a zero instant.
func memorySavedAfter(record MemoryRecord, at time.Time) bool {
	return !at.IsZero() && record.CreatedAt.After(at)
}

// replacedRow is a superseded memory as the chain lookup reads it.
type replacedRow struct {
	record MemoryRecord
	by     string
}

// replacedBy loads the memories replaced by any of ids: one query, matched on
// the superseded_by stamp.
func (db *DB) replacedBy(ctx context.Context, ids []string) ([]replacedRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	in, args := sqlPlaceholders(ids)
	// The expression is idx_messages_superseded_by's (pkg/core), spelled
	// exactly as indexed so the lookup is one.
	rows, err := db.query(ctx, fmt.Sprintf(`
		SELECT id, session_id, content, metadata, created_at
		FROM messages
		WHERE %s IN (%s) AND session_id LIKE 'memory:%%'`,
		db.Dialect().JSONTextGuarded("metadata", "superseded_by"), in), args...)
	if err != nil {
		return nil, fmt.Errorf("load replaced memories: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []replacedRow
	for rows.Next() {
		var r replacedRow
		var metadataJSON []byte
		if err := rows.Scan(&r.record.ID, &r.record.SessionID, &r.record.Content, &metadataJSON, &r.record.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan replaced memory: %w", err)
		}
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &r.record.Metadata); err != nil {
				continue
			}
		}
		applyMemoryMetadata(&r.record)
		r.by, _ = stringFromAny(r.record.Metadata["superseded_by"])
		out = append(out, r)
	}
	return out, rows.Err()
}

// attachReplaces fills each hit's Replaces from the stored chain: the
// memories it superseded, the ones those superseded, and so on up to
// maxReplacesDepth steps, oldest first. A replaced memory outside the
// caller's scope is left out, as it is from a read by id; under as_of so is
// one replaced after that instant, which was still current then.
//
// One query per step for every hit at once, and a recall whose hits replaced
// nothing — most of them — costs one.
func (db *DB) attachReplaces(ctx context.Context, hits []MemorySearchHit) error {
	if len(hits) == 0 {
		return nil
	}
	at := memoryAsOf(ctx)
	// root maps every id on a chain to the hits whose chain it is on.
	root := make(map[string][]int, len(hits))
	frontier := make([]string, 0, len(hits))
	for i, h := range hits {
		if _, seen := root[h.Memory.ID]; !seen {
			frontier = append(frontier, h.Memory.ID)
		}
		root[h.Memory.ID] = append(root[h.Memory.ID], i)
	}
	visited := map[string]bool{}
	for _, id := range frontier {
		visited[id] = true
	}
	found := make([][]MemoryReplaced, len(hits))
	permitted := map[string]bool{}
	for depth := 0; depth < maxReplacesDepth && len(frontier) > 0; depth++ {
		rows, err := db.replacedBy(ctx, frontier)
		if err != nil {
			return err
		}
		frontier = frontier[:0]
		for _, r := range rows {
			if visited[r.record.ID] {
				continue
			}
			if !at.IsZero() && (memorySavedAfter(r.record, at) || !memorySupersededAsOf(r.record, at)) {
				continue
			}
			ok, seen := permitted[r.record.SessionID]
			if !seen {
				var err error
				if ok, err = db.memoryReadPermitted(ctx, r.record.SessionID); err != nil {
					return err
				}
				permitted[r.record.SessionID] = ok
			}
			if !ok {
				continue
			}
			visited[r.record.ID] = true
			entry := replacedEntry(r)
			for _, i := range root[r.by] {
				found[i] = append(found[i], entry)
				root[r.record.ID] = append(root[r.record.ID], i)
			}
			frontier = append(frontier, r.record.ID)
		}
	}
	for i := range hits {
		chain := found[i]
		if len(chain) == 0 {
			continue
		}
		// Newest first to keep the latest history under the cap, then
		// oldest first, the order a reader follows a change in.
		sort.SliceStable(chain, func(a, b int) bool { return replacedLater(chain[a], chain[b]) })
		if len(chain) > maxReplacesPerHit {
			chain = chain[:maxReplacesPerHit]
		}
		slices.Reverse(chain)
		hits[i].Replaces = chain
	}
	return nil
}

func replacedEntry(r replacedRow) MemoryReplaced {
	when, _ := memorySupersededAt(r.record)
	return MemoryReplaced{
		ID:           r.record.ID,
		Excerpt:      firstLine(r.record.Content, replacedExcerptRunes),
		SavedAt:      r.record.CreatedAt.UTC(),
		SupersededAt: when,
		SupersededBy: r.by,
		WrittenBy:    metaString(r.record.Metadata, metadataWrittenBy),
		Source:       metaString(r.record.Metadata, "source", "agent"),
	}
}

// replacedLater orders replaced memories by when they stopped being current,
// then by when they were saved. A memory whose change time is unknown is
// placed by when it was saved: it stopped being current after that.
func replacedLater(a, b MemoryReplaced) bool {
	if ka, kb := replacedOrderTime(a), replacedOrderTime(b); !ka.Equal(kb) {
		return ka.After(kb)
	}
	if !a.SavedAt.Equal(b.SavedAt) {
		return a.SavedAt.After(b.SavedAt)
	}
	return a.ID > b.ID
}

func replacedOrderTime(r MemoryReplaced) time.Time {
	if r.SupersededAt.IsZero() {
		return r.SavedAt
	}
	return r.SupersededAt
}

// fillCurrentID follows a replaced memory's superseded_by forward to the
// memory now current and names it in CurrentID, with the first step in
// SupersededBy. A record nobody replaced is left as it is. A step the
// caller's scope does not reach, or one deleted since, ends the walk where
// it is: the last memory reached is the current one the caller can read.
func (db *DB) fillCurrentID(ctx context.Context, record *MemoryRecord) error {
	next, _ := stringFromAny(record.Metadata["superseded_by"])
	next = strings.TrimSpace(next)
	if next == "" {
		return nil
	}
	record.SupersededBy = next
	current := ""
	seen := map[string]bool{record.ID: true}
	for hop := 0; hop < maxCurrentHops && next != "" && !seen[next]; hop++ {
		seen[next] = true
		row, err := db.loadMemoryRow(ctx, next)
		if err != nil {
			break
		}
		if ok, err := db.memoryReadPermitted(ctx, row.record.SessionID); err != nil {
			return err
		} else if !ok {
			break
		}
		current = next
		next, _ = stringFromAny(row.record.Metadata["superseded_by"])
		next = strings.TrimSpace(next)
	}
	record.CurrentID = current
	return nil
}

// memoryChangeNote is what a context-pack line adds for a memory that
// replaced others: the date of the last change and the value before it, and
// how many versions came before that. Empty for a memory that replaced
// nothing.
func memoryChangeNote(hit MemorySearchHit) string {
	n := len(hit.Replaces)
	if n == 0 {
		return ""
	}
	prev := hit.Replaces[n-1]
	when := prev.SupersededAt
	if when.IsZero() {
		when = hit.Memory.CreatedAt
	}
	date := ""
	if !when.IsZero() {
		date = " " + when.UTC().Format("2006-01-02")
	}
	earlier := ""
	if n > 1 {
		earlier = fmt.Sprintf("; %d earlier", n-1)
	}
	return fmt.Sprintf(" [changed%s; before: %q%s]", date, firstLine(prev.Excerpt, changeNotePrevRunes), earlier)
}
