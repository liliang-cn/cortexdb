package core

// A Store that keeps its vectors in PostgreSQL.
//
// SQLiteStore is the default and stays the default: it is fast, embedded and
// needs no daemon, which is exactly right for a brain on one machine. This is
// for the other place — a deployment that has to be procured, audited, backed
// up and replicated, where "our own engine" is not an option no matter how
// good it is, and where the index belongs in the database rather than in one
// process's memory.
//
// Partial on purpose, and it says which parts.
//
// Store has thirty methods, and most of them are not about vectors: documents,
// sessions, chat messages, quantizer training. This implements the vector core
// — write, search, delete, count, collections — and answers the rest with
// ErrPostgresStoreUnimplemented naming the method. That is the same bargain
// agent-go's BaseStore strikes with ErrMemoryStoreUnsupported: a backend that
// silently did nothing would be worse than one that says what it cannot do,
// because the caller finds out either way and only one of those tells them
// when.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// ErrPostgresStoreUnimplemented is returned by the parts of Store this backend
// does not cover yet. It always names the method, so a log line is enough to
// know what to reach for.
var ErrPostgresStoreUnimplemented = fmt.Errorf("cortexdb: not implemented by the PostgreSQL store")

func unimplemented(method string) error {
	return fmt.Errorf("%s: %w", method, ErrPostgresStoreUnimplemented)
}

// PostgresStore implements the vector core of Store on PostgreSQL + pgvector.
type PostgresStore struct {
	db     *sql.DB
	config Config

	// indexed reports whether an ANN index serves searches. False means exact
	// scan — correct, and linear. pgvector will not index past 2000
	// dimensions, so a 4096-dimensional model lands here.
	indexed bool
	// why explains a false `indexed`, in words for whoever reads the log.
	why string
}

// NewPostgresStore wraps an open database. The caller owns the pool: a
// deployment usually has one and wants its own limits on it.
func NewPostgresStore(db *sql.DB, config Config) *PostgresStore {
	if config.SimilarityFn == nil {
		config.SimilarityFn = CosineSimilarity
	}
	return &PostgresStore{db: db, config: config}
}

// Indexed reports what search will actually do, and why if it is not what you
// would want. See the note on the field.
func (s *PostgresStore) Indexed() (bool, string) { return s.indexed, s.why }

func (s *PostgresStore) Init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return fmt.Errorf("pgvector is required by the PostgreSQL store: %w", err)
	}

	// Unconstrained `vector` when the dimension is not known yet: it accepts
	// any width, at the cost of an index, which is better than refusing to
	// start while the store is still auto-detecting.
	colType := "vector"
	if s.config.VectorDim > 0 {
		colType = fmt.Sprintf("vector(%d)", s.config.VectorDim)
	}
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS collections (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			dimensions INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO collections (id, name) VALUES (1, 'default') ON CONFLICT (id) DO NOTHING`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS embeddings (
			id TEXT PRIMARY KEY,
			collection_id INTEGER NOT NULL DEFAULT 1 REFERENCES collections(id) ON DELETE CASCADE,
			vector %s NOT NULL,
			content TEXT NOT NULL,
			-- No foreign key: the SQLite store points doc_id at documents(id)
			-- and enforces it, but documents are not implemented here yet, so
			-- there is nothing to point at. Until they are, doc_id is a free
			-- tag on this backend and an insert SQLite would reject is
			-- accepted. Stated here because the parity suite found it, and a
			-- divergence nobody wrote down is one somebody debugs.
			doc_id TEXT,
			metadata JSONB,
			acl JSONB,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`, colType),
		`CREATE INDEX IF NOT EXISTS idx_embeddings_collection_id ON embeddings(collection_id)`,
		`CREATE INDEX IF NOT EXISTS idx_embeddings_doc_id ON embeddings(doc_id)`,
		// metadata is jsonb rather than TEXT: the filters below query it, and
		// a GIN index makes that a lookup instead of a parse-per-row.
		`CREATE INDEX IF NOT EXISTS idx_embeddings_metadata ON embeddings USING gin (metadata)`,
	}
	for _, stmt := range ddl {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init: %w", err)
		}
	}

	return s.buildVectorIndex(ctx)
}

