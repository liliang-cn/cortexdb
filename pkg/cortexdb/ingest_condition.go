package cortexdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// ErrPreconditionFailed is what a conditional write returns when its
// condition does not hold: nothing was written.
var ErrPreconditionFailed = errors.New("cortexdb: precondition failed")

// ErrConflictingConditions is what an ingest returns when it gives both
// if_absent and if_sha256: a document is either new or the one read.
var ErrConflictingConditions = errors.New("cortexdb: invalid request: if_absent and if_sha256 are not given together")

// ingestCondition is the condition of a conditional ingest: absent, the
// brain has no document of the id; sha256, the stored document's content
// hash is this one.
type ingestCondition struct {
	absent bool
	sha256 string
}

// newIngestCondition reads the condition of a request, nil when it has none.
func newIngestCondition(req ToolIngestDocumentRequest) (*ingestCondition, error) {
	if req.IfAbsent && req.IfSHA256 != "" {
		return nil, ErrConflictingConditions
	}
	if !req.IfAbsent && req.IfSHA256 == "" {
		return nil, nil
	}
	return &ingestCondition{absent: req.IfAbsent, sha256: req.IfSHA256}, nil
}

// holds says whether the condition holds for the stored document: found
// false when the brain has none, hash its content hash otherwise.
func (c *ingestCondition) holds(id string, found bool, hash string) error {
	switch {
	case c.absent && found:
		return fmt.Errorf("%w: document %q exists (if_absent)", ErrPreconditionFailed, id)
	case c.sha256 != "" && !found:
		return fmt.Errorf("%w: document %q is not stored (if_sha256)", ErrPreconditionFailed, id)
	case c.sha256 != "" && hash != c.sha256:
		// A document stored without a content hash matches no hash. The
		// refusal names the stored hash, so the caller can re-read and retry.
		return fmt.Errorf("%w: document %q is stored with content %q, not %q (if_sha256)", ErrPreconditionFailed, id, hash, c.sha256)
	}
	return nil
}

// documentLockSpace is the first key of the PostgreSQL advisory lock on a
// document id ("CDID"). The two-key form keeps it apart from the one-key
// lock on a content hash (contentHolderTx) and from the change feed's space.
const documentLockSpace int32 = 0x43444944

// lockDocumentIDTx makes the transactions that write or delete one document
// id wait for each other on PostgreSQL, so a condition read under the lock
// still holds at the write: every knowledge write and delete takes it first,
// before the content hash lock. SQLite needs nothing: a transaction there
// already holds the process's write turn (core/sqlite_writelock.go).
func (db *DB) lockDocumentIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	if db.Dialect().Kind() != sqldialect.Postgres {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1, hashtext($2))`, documentLockSpace, id); err != nil {
		return fmt.Errorf("lock document id: %w", err)
	}
	return nil
}

// checkIngestConditionTx checks the condition inside the write transaction,
// after lockDocumentIDTx. The read sees every stored row, whatever the
// search gate hides: an id is taken by a document a search does not serve.
func (db *DB) checkIngestConditionTx(ctx context.Context, tx *sql.Tx, id string, c *ingestCondition) error {
	var hash sql.NullString
	err := db.querierQueryRow(ctx, tx, `SELECT `+db.Dialect().JSONTextGuarded("metadata", contentHashKey)+` FROM documents WHERE id = ?`, id).Scan(&hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read the condition of document %q: %w", id, err)
	}
	return c.holds(id, err == nil, hash.String)
}
