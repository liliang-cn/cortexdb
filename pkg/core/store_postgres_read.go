package core

// The rest of what a brain asks its store for, on PostgreSQL.
//
// BrainStore is Store plus the methods cortexdb.DB actually calls, and these
// six are the read-and-write side of that: the two accessors, the two reads
// that take an id rather than a query, and the two writes that differ from
// UpsertBatch in who owns the transaction and whether a vector may be
// reshaped on the way in.
//
// Each one is written against the SQLite version rather than from the
// interface comment, because the interface says what the method is called and
// the SQLite implementation says what callers have been relying on: which
// absence is an error and which is an empty result, what order rows come back
// in, which fields a Get actually fills. A backend that got any of those
// subtly wrong would pass the compiler and fail in production, which is the
// exact failure this package is trying to stop having.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
)

// Config reports how this store was built. SQLiteStore takes its read lock
// here; this store keeps no state behind a lock, so the copy is the whole of
// it.
func (s *PostgresStore) Config() Config {
	return s.config
}

// GetDB hands out the raw handle for the sibling packages that keep their own
// tables in the same database. The caller must not close it — NewPostgresStore
// did not open it either.
func (s *PostgresStore) GetDB() *sql.DB {
	return s.db
}

// GetByID reads one embedding back by id.
//
// A missing id is ErrNotFound, not (nil, nil): SQLite decided that and callers
// test for it. The collection name comes from the join, matching the SQLite
// query column for column — including that CollectionID is left unset there,
// so it is left unset here rather than helpfully filled in.
func (s *PostgresStore) GetByID(ctx context.Context, id string) (*Embedding, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			e.id, e.vector::text, e.content, e.doc_id, e.metadata, e.acl,
			COALESCE(c.name, '') AS collection_name
		FROM embeddings e
		LEFT JOIN collections c ON e.collection_id = c.id
		WHERE e.id = $1`, id)

	var (
		emb            Embedding
		vectorText     string
		docID          sql.NullString
		metadata       []byte
		acl            []byte
		collectionName string
	)
	err := row.Scan(&emb.ID, &vectorText, &emb.Content, &docID, &metadata, &acl, &collectionName)
	if err == sql.ErrNoRows {
		return nil, wrapError("get_by_id", ErrNotFound)
	}
	if err != nil {
		return nil, wrapError("get_by_id", fmt.Errorf("failed to scan row: %w", err))
	}

	vector, err := pgParseVector(vectorText)
	if err != nil {
		return nil, wrapError("get_by_id", fmt.Errorf("failed to decode vector: %w", err))
	}
	emb.Vector = vector
	emb.DocID = docID.String
	emb.Collection = collectionName
	// Metadata and ACL that will not decode are dropped rather than fatal,
	// which is what SQLite does: a row whose sidecar JSON is corrupt still has
	// a usable vector and content, and refusing to return it helps nobody.
	emb.Metadata = pgDecodeMetadata(metadata)
	emb.ACL = pgDecodeACL(acl)
	return &emb, nil
}

// GetByDocID returns every embedding of one document, oldest first.
//
// An unknown document is an empty result and no error — the document simply
// has no chunks — while an empty docID is an error, because that is a caller
// bug rather than an answer. Both are SQLite's choices. The five columns are
// SQLite's five: no collection name, no ACL, since the SQLite query does not
// select them and a caller that got them from one backend and not the other
// would be looking at exactly the divergence this file exists to prevent.
func (s *PostgresStore) GetByDocID(ctx context.Context, docID string) ([]*Embedding, error) {
	if docID == "" {
		return nil, wrapError("get_by_doc_id", fmt.Errorf("doc ID cannot be empty"))
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vector::text, content, doc_id, metadata
		FROM embeddings
		WHERE doc_id = $1
		ORDER BY created_at`, docID)
	if err != nil {
		return nil, wrapError("get_by_doc_id", fmt.Errorf("failed to query embeddings: %w", err))
	}
	defer rows.Close()

	var embeddings []*Embedding
	for rows.Next() {
		var (
			emb        Embedding
			vectorText string
			rowDocID   sql.NullString
			metadata   []byte
		)
		if err := rows.Scan(&emb.ID, &vectorText, &emb.Content, &rowDocID, &metadata); err != nil {
			// Skipped, not fatal: SQLite drops the unreadable row and keeps
			// going, so one bad row does not hide a whole document.
			continue
		}
		vector, err := pgParseVector(vectorText)
		if err != nil {
			continue
		}
		emb.Vector = vector
		emb.DocID = rowDocID.String
		emb.Metadata = pgDecodeMetadata(metadata)
		row := emb
		embeddings = append(embeddings, &row)
	}

	if err := rows.Err(); err != nil {
		return nil, wrapError("get_by_doc_id", fmt.Errorf("error iterating rows: %w", err))
	}

	return embeddings, nil
}