// buildVectorIndex creates the ANN index when pgvector can carry one, and
// records why when it cannot.
func (s *PostgresStore) buildVectorIndex(ctx context.Context) error {
	dim := s.config.VectorDim
	switch {
	case dim <= 0:
		s.indexed, s.why = false, "no fixed dimension yet: an ANN index needs one, so search is exact"
		return nil
	case dim > pgVectorMaxIndexedDims:
		s.indexed, s.why = false, fmt.Sprintf(
			"%d dimensions is past pgvector's %d-dimension index limit, so search is exact",
			dim, pgVectorMaxIndexedDims)
		return nil
	}
	_, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_embeddings_vec
		ON embeddings USING hnsw (vector vector_cosine_ops)`)
	if err != nil {
		// Not fatal: an unindexed store still answers correctly.
		s.indexed, s.why = false, "index not built, search is exact: "+err.Error()
		return nil
	}
	s.indexed, s.why = true, ""
	return nil
}

// pgVectorMaxIndexedDims mirrors pkg/graph: HNSW and IVFFlat both refuse a
// `vector` column wider than this.
const pgVectorMaxIndexedDims = 2000

func (s *PostgresStore) Upsert(ctx context.Context, emb *Embedding) error {
	if emb == nil || emb.ID == "" {
		return fmt.Errorf("invalid embedding: missing ID")
	}
	if len(emb.Vector) == 0 {
		return fmt.Errorf("invalid embedding %s: missing vector", emb.ID)
	}
	meta, err := jsonOrNull(emb.Metadata)
	if err != nil {
		return err
	}
	acl, err := jsonOrNull(emb.ACL)
	if err != nil {
		return err
	}
	collection := emb.CollectionID
	if collection == 0 {
		collection = 1
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO embeddings (id, collection_id, vector, content, doc_id, metadata, acl)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			collection_id = EXCLUDED.collection_id,
			vector        = EXCLUDED.vector,
			content       = EXCLUDED.content,
			doc_id        = EXCLUDED.doc_id,
			metadata      = EXCLUDED.metadata,
			acl           = EXCLUDED.acl`,
		emb.ID, collection, PgVectorLiteral(emb.Vector), emb.Content, nullIfEmpty(emb.DocID), meta, acl)
	return err
}

