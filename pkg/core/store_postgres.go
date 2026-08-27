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
// Store has thirty methods, and most of them are not about vectors. The vector
// core is here; documents, sessions and chat live in store_postgres_chat.go,
// the reads and batch writes in store_postgres_read.go, and the dimension
// bookkeeping in store_postgres_dims.go. What is still missing answers with
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
		// Seeding an explicit id does not advance the SERIAL sequence, so the
		// first CreateCollection asks for 1 again and dies on the primary key
		// — and ON CONFLICT (name) does not catch a conflict on id. Nudging
		// the sequence past whatever is already there is the fix. Found by
		// the dimension tests, which needed a collection of their own.
		`SELECT setval(pg_get_serial_sequence('collections', 'id'),
		               GREATEST((SELECT COALESCE(MAX(id), 1) FROM collections), 1))`,
		`CREATE TABLE IF NOT EXISTS documents (
			id TEXT PRIMARY KEY,
			title TEXT,
			content TEXT,
			source_url TEXT,
			version INTEGER NOT NULL DEFAULT 1,
			author TEXT,
			metadata JSONB,
			acl JSONB,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_author ON documents(author)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS embeddings (
			id TEXT PRIMARY KEY,
			collection_id INTEGER NOT NULL DEFAULT 1 REFERENCES collections(id) ON DELETE CASCADE,
			vector %s NOT NULL,
			content TEXT NOT NULL,
			-- The same foreign key SQLite declares. It was absent while
			-- documents were unimplemented here, which meant an insert SQLite
			-- rejected was accepted on this backend — a divergence the parity
			-- suite found and this closes.
			doc_id TEXT REFERENCES documents(id) ON DELETE CASCADE,
			metadata JSONB,
			acl JSONB,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`, colType),
		`CREATE INDEX IF NOT EXISTS idx_embeddings_collection_id ON embeddings(collection_id)`,
		`CREATE INDEX IF NOT EXISTS idx_embeddings_doc_id ON embeddings(doc_id)`,
		// metadata is jsonb rather than TEXT: the filters below query it, and
		// a GIN index makes that a lookup instead of a parse-per-row.
		`CREATE INDEX IF NOT EXISTS idx_embeddings_metadata ON embeddings USING gin (metadata)`,
		// Chat lives in the same database as the vectors it searches. The
		// columns are SQLite's, translated rather than redesigned: DATETIME
		// becomes TIMESTAMP, the metadata TEXT becomes JSONB, and the message
		// vector — a BLOB there, decoded and scored in Go — becomes the same
		// pgvector column the embeddings use, so the ranking happens in the
		// database and means the same thing.
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT,
			metadata JSONB,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			vector %s,
			metadata JSONB,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`, colType),
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at)`,
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

// GetSimilarityFunc is how this store compares two vectors.
//
// The graph layer scores its own candidates in Go and has to score them the
// same way, or a hybrid result would rank differently from a plain search over
// the same rows. Ranking here happens in the database, so this is the
// conversion that keeps the two agreeing: pgvector's <=> is cosine distance
// and Search returns 1 - distance, which is what CosineSimilarity computes.
func (s *PostgresStore) GetSimilarityFunc() SimilarityFunc {
	if s.config.SimilarityFn != nil {
		return s.config.SimilarityFn
	}
	return CosineSimilarity
}

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

// --- Documents ---------------------------------------------------------------
//
// Implemented because DB needs them: cortexdb.DB reaches for GetDocument and
// CreateDocument in eleven places, so a store that cannot hold a document
// cannot back a brain no matter how well it holds vectors. Their absence was
// also the one divergence the parity suite found — SQLite enforces
// embeddings.doc_id as a foreign key and this backend had nothing to point at.
// Now it does, and the constraint matches.