// UpsertBatchTx writes inside a transaction the caller already opened.
//
// The point of it is that a write spanning the vector store and a sibling
// package's tables is one transaction rather than two that can half-fail — so
// it neither commits nor rolls back, and a caller who rolls back must find
// nothing left behind. Like SQLite's, it also touches no index: the rows are
// not durable until the caller commits, which is what SyncUpsertedEmbeddings
// is for afterwards.
func (s *PostgresStore) UpsertBatchTx(ctx context.Context, tx *sql.Tx, embs []*Embedding) error {
	if len(embs) == 0 {
		return nil
	}
	vectorDim := s.config.VectorDim

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO embeddings (id, collection_id, vector, content, doc_id, metadata, acl)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			collection_id = EXCLUDED.collection_id,
			vector        = EXCLUDED.vector,
			content       = EXCLUDED.content,
			doc_id        = EXCLUDED.doc_id,
			metadata      = EXCLUDED.metadata,
			acl           = EXCLUDED.acl`)
	if err != nil {
		return wrapError("upsert_batch_tx", fmt.Errorf("failed to prepare statement: %w", err))
	}
	defer stmt.Close()

	for i, emb := range embs {
		if emb == nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("invalid embedding at index %d: %w", i, encoding.ErrInvalidVector))
		}
		if err := encoding.ValidateEmbedding(emb.Vector, vectorDim); err != nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("invalid embedding at index %d: %w", i, err))
		}

		collectionID, err := pgLookupCollectionIDTx(ctx, tx, emb)
		if err != nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("resolve collection at index %d: %w", i, err))
		}

		meta, err := jsonOrNull(emb.Metadata)
		if err != nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("failed to encode metadata at index %d: %w", i, err))
		}
		acl, err := jsonOrNull(emb.ACL)
		if err != nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("failed to marshal ACL at index %d: %w", i, err))
		}

		if _, err := stmt.ExecContext(ctx, emb.ID, collectionID, PgVectorLiteral(emb.Vector),
			emb.Content, nullIfEmpty(emb.DocID), meta, acl); err != nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("failed to insert embedding at index %d: %w", i, err))
		}
	}

	return nil
}

// pgLookupCollectionIDTx resolves an embedding's collection the way
// lookupCollectionIDTx does on SQLite: an explicit id wins, a name is looked
// up, and nothing at all means the default collection.
func pgLookupCollectionIDTx(ctx context.Context, tx *sql.Tx, emb *Embedding) (int, error) {
	if emb.CollectionID != 0 {
		return emb.CollectionID, nil
	}
	if emb.Collection == "" {
		return 1, nil
	}

	var collectionID int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM collections WHERE name = $1`, emb.Collection).Scan(&collectionID); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("collection %q not found", emb.Collection)
		}
		return 0, err
	}
	return collectionID, nil
}

// UpsertBatchWithAdapt writes vectors that may not be the store's width,
// reshaping them to it first.
//
// Separate from UpsertBatch because silently reshaping a vector is a decision:
// the policy in Config makes it, and the default policy is StrictMode, which
// refuses. A store with no dimension yet takes the first vector's width as its
// own, which is how auto-detection settles.
func (s *PostgresStore) UpsertBatchWithAdapt(ctx context.Context, embs []*Embedding) error {
	if len(embs) == 0 {
		return nil
	}

	currentDim := s.config.VectorDim
	if currentDim == 0 {
		currentDim = len(embs[0].Vector)
		s.config.VectorDim = currentDim
	}

	adapter := NewDimensionAdapter(s.config.AutoDimAdapt)
	for i, emb := range embs {
		if emb == nil {
			return wrapError("upsert_batch_with_adapt", fmt.Errorf("invalid embedding at index %d: %w", i, encoding.ErrInvalidVector))
		}
		incomingDim := len(emb.Vector)
		if incomingDim != currentDim {
			adaptedVector, err := adapter.AdaptVector(emb.Vector, incomingDim, currentDim)
			if err != nil {
				return wrapError("upsert_batch_with_adapt", fmt.Errorf("dimension adaptation failed at index %d: %w", i, err))
			}
			adapter.logDimensionEvent("batch_adapt", incomingDim, currentDim, emb.ID)
			emb.Vector = adaptedVector
		}

		if err := encoding.ValidateEmbedding(emb.Vector, currentDim); err != nil {
			return wrapError("upsert_batch_with_adapt", fmt.Errorf("invalid embedding at index %d: %w", i, err))
		}
	}

	return s.UpsertBatch(ctx, embs)
}

// --- helpers -----------------------------------------------------------------

// pgParseVector reads back what PgVectorLiteral wrote: pgvector renders a
// vector as [1,2,3] in text, which is the one format both directions agree on
// without a driver-specific type.
func pgParseVector(text string) ([]float32, error) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "[") || !strings.HasSuffix(t, "]") {
		return nil, fmt.Errorf("not a pgvector literal: %q", text)
	}
	t = t[1 : len(t)-1]
	if strings.TrimSpace(t) == "" {
		return []float32{}, nil
	}
	parts := strings.Split(t, ",")
	out := make([]float32, len(parts))
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil, fmt.Errorf("element %d of pgvector literal: %w", i, err)
		}
		out[i] = float32(v)
	}
	return out, nil
}

// pgDecodeMetadata and pgDecodeACL mirror the SQLite Get path, where a sidecar
// column that will not decode costs its own value and nothing else.
func pgDecodeMetadata(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func pgDecodeACL(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var acl []string
	if err := json.Unmarshal(raw, &acl); err != nil {
		return nil
	}
	return acl
}