// UpsertBatch writes in one transaction: a half-written batch would leave the
// caller with no way to know which half.
func (s *PostgresStore) UpsertBatch(ctx context.Context, embs []*Embedding) error {
	if len(embs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, emb := range embs {
		if emb == nil || emb.ID == "" || len(emb.Vector) == 0 {
			return fmt.Errorf("invalid embedding in batch")
		}
		meta, err := jsonOrNull(emb.Metadata)
		if err != nil {
			return err
		}
		acl, err := jsonOrNull(emb.ACL)
		if err != nil {
			return err
		}
		collection := emb.CollectionID
		if collection == 0 {
			collection = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO embeddings (id, collection_id, vector, content, doc_id, metadata, acl)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET
				collection_id = EXCLUDED.collection_id,
				vector        = EXCLUDED.vector,
				content       = EXCLUDED.content,
				doc_id        = EXCLUDED.doc_id,
				metadata      = EXCLUDED.metadata,
				acl           = EXCLUDED.acl`,
			emb.ID, collection, PgVectorLiteral(emb.Vector), emb.Content,
			nullIfEmpty(emb.DocID), meta, acl); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Search returns the nearest embeddings, ranked by the database.
//
// Cosine distance, converted back to a similarity so the score means the same
// thing it does on SQLite — a caller comparing against a threshold must not
// have to know which backend answered.
func (s *PostgresStore) Search(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("search: empty query vector")
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = 10
	}

	where := []string{}
	args := []any{PgVectorLiteral(query)}
	if opts.Collection != "" {
		args = append(args, opts.Collection)
		where = append(where, fmt.Sprintf(
			"collection_id = (SELECT id FROM collections WHERE name = $%d)", len(args)))
	}
	// Metadata equality filters, pushed into the jsonb containment operator so
	// the GIN index can serve them rather than every row being decoded in Go.
	if len(opts.Filter) > 0 {
		filter, err := json.Marshal(opts.Filter)
		if err != nil {
			return nil, err
		}
		args = append(args, string(filter))
		where = append(where, fmt.Sprintf("metadata @> $%d::jsonb", len(args)))
	}

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, topK)
	q := fmt.Sprintf(`
		SELECT id, collection_id, content, doc_id, metadata, acl, 1 - (vector <=> $1) AS score
		FROM embeddings%s
		ORDER BY vector <=> $1
		LIMIT $%d`, clause, len(args))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScoredEmbedding
	for rows.Next() {
		var (
			e        ScoredEmbedding
			docID    sql.NullString
			metadata []byte
			acl      []byte
		)
		if err := rows.Scan(&e.ID, &e.CollectionID, &e.Content, &docID, &metadata, &acl, &e.Score); err != nil {
			return nil, err
		}
		e.DocID = docID.String
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &e.Metadata); err != nil {
				return nil, err
			}
		}
		if len(acl) > 0 {
			if err := json.Unmarshal(acl, &e.ACL); err != nil {
				return nil, err
			}
		}
		// Applied here rather than in SQL: the threshold is on similarity and
		// the ORDER BY is on distance, so a WHERE would have to restate the
		// conversion and the two could drift.
		if opts.Threshold > 0 && e.Score < opts.Threshold {
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RangeSearch returns everything within a cosine distance of the query.
func (s *PostgresStore) RangeSearch(ctx context.Context, query []float32, radius float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	// Expressed as a similarity threshold on top of the ordered search, so the
	// two paths cannot disagree about what "close" means.
	opts.Threshold = float64(1 - radius)
	if opts.TopK <= 0 {
		opts.TopK = 1000
	}
	return s.Search(ctx, query, opts)
}

func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM embeddings WHERE id = $1`, id)
	return err
}

func (s *PostgresStore) DeleteByDocID(ctx context.Context, docID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM embeddings WHERE doc_id = $1`, docID)
	return err
}

func (s *PostgresStore) DeleteBatch(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	// One statement with an array rather than N statements or a generated IN
	// list: no placeholder ceiling and nothing to escape.
	_, err := s.db.ExecContext(ctx, `DELETE FROM embeddings WHERE id = ANY($1)`, pgTextArray(ids))
	return err
}

// DeleteByFilter is deliberately absent rather than approximated.
//
// MetadataFilter carries an expression tree — AND/OR, comparisons, ranges —
// and translating it to SQL is its own piece of work. A partial translation
// that quietly ignored a clause would delete rows the caller meant to keep,
// and unlike a wrong search result there is nothing to notice afterwards.
// Data loss is not a degradation, so this waits for the real thing.
func (s *PostgresStore) DeleteByFilter(context.Context, *MetadataFilter) error {
	return unimplemented("DeleteByFilter")
}

func (s *PostgresStore) Stats(ctx context.Context) (StoreStats, error) {
	var stats StoreStats
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*), COALESCE(pg_total_relation_size('embeddings'), 0) FROM embeddings`).
		Scan(&stats.Count, &stats.Size)
	stats.Dimensions = s.config.VectorDim
	return stats, err
}

func (s *PostgresStore) Close() error { return nil } // the pool belongs to the caller

func (s *PostgresStore) CreateCollection(ctx context.Context, name string, dimensions int) (*Collection, error) {
	var c Collection
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO collections (name, dimensions) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET dimensions = EXCLUDED.dimensions
		RETURNING id, name, dimensions`, name, dimensions).
		Scan(&c.ID, &c.Name, &c.Dimensions)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *PostgresStore) GetCollection(ctx context.Context, name string) (*Collection, error) {
	var c Collection
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, dimensions FROM collections WHERE name = $1`, name).
		Scan(&c.ID, &c.Name, &c.Dimensions)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("collection %q not found", name)
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *PostgresStore) ListCollections(ctx context.Context) ([]*Collection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, dimensions FROM collections ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Dimensions); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteCollection(ctx context.Context, name string) error {
	if name == "default" {
		return fmt.Errorf("refusing to delete the default collection")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM collections WHERE name = $1`, name)
	return err
}

// --- The rest of Store, named rather than silently absent. -------------------
//
// Documents, sessions, chat history and the quantizer trainers belong to the
// SQLite store's world and have no PostgreSQL implementation yet. Each says so
// with its own name in the error, so a caller that hits one knows exactly what
// is missing instead of debugging an empty result.

func (s *PostgresStore) GetCollectionStats(context.Context, string) (*CollectionStats, error) {
	return nil, unimplemented("GetCollectionStats")
}
func (s *PostgresStore) TrainIndex(context.Context, int) error {
	// pgvector builds and maintains its own index; there is nothing to train.
	return nil
}
func (s *PostgresStore) TrainQuantizer(context.Context) error {
	return unimplemented("TrainQuantizer")
}
func (s *PostgresStore) CreateDocument(context.Context, *Document) error {
	return unimplemented("CreateDocument")
}
func (s *PostgresStore) GetDocument(context.Context, string) (*Document, error) {
	return nil, unimplemented("GetDocument")
}
func (s *PostgresStore) DeleteDocument(context.Context, string) error {
	return unimplemented("DeleteDocument")
}
func (s *PostgresStore) ListDocumentsWithFilter(context.Context, string, int) ([]*Document, error) {
	return nil, unimplemented("ListDocumentsWithFilter")
}
func (s *PostgresStore) CreateSession(context.Context, *Session) error {
	return unimplemented("CreateSession")
}
func (s *PostgresStore) GetSession(context.Context, string) (*Session, error) {
	return nil, unimplemented("GetSession")
}
func (s *PostgresStore) AddMessage(context.Context, *Message) error {
	return unimplemented("AddMessage")
}
func (s *PostgresStore) GetSessionHistory(context.Context, string, int) ([]*Message, error) {
	return nil, unimplemented("GetSessionHistory")
}
func (s *PostgresStore) SearchChatHistory(context.Context, []float32, string, int) ([]*Message, error) {
	return nil, unimplemented("SearchChatHistory")
}
func (s *PostgresStore) SearchWithACL(context.Context, []float32, []string, SearchOptions) ([]ScoredEmbedding, error) {
	return nil, unimplemented("SearchWithACL")
}
func (s *PostgresStore) HybridSearch(context.Context, []float32, string, HybridSearchOptions) ([]ScoredEmbedding, error) {
	return nil, unimplemented("HybridSearch")
}
func (s *PostgresStore) SearchWithAdvancedFilter(context.Context, []float32, AdvancedSearchOptions) ([]ScoredEmbedding, error) {
	return nil, unimplemented("SearchWithAdvancedFilter")
}

// --- helpers -----------------------------------------------------------------

// PgVectorLiteral renders a vector the way pgvector parses it: [1,2,3].
// Always passed as a bound parameter, so it is a value and never SQL.
func PgVectorLiteral(vec []float32) string {
	var b strings.Builder
	b.Grow(len(vec)*8 + 2)
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%g", v)
	}
	b.WriteByte(']')
	return b.String()
}

// pgTextArray renders a Go slice as a PostgreSQL text[] literal.
func pgTextArray(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

func jsonOrNull(v any) (any, error) {
	switch t := v.(type) {
	case map[string]string:
		if len(t) == 0 {
			return nil, nil
		}
	case []string:
		if len(t) == 0 {
			return nil, nil
		}
	case nil:
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