func (s *PostgresStore) CreateDocument(ctx context.Context, doc *Document) error {
	if doc == nil || doc.ID == "" {
		return fmt.Errorf("create document: an ID is required")
	}
	meta, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("create document: metadata: %w", err)
	}
	acl, err := json.Marshal(doc.ACL)
	if err != nil {
		return fmt.Errorf("create document: acl: %w", err)
	}
	version := doc.Version
	if version <= 0 {
		version = 1
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO documents (id, title, source_url, content, version, author, metadata, acl, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		doc.ID, doc.Title, doc.SourceURL, doc.Content, version, doc.Author, string(meta), string(acl))
	return err
}

func (s *PostgresStore) GetDocument(ctx context.Context, id string) (*Document, error) {
	var (
		doc                    Document
		title, source, content sql.NullString
		author                 sql.NullString
		metadata, acl          []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, source_url, content, version, author, metadata, acl, created_at, updated_at
		FROM documents WHERE id = $1`, id).
		Scan(&doc.ID, &title, &source, &content, &doc.Version, &author,
			&metadata, &acl, &doc.CreatedAt, &doc.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("document %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	doc.Title, doc.SourceURL, doc.Content, doc.Author = title.String, source.String, content.String, author.String
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &doc.Metadata); err != nil {
			return nil, fmt.Errorf("get document %q: metadata: %w", id, err)
		}
	}
	if len(acl) > 0 {
		if err := json.Unmarshal(acl, &doc.ACL); err != nil {
			return nil, fmt.Errorf("get document %q: acl: %w", id, err)
		}
	}
	return &doc, nil
}

// UpdateDocument replaces a document's content and bumps its version.
//
// Not part of the Store interface, but cortexdb.DB calls it, and a store that
// cannot answer it cannot back a brain.
func (s *PostgresStore) UpdateDocument(ctx context.Context, doc *Document) error {
	if doc == nil || doc.ID == "" {
		return fmt.Errorf("update document: an ID is required")
	}
	meta, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("update document: metadata: %w", err)
	}
	acl, err := json.Marshal(doc.ACL)
	if err != nil {
		return fmt.Errorf("update document: acl: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE documents
		   SET title = $2, source_url = $3, content = $4, version = $5,
		       author = $6, metadata = $7, acl = $8, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1`,
		doc.ID, doc.Title, doc.SourceURL, doc.Content, doc.Version, doc.Author, string(meta), string(acl))
	if err != nil {
		return err
	}
	// Reported rather than swallowed: an update that matched nothing means the
	// caller is working from a document that is not there, and returning nil
	// would let it carry on believing otherwise.
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("update document: %q does not exist", doc.ID)
	}
	return nil
}

// DeleteDocument removes a document. Its embeddings go with it, by the foreign
// key — the same cascade SQLite declares.
func (s *PostgresStore) DeleteDocument(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM documents WHERE id = $1`, id)
	return err
}

func (s *PostgresStore) ListDocumentsWithFilter(ctx context.Context, author string, limit int) ([]*Document, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id, title, source_url, content, version, author, metadata, acl, created_at, updated_at
	          FROM documents`
	args := []any{}
	if author != "" {
		args = append(args, author)
		query += ` WHERE author = $1`
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Document
	for rows.Next() {
		var (
			doc                    Document
			title, source, content sql.NullString
			auth                   sql.NullString
			metadata, acl          []byte
		)
		if err := rows.Scan(&doc.ID, &title, &source, &content, &doc.Version, &auth,
			&metadata, &acl, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
			return nil, err
		}
		doc.Title, doc.SourceURL, doc.Content, doc.Author = title.String, source.String, content.String, auth.String
		if len(metadata) > 0 {
			_ = json.Unmarshal(metadata, &doc.Metadata)
		}
		if len(acl) > 0 {
			_ = json.Unmarshal(acl, &doc.ACL)
		}
		out = append(out, &doc)
	}
	return out, rows.Err()
}

// --- The rest of Store, named rather than silently absent. -------------------
//
// What is left is the ACL and advanced-filter search surface, plus the
// quantizer trainer. Each says so with its own name in the error, so a caller
// that hits one knows exactly what is missing instead of debugging an empty
// result.

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
