package connector

import (
	"context"
	"os"
	"testing"
)

func TestMySQLSourceIntrospect(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_MYSQL_DSN to run (e.g. user:pass@tcp(localhost:3306)/db)")
	}
	src, err := NewMySQLSource(dsn, SourceOptions{SampleSize: 3})
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
}
