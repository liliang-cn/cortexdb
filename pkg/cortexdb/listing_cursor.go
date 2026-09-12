package cortexdb

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// A listing cursor is an opaque resume point for a bulk read: the sort key of
// the last row a page scanned. It is keyset, never an offset — a shared brain
// is written while it is read, and an offset silently skips records when rows
// land behind it.
//
// Opaque is a contract, not decoration. A caller that parsed this and did
// arithmetic on it would be holding a lock on a detail we need to change.

// listingCursorKind identifies which listing issued a cursor. graph_list_all
// and memory_list_all both expose a `cursor` field described identically as
// an opaque string, so an agent mixing them up is a realistic mistake — one
// that must fail loudly rather than walk the wrong table. The kind travels
// inside the encoded value (never as a visible prefix), so the cursor stays
// opaque while still being self-describing enough to reject a cross-fed read.
type listingCursorKind string

const (
	listingCursorKindMemory listingCursorKind = "memory"
	listingCursorKindGraph  listingCursorKind = "graph"
)

// encodeListingCursor packs a sort key into a cursor value, tagged with the
// kind of listing that produced it.
func encodeListingCursor(kind listingCursorKind, ts time.Time, id string) string {
	raw := string(kind) + "\x00" + ts.UTC().Format(time.RFC3339Nano) + "\x00" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeListingCursor unpacks a cursor produced for the given kind. A
// malformed value is an error rather than a silent restart: a resumed walk
// that quietly began again would return duplicates and look complete. A
// cursor produced by a *different* listing is rejected the same way — a
// cross-fed cursor that silently matched nothing, or silently walked the
// wrong ordering, would be a second version of the exact bug this file exists
// to prevent.
func decodeListingCursor(kind listingCursorKind, cursor string) (time.Time, string, error) {
	if strings.TrimSpace(cursor) == "" {
		return time.Time{}, "", fmt.Errorf("cortexdb: empty listing cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("cortexdb: malformed listing cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "\x00", 3)
	if len(parts) != 3 {
		return time.Time{}, "", fmt.Errorf("cortexdb: malformed listing cursor: missing separator")
	}
	gotKind, tsPart, idPart := listingCursorKind(parts[0]), parts[1], parts[2]
	if gotKind != kind {
		return time.Time{}, "", fmt.Errorf("cortexdb: listing cursor is for %q, not %q", gotKind, kind)
	}
	ts, err := time.Parse(time.RFC3339Nano, tsPart)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("cortexdb: malformed listing cursor timestamp: %w", err)
	}
	return ts, idPart, nil
}
