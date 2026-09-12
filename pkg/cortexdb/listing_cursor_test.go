package cortexdb

import (
	"testing"
	"time"
)

func TestListingCursorRoundTrips(t *testing.T) {
	ts := time.Date(2026, 9, 12, 10, 30, 0, 123456789, time.UTC)
	enc := encodeListingCursor(ts, "memory:global:default")

	gotTS, gotID, err := decodeListingCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gotTS.Equal(ts) {
		t.Errorf("timestamp: want %v, got %v", ts, gotTS)
	}
	if gotID != "memory:global:default" {
		t.Errorf("id: want memory:global:default, got %q", gotID)
	}
}

func TestListingCursorIsOpaque(t *testing.T) {
	// A caller must not be able to read an offset out of it and start doing
	// arithmetic on it — the value is ours to change.
	enc := encodeListingCursor(time.Now(), "some:id")
	if enc == "" {
		t.Fatal("empty cursor")
	}
	if containsAny(enc, []string{"some:id", ":"}) {
		t.Errorf("cursor leaks its contents: %q", enc)
	}
}

func TestListingCursorRejectsGarbage(t *testing.T) {
	// A malformed cursor must be an error, never silently "start from the
	// beginning" — that would make a resumed walk quietly return duplicates.
	for _, bad := range []string{"not-base64!!", "", "YWJjZA=="} {
		if _, _, err := decodeListingCursor(bad); err == nil {
			t.Errorf("accepted garbage cursor %q", bad)
		}
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
