package connector

import (
	"testing"
	"time"
)

// valueToString must render time.Time as RFC3339 so the polling cursor watermark
// round-trips as a SQL timestamp literal. The default %v format ("... +0800 CST")
// is not parseable by Postgres when bound back into WHERE cursor > $1.
func TestValueToStringTimeRoundTrips(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	ts := time.Date(2026, 6, 16, 15, 36, 0, 829202000, loc)
	got := valueToString(ts)
	if _, err := time.Parse(time.RFC3339Nano, got); err != nil {
		t.Fatalf("valueToString(time.Time)=%q is not RFC3339-parseable: %v", got, err)
	}
	if got == ts.String() {
		t.Fatalf("valueToString must not use Go's default time format: %q", got)
	}
}

func TestValueToStringBytesAndString(t *testing.T) {
	if valueToString([]byte("hi")) != "hi" || valueToString("hi") != "hi" {
		t.Fatal("bytes/string passthrough broken")
	}
}
