package connector

import (
	"os"
	"testing"
)

func TestPostgresCDCSourceRequiresPublication(t *testing.T) {
	_, err := NewPostgresCDCSource("postgres://x/y", PostgresCDCOptions{})
	if err == nil {
		t.Fatal("expected error when Publication is empty")
	}
}

func TestPostgresCDCSourceConnects(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_PG_REPL_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_PG_REPL_DSN to a wal_level=logical Postgres (publication + slot auto-created)")
	}
	src, err := NewPostgresCDCSource(dsn, PostgresCDCOptions{
		Publication: "cdc_pub", Slot: "cdc_slot", Tables: map[string][]string{"cdc_repl_users": {"id"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
