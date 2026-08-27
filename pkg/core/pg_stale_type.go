package core

// Surviving a type that was replaced underneath a live connection.
//
// pgx keeps a per-connection cache of prepared statements, and a statement's
// descriptor names the OIDs of its parameter and result types. `vector` is not
// a built-in — it belongs to an extension — so its OID is assigned when the
// extension is created and a *new* one is assigned if it is ever dropped and
// created again. A restore, an extension reinstall, or a test suite sharing the
// database will do exactly that, and any connection still holding a cached
// statement then asks the server about an OID that no longer exists:
//
//	ERROR: cache lookup failed for type 91164 (SQLSTATE XX000)
//
// The error names an integer and nothing else — not the type, not the table,
// not the statement — which is why it took a reproduction to identify rather
// than a reading.
//
// Two things make this worth handling rather than propagating. It is raised
// while the server resolves the statement's types, before the statement runs,
// so nothing has happened yet. And pgx drops the offending entry from its cache
// on the way out, so the very next attempt on that same connection prepares
// afresh and succeeds. One retry is the whole fix.
//
// Applied to reads only. A write that failed this way is equally safe to
// repeat — the statement never executed — but "equally safe" is a claim about
// every write path rather than about this error, and a read is where the
// failure was actually seen.

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// staleTypeCacheSQLStates are the two ways a connection reports that what it
// remembered about a statement no longer matches the catalog.
//
// XX000 (internal_error) carries "cache lookup failed for type N" — a
// parameter or operator operand whose type is gone. 0A000
// (feature_not_supported) carries "cached plan must not change result type",
// which is the same situation seen from the result side: it arrives when a
// selected column's type changed rather than a bound parameter's.
var staleTypeCacheSQLStates = map[string]string{
	"XX000": "cache lookup failed for type",
	"0A000": "cached plan must not change result type",
}

// IsStaleTypeCache reports whether err is a connection whose cached statement
// refers to a type the catalog no longer has under that OID.
//
// Matched on SQLSTATE *and* message text: XX000 is PostgreSQL's catch-all
// internal error and 0A000 covers plenty of unrelated unsupported features, so
// either code alone would swallow errors that deserve to surface.
func IsStaleTypeCache(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	want, ok := staleTypeCacheSQLStates[pgErr.Code]
	if !ok {
		return false
	}
	return strings.Contains(strings.ToLower(pgErr.Message), want)
}

// retryOnStaleTypeCache runs fn, and runs it once more if the first attempt
// failed only because the connection's statement cache had gone stale.
//
// Once, not in a loop: the first failure is what clears the cache, so a second
// failure means something other than a replaced type and looping would only
// delay reporting it.
func retryOnStaleTypeCache(fn func() error) error {
	err := fn()
	if IsStaleTypeCache(err) {
		return fn()
	}
	return err
}
