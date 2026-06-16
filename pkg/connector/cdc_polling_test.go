package connector

import (
	"context"
	"os"
	"testing"
)

func TestPollingChangeSourcePostgres(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_PG_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_PG_DSN (table: cdc_users(id int pk, name text, updated_at timestamptz))")
	}
	ctx := context.Background()
	src, err := NewPollingChangeSource("postgres", dsn, PollingOptions{
		Tables:    []TableCursor{{Table: "cdc_users", CursorColumn: "updated_at", KeyColumns: []string{"id"}}},
		BatchSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	var events []ChangeEvent
	if err := src.Changes(ctx, func(e ChangeEvent) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Op != OpInsert && e.Op != OpUpdate {
			t.Fatalf("polling must emit insert/update only, got %v", e.Op)
		}
		if e.Key["id"] == "" {
			t.Fatalf("event missing key: %+v", e)
		}
	}
}
