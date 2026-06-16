package connector

import (
	"context"
	"encoding/json"
	"fmt"
)

// TableCursor declares how to poll one table for changes.
type TableCursor struct {
	Table        string
	CursorColumn string   // monotonic column, e.g. updated_at or a sequence
	KeyColumns   []string // primary key column(s)
}

// PollingOptions configures a polling change source.
type PollingOptions struct {
	Tables    []TableCursor
	BatchSize int // rows per table per drain; default 500
}

// pollingChangeSource emits insert/update events for rows whose cursor advanced.
// It CANNOT see hard deletes. One Changes() call drains current changes once.
type pollingChangeSource struct {
	inner      *sqlSource
	opts       PollingOptions
	watermarks map[string]string // table -> last seen cursor value
}

// Compile-time guarantees: a polling source is both a ChangeSource and a
// cursorCheckpointer, so the Watcher always seeds/saves its watermark (a future
// refactor dropping a method would fail the build rather than silently disabling
// resume).
var (
	_ ChangeSource       = (*pollingChangeSource)(nil)
	_ cursorCheckpointer = (*pollingChangeSource)(nil)
)

// NewPollingChangeSource opens a live SQL source for polling. driver is
// "postgres"/"pgx" or "mysql".
func NewPollingChangeSource(driver, dsn string, opts PollingOptions) (ChangeSource, error) {
	if opts.BatchSize == 0 {
		opts.BatchSize = 500
	}
	src, err := openSource(driver, dsn, SourceOptions{})
	if err != nil {
		return nil, err
	}
	ss, ok := src.(*sqlSource)
	if !ok {
		_ = src.Close()
		return nil, fmt.Errorf("connector: polling requires a live SQL source")
	}
	return &pollingChangeSource{inner: ss, opts: opts, watermarks: map[string]string{}}, nil
}

func (p *pollingChangeSource) Changes(ctx context.Context, fn func(ChangeEvent) error) error {
	for _, tc := range p.opts.Tables {
		recs, err := p.inner.readRowsWhere(ctx, tc.Table, tc.CursorColumn, p.watermarks[tc.Table], tc.KeyColumns, p.opts.BatchSize)
		if err != nil {
			return err
		}
		for _, r := range recs {
			key := map[string]string{}
			for _, k := range tc.KeyColumns {
				key[k] = r.Values[k]
			}
			if err := fn(ChangeEvent{Op: OpUpdate, Table: tc.Table, Key: key, Row: r}); err != nil {
				return err
			}
			if cv, ok := r.Values[tc.CursorColumn]; ok {
				p.watermarks[tc.Table] = cv
			}
		}
	}
	return nil
}

func (p *pollingChangeSource) Close() error { return p.inner.Close() }

// WatermarkJSON serializes the current per-table watermarks for the checkpoint.
func (p *pollingChangeSource) WatermarkJSON() (string, error) {
	b, err := json.Marshal(p.watermarks)
	return string(b), err
}

// LoadWatermarks restores per-table watermarks from a checkpoint cursor JSON.
func (p *pollingChangeSource) LoadWatermarks(cursor string) error {
	if cursor == "" {
		return nil
	}
	return json.Unmarshal([]byte(cursor), &p.watermarks)
}
