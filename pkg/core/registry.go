package core

// Choosing a backend with a connection string.
//
// Two real implementations now exist — SQLiteStore and PostgresStore — so the
// shape they have in common is something that was discovered rather than
// imagined. That order matters: an interface designed before the second
// implementation would have missed the things only writing one teaches, like
// pgvector refusing to index past 2000 dimensions, or SQLite enforcing a
// foreign key its counterpart has no table for.
//
// The registry is deliberately compile-time, not a plugin system. Storage is
// the hot path — every recall goes through it, and a process boundary would
// cost an IPC round trip and serialization per search, on top of making
// transactions impossible. Out-of-process plugins are the right shape for
// tools, which are called occasionally and by third parties; a storage backend
// is called constantly and by one author.
//
// The same shape as agent-go's RegisterMemoryStore, on purpose: a host that
// already knows how to swap a memory store there has nothing new to learn
// here, and a DSN is the only thing that changes.

import (
	"database/sql"
	"fmt"
	"sort"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver, for postgres DSNs

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// StoreFactory opens one backend from a DSN.
type StoreFactory func(dsn string, config Config) (Store, error)

var (
	storesMu sync.RWMutex
	stores   = map[string]StoreFactory{}
)

// RegisterStore adds a backend under a name.
//
// Refuses to replace one that is already registered: a silent replacement
// would mean the backend a process uses depends on package initialisation
// order, which is not something anybody wants to debug.
func RegisterStore(name string, factory StoreFactory) error {
	if name == "" || factory == nil {
		return fmt.Errorf("register store: name and factory are both required")
	}
	storesMu.Lock()
	defer storesMu.Unlock()
	if _, exists := stores[name]; exists {
		return fmt.Errorf("register store: %q is already registered", name)
	}
	stores[name] = factory
	return nil
}

// RegisteredStores lists the backends this binary was built with, sorted.
func RegisteredStores() []string {
	storesMu.RLock()
	defer storesMu.RUnlock()
	names := make([]string, 0, len(stores))
	for name := range stores {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// OpenStore opens the backend a DSN asks for.
//
//	/var/lib/cortexdb/brain.db                 -> SQLite
//	postgres://user:pw@host:5432/cortex        -> PostgreSQL + pgvector
//
// A bare path has always meant a SQLite file here, so it still does: an
// existing configuration keeps working without being told about any of this.
// The caller still has to call Init.
func OpenStore(dsn string, config Config) (Store, error) {
	name := string(sqldialect.KindForDSN(dsn))
	storesMu.RLock()
	factory, ok := stores[name]
	storesMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no %q store registered (have: %v)", name, RegisteredStores())
	}
	return factory(dsn, config)
}

func init() {
	// The default, and the one a bare path selects.
	_ = RegisterStore(string(sqldialect.SQLite), func(dsn string, config Config) (Store, error) {
		if dsn == "" {
			return nil, fmt.Errorf("sqlite: a database path is required")
		}
		config.Path = dsn
		return NewWithConfig(config)
	})

	// Opens its own pool from the DSN. A caller that wants to own the pool —
	// to set its own limits, or to share it with the graph store — builds
	// NewPostgresStore directly instead.
	_ = RegisterStore(string(sqldialect.Postgres), func(dsn string, config Config) (Store, error) {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, fmt.Errorf("postgres: %w", err)
		}
		return NewPostgresStore(db, config), nil
	})
}

// OpenBrainStore opens a store that can back a brain.
//
// Same DSNs as OpenStore, one extra requirement: the backend has to implement
// everything cortexdb.DB reaches for, not only the vector contract. A backend
// that opens but cannot be a brain is refused here, by name, rather than
// panicking on a type assertion somewhere in the middle of a request.
func OpenBrainStore(dsn string, config Config) (BrainStore, error) {
	store, err := OpenStore(dsn, config)
	if err != nil {
		return nil, err
	}
	brain, ok := store.(BrainStore)
	if !ok {
		_ = store.Close()
		return nil, fmt.Errorf(
			"the %s store cannot back a brain yet: it implements Store but not BrainStore",
			sqldialect.KindForDSN(dsn))
	}
	return brain, nil
}
