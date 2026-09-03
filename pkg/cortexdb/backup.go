package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// ErrBackupUnsupported is returned when the backend behind this DB cannot copy
// itself. It is a sentinel so callers can tell "this brain does not do backups"
// apart from "the backup failed" — the first is a fact about the deployment and
// wants a different answer from the operator than a retry.
var ErrBackupUnsupported = errors.New("cortexdb: backend does not support in-process backup")

// Backup writes a consistent copy of the whole brain — vectors, documents,
// memory, graph, and every sibling package's tables, since they all live in the
// one database — to path, while the DB stays open and writable.
//
// path must not already exist; the backend refuses to overwrite a backup.
//
// Whether this works at all depends on the backend, which is why core.Backupper
// is an optional interface rather than part of BrainStore: SQLite can snapshot
// itself into a file, PostgreSQL is backed up by pg_dump and the operations team
// that runs it. When the backend cannot, the error wraps ErrBackupUnsupported
// and names the backend, so the message tells an operator what to do instead of
// only that they cannot do this.
func (db *DB) Backup(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("backup path is required")
	}

	b, ok := db.store.(core.Backupper)
	if !ok {
		return fmt.Errorf("%w: %T takes its backups outside this process", ErrBackupUnsupported, db.store)
	}

	return b.Backup(ctx, path)
}
