package core

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// The whole point of the registry: one DSN decides the backend, and a path
// that worked before this existed still works.
func TestADSNPicksTheBackend(t *testing.T) {
	for _, name := range []string{"sqlite", "postgres"} {
		found := false
		for _, have := range RegisteredStores() {
			if have == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is not registered; have %v", name, RegisteredStores())
		}
	}

	path := fmt.Sprintf("test_registry_%d.db", testname.Nano())
	defer os.Remove(path)

	store, err := OpenStore(path, Config{VectorDim: 4})
	if err != nil {
		t.Fatalf("a bare path should open SQLite: %v", err)
	}
	defer store.Close()
	if _, ok := store.(*SQLiteStore); !ok {
		t.Errorf("a bare path opened %T, want *SQLiteStore", store)
	}

	pg, err := OpenStore("postgres://user:pw@localhost:5432/cortex", Config{VectorDim: 4})
	if err != nil {
		t.Fatalf("a postgres URL should open the PostgreSQL store: %v", err)
	}
	defer pg.Close()
	if _, ok := pg.(*PostgresStore); !ok {
		t.Errorf("a postgres URL opened %T, want *PostgresStore", pg)
	}
	// Note it did not connect: sql.Open is lazy, so choosing a backend does
	// not require the database to be up. Init is where it has to be.
}

// Registration must not be silently replaceable, or which backend a process
// uses depends on package initialisation order.
func TestARegisteredNameCannotBeQuietlyReplaced(t *testing.T) {
	name := fmt.Sprintf("test-backend-%d", testname.Nano())
	factory := func(string, Config) (Store, error) { return nil, nil }

	if err := RegisterStore(name, factory); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	err := RegisterStore(name, factory)
	if err == nil {
		t.Fatal("a second registration under the same name was accepted")
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("the refusal does not name the backend: %v", err)
	}

	if err := RegisterStore("", factory); err == nil {
		t.Error("an unnamed backend was accepted")
	}
	if err := RegisterStore("nil-factory", nil); err == nil {
		t.Error("a nil factory was accepted")
	}
}

// Opening through the registry has to produce a store that actually works,
// not just one of the right type.
func TestAStoreOpenedByDSNWorks(t *testing.T) {
	path := fmt.Sprintf("test_registry_live_%d.db", testname.Nano())
	defer os.Remove(path)

	store, err := OpenStore(path, Config{VectorDim: 4})
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Upsert(ctx, &Embedding{
		ID: "one", Vector: []float32{1, 0, 0, 0}, Content: "hello",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := store.Search(ctx, []float32{1, 0, 0, 0}, SearchOptions{TopK: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Content != "hello" {
		t.Fatalf("Search returned %+v", got)
	}
}
