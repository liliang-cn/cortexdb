package connector

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// ChangeOp is the kind of row-level change captured from a source.
type ChangeOp int

const (
	OpInsert ChangeOp = iota
	OpUpdate
	OpDelete
)

// String returns the lowercase op name.
func (o ChangeOp) String() string {
	switch o {
	case OpInsert:
		return "insert"
	case OpUpdate:
		return "update"
	case OpDelete:
		return "delete"
	default:
		return "unknown"
	}
}

// ChangeEvent is one row-level change from a source. Key holds the primary-key
// column(s) -> raw value(s) and is present for every op (a delete carries only
// the key). Row holds the full row for Insert/Update and is empty for Delete.
type ChangeEvent struct {
	Op    ChangeOp
	Table string
	Key   map[string]string
	Row   importflow.Record
	// Position is the source's resume token for this event (Route B: the LSN /
	// binlog position string). Empty for polling sources.
	Position string
}

// ChangeSource streams row changes. For polling (Route A) Changes returns after
// one drained batch; for log-based sources (Route B) it blocks and streams until
// ctx is cancelled. fn returning an error aborts iteration.
type ChangeSource interface {
	Changes(ctx context.Context, fn func(ChangeEvent) error) error
	Close() error
}
