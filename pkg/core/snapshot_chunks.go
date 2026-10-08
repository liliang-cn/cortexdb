package core

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"
)

// Index snapshots are written in chunks.
//
// A snapshot used to be one blob: the index serialized into a buffer in
// memory, then written as one row; and read back whole before decoding. On
// 100,000 768-d vectors that blob is 450 MB, so saving cost a second copy of
// the index and loading cost the blob plus the index it decodes into — a
// 1.85 GB peak to load a 413 MB index. Chunked, the encoder writes a row
// every snapshotChunkSize bytes and the decoder reads one row at a time, so
// either way one chunk is the only copy.
//
// Chunks are rows of index_snapshots typed "<type>/<seq>", seq zero-padded
// so the rows sort in order. A snapshot written before chunking — one row
// typed "<type>" — is still read.

const snapshotChunkSize = 4 << 20

// snapshotWriteTimeout bounds writing one snapshot. It has to cover the
// largest index a store holds — about five seconds per 450 MB here, longer on
// an SD card — because a deadline reached mid-write discards the snapshot.
const snapshotWriteTimeout = 10 * time.Minute

// chunkRange is the half-open range of type values a snapshot's chunks take:
// '0' is the byte after '/', so ["HNSW/", "HNSW0") holds "HNSW/000000"… and
// nothing else, and an index on type serves it.
func chunkRange(indexType string) (string, string) {
	return indexType + "/", indexType + "0"
}

// writeSnapshotChunks replaces indexType's snapshot with what encode writes,
// in one transaction, so a reader sees the old snapshot or the new one.
func (s *SQLiteStore) writeSnapshotChunks(ctx context.Context, indexType string, encode func(io.Writer) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	lo, hi := chunkRange(indexType)
	if _, err := tx.ExecContext(ctx, `DELETE FROM index_snapshots WHERE type = ? OR (type >= ? AND type < ?)`, indexType, lo, hi); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO index_snapshots (type, data, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	w := &chunkWriter{buf: make([]byte, 0, snapshotChunkSize), flush: func(seq int, data []byte) error {
		_, err := stmt.ExecContext(ctx, fmt.Sprintf("%s/%06d", indexType, seq), data)
		return err
	}}
	if err := encode(w); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return tx.Commit()
}

// chunkWriter hands its input on in pieces of snapshotChunkSize.
type chunkWriter struct {
	buf   []byte
	seq   int
	flush func(seq int, data []byte) error
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		room := snapshotChunkSize - len(w.buf)
		take := min(room, len(p))
		w.buf = append(w.buf, p[:take]...)
		p = p[take:]
		if len(w.buf) == snapshotChunkSize {
			if err := w.emit(); err != nil {
				return n - len(p), err
			}
		}
	}
	return n, nil
}

func (w *chunkWriter) emit() error {
	if err := w.flush(w.seq, w.buf); err != nil {
		return err
	}
	w.seq++
	w.buf = w.buf[:0]
	return nil
}

// Close writes what is left. An empty snapshot still gets its one chunk, so
// that the next load finds a snapshot rather than none.
func (w *chunkWriter) Close() error {
	if len(w.buf) > 0 || w.seq == 0 {
		return w.emit()
	}
	return nil
}

// openSnapshot returns a reader over indexType's snapshot, chunked or not.
// found is false when there is none.
func (s *SQLiteStore) openSnapshot(ctx context.Context, indexType string) (r io.Reader, closeFn func(), found bool, err error) {
	lo, hi := chunkRange(indexType)
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM index_snapshots WHERE type >= ? AND type < ? ORDER BY type`, lo, hi)
	if err != nil {
		return nil, nil, false, err
	}
	cr := &chunkReader{rows: rows}
	if cr.next() {
		return cr, func() { _ = rows.Close() }, true, nil
	}
	_ = rows.Close()
	if cr.err != nil {
		return nil, nil, false, cr.err
	}

	var data []byte
	err = s.db.QueryRowContext(ctx, "SELECT data FROM index_snapshots WHERE type = ?", indexType).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	return bytes.NewReader(data), func() {}, true, nil
}

// chunkReader reads the chunk rows of one snapshot as one stream.
type chunkReader struct {
	rows *sql.Rows
	cur  []byte
	err  error
}

func (r *chunkReader) next() bool {
	if !r.rows.Next() {
		r.err = r.rows.Err()
		return false
	}
	var data []byte
	if err := r.rows.Scan(&data); err != nil {
		r.err = err
		return false
	}
	r.cur = data
	return true
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.cur) == 0 {
		if !r.next() {
			if r.err != nil {
				return 0, r.err
			}
			return 0, io.EOF
		}
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}
