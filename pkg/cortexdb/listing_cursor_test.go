package cortexdb

import (
	"encoding/base64"
	"strings"
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
	if strings.Contains(enc, "some:id") {
		t.Errorf("cursor leaks its contents: %q", enc)
	}
}

func TestListingCursorRejectsGarbage(t *testing.T) {
	// Each input must reach a *different* rejection branch. Getting this wrong
	// is easy: base64.RawURLEncoding refuses "=" padding, so a padded string
	// dies at the decode step and never reaches the checks after it.
	validTimestamp := time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	cases := []struct {
		name   string
		cursor string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"not base64", "not-base64!!"},
		{"padded base64 is not raw base64", "YWJjZA=="},
		{"has valid timestamp but missing separator", base64.RawURLEncoding.EncodeToString([]byte(validTimestamp))},
		{"separator present but timestamp is junk", base64.RawURLEncoding.EncodeToString([]byte("not-a-timestamp\x00some:id"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := decodeListingCursor(tc.cursor); err == nil {
				t.Errorf("accepted %q", tc.cursor)
			}
		})
	}
}

func TestListingCursorCarriesAnIdWithNoTimestamp(t *testing.T) {
	// The graph listing orders by id alone, so it encodes a zero time and reads
	// back only the id. That has to keep working: a later change to the encoding
	// that special-cased the zero time would break a caller silently.
	enc := encodeListingCursor(time.Time{}, "entity:athanor")
	ts, id, err := decodeListingCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if id != "entity:athanor" {
		t.Errorf("id: want entity:athanor, got %q", id)
	}
	if !ts.IsZero() {
		t.Errorf("timestamp should have stayed zero, got %v", ts)
	}
}

func TestListingCursorRoundTripsARealClockReading(t *testing.T) {
	// time.Now() carries a monotonic reading and a local location; the memory
	// listing's key is created_at, so the instant has to survive exactly.
	for _, ts := range []time.Time{
		time.Now(),
		time.Date(2026, 3, 8, 2, 30, 0, 1, time.FixedZone("Chatham", 12*3600+45*60)),
	} {
		enc := encodeListingCursor(ts, "m1")
		got, _, err := decodeListingCursor(enc)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !got.Equal(ts) {
			t.Errorf("instant changed: want %v, got %v", ts, got)
		}
	}
}
