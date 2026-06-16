package connector

import (
	"context"
	"os"
	"testing"
)

func TestPostgresSourceIntrospect(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_PG_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_PG_DSN to run (e.g. postgres://user:pass@localhost:5432/db?sslmode=disable)")
	}
	src, err := NewPostgresSource(dsn, SourceOptions{SampleSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	schemas, err := src.Schemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) == 0 {
		t.Fatal("no tables introspected")
	}
	for _, s := range schemas {
		if len(s.Columns) == 0 {
			t.Fatalf("table %s has no columns", s.Table)
		}
	}
}
