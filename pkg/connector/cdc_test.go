package connector

import (
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestChangeOpString(t *testing.T) {
	if OpInsert.String() != "insert" || OpUpdate.String() != "update" || OpDelete.String() != "delete" {
		t.Fatalf("op strings: %q %q %q", OpInsert, OpUpdate, OpDelete)
	}
}

func TestChangeEventConstruction(t *testing.T) {
	e := ChangeEvent{
		Op:    OpInsert,
		Table: "users",
		Key:   map[string]string{"id": "1"},
		Row:   importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Bob"}},
	}
	if e.Op != OpInsert || e.Table != "users" || e.Key["id"] != "1" || e.Row.Values["name"] != "Bob" {
		t.Fatalf("bad event: %+v", e)
	}
}
