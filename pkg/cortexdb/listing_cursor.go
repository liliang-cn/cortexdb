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

// encodeListingCursor packs a sort key into a cursor value.
func encodeListingCursor(ts time.Time, id string) string {
	raw := ts.UTC().Format(time.RFC3339Nano) + "\x00" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeListingCursor unpacks a cursor. A malformed value is an error rather
// than a silent restart: a resumed walk that quietly began again would return
// duplicates and look complete.
func decodeListingCursor(cursor string) (time.Time, string, error) {
	if strings.TrimSpace(cursor) == "" {
		return time.Time{}, "", fmt.Errorf("cortexdb: empty listing cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("cortexdb: malformed listing cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("cortexdb: malformed listing cursor: missing separator")
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("cortexdb: malformed listing cursor timestamp: %w", err)
	}
	return ts, parts[1], nil
}
