package connector

import (
	"os"
	"testing"
)

func TestMySQLBinlogSourceRequiresDSN(t *testing.T) {
	if _, err := NewMySQLBinlogSource("", MySQLBinlogOptions{ServerID: 1001}); err == nil {
		t.Fatal("expected error for empty DSN")
	}
}

func TestMySQLBinlogSourceConnects(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_MYSQL_BINLOG_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_MYSQL_BINLOG_DSN to a ROW-binlog MySQL (e.g. root:p@tcp(localhost:3306)/test)")
	}
	src, err := NewMySQLBinlogSource(dsn, MySQLBinlogOptions{
		ServerID: 1001, Tables: map[string][]string{"cdc_binlog_users": {"id"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
