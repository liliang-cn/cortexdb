package core

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/liliang-cn/sqvect/v2/internal/encoding"
	"github.com/liliang-cn/sqvect/v2/pkg/index"
)

// DumpFormat represents the format for data export
type DumpFormat string

const (
	// DumpFormatJSON exports data as JSON
	DumpFormatJSON DumpFormat = "json"
	// DumpFormatJSONL exports data as JSON Lines (one JSON object per line)
	DumpFormatJSONL DumpFormat = "jsonl"
	// DumpFormatCSV exports data as CSV (vectors as base64 encoded strings)
	DumpFormatCSV DumpFormat = "csv"
)

// DumpOptions defines options for data export
type DumpOptions struct {
	Format         DumpFormat // Export format
	IncludeVectors bool       // Include vector data (can be large)
	IncludeIndex   bool       // Include index data (HNSW, IVF)
	Filter         *MetadataFilter // Optional filter for selective export
	BatchSize      int        // Batch size for export (default: 1000)
}

// DefaultDumpOptions returns default dump options
func DefaultDumpOptions() DumpOptions {
	return DumpOptions{
		Format:         DumpFormatJSON,
		IncludeVectors: true,
		IncludeIndex:   false,
		Filter:         nil,
		BatchSize:      1000,
	}
}

// DumpStats provides statistics about the export operation
type DumpStats struct {
	TotalEmbeddings int    `json:"total_embeddings"`
	TotalDocuments  int    `json:"total_documents"`
	TotalCollections int   `json:"total_collections"`
	BytesWritten    int64  `json:"bytes_written"`
}

// ExportMetadata contains metadata about the export
type ExportMetadata struct {
	Version     string    `json:"version"`
	Dimensions  int       `json:"dimensions"`
	Count       int       `json:"count"`
	ExportedAt  string    `json:"exported_at"`
	Config      Config    `json:"config"`
}

// ImportStats provides statistics about the import operation
type ImportStats struct {
	TotalEmbeddings int      `json:"total_embeddings"`
	TotalDocuments  int      `json:"total_documents"`
	FailedCount     int      `json:"failed_count"`
	SkippedCount    int      `json:"skipped_count"`
}

// Dump exports all embeddings to a writer in the specified format
func (s *SQLiteStore) Dump(ctx context.Context, w io.Writer, opts DumpOptions) (*DumpStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("dump", ErrStoreClosed)
	}

	switch opts.Format {
	case DumpFormatJSON:
		return s.dumpJSON(ctx, w, opts)
	case DumpFormatJSONL:
		return s.dumpJSONL(ctx, w, opts)
	case DumpFormatCSV:
		return s.dumpCSV(ctx, w, opts)
	default:
		return nil, wrapError("dump", fmt.Errorf("unsupported format: %s", opts.Format))
	}
}

// dumpJSON exports data as a JSON array
func (s *SQLiteStore) dumpJSON(ctx context.Context, w io.Writer, opts DumpOptions) (*DumpStats, error) {
	stats := &DumpStats{}

	// Get all embeddings
	embeddings, err := s.getAllEmbeddings(ctx, opts)
	if err != nil {
		return nil, err
	}
	stats.TotalEmbeddings = len(embeddings)

	// Get metadata
	schemaStats, _ := s.Stats(ctx)
	stats.TotalDocuments = int(schemaStats.Count)

	// Build export structure
	export := struct {
		Metadata   ExportMetadata `json:"metadata"`
		Embeddings []*Embedding   `json:"embeddings"`
	}{
		Metadata: ExportMetadata{
			Version:    "1.0",
			Dimensions: schemaStats.Dimensions,
			Count:      len(embeddings),
			ExportedAt: currentTimeStr(),
			Config:     s.config,
		},
		Embeddings: embeddings,
	}

	// Encode and write
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(export); err != nil {
		return nil, wrapError("dump_json", fmt.Errorf("failed to encode JSON: %w", err))
	}

	return stats, nil
}

// dumpJSONL exports data as JSON Lines (one JSON per line)
func (s *SQLiteStore) dumpJSONL(ctx context.Context, w io.Writer, opts DumpOptions) (*DumpStats, error) {
	stats := &DumpStats{}

	embeddings, err := s.getAllEmbeddings(ctx, opts)
	if err != nil {
		return nil, err
	}
	stats.TotalEmbeddings = len(embeddings)

	encoder := json.NewEncoder(w)
	for _, emb := range embeddings {
		if err := encoder.Encode(emb); err != nil {
			return stats, wrapError("dump_jsonl", fmt.Errorf("failed to encode: %w", err))
		}
	}

	return stats, nil
}

// dumpCSV exports data as CSV
func (s *SQLiteStore) dumpCSV(ctx context.Context, w io.Writer, opts DumpOptions) (*DumpStats, error) {
	stats := &DumpStats{}

	embeddings, err := s.getAllEmbeddings(ctx, opts)
	if err != nil {
		return nil, err
	}
	stats.TotalEmbeddings = len(embeddings)

	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Write header
	headers := []string{"id", "content", "doc_id", "metadata"}
	if opts.IncludeVectors {
		headers = append(headers, "vector")
	}
	if err := writer.Write(headers); err != nil {
		return nil, err
	}

	// Write data
	for _, emb := range embeddings {
		row := []string{emb.ID, emb.Content, emb.DocID}
		if emb.Metadata != nil {
			metaJSON, _ := json.Marshal(emb.Metadata)
			row = append(row, string(metaJSON))
		} else {
			row = append(row, "")
		}
		if opts.IncludeVectors {
			vecJSON, _ := json.Marshal(emb.Vector)
			row = append(row, string(vecJSON))
		}
		if err := writer.Write(row); err != nil {
			return stats, err
		}
	}

	return stats, nil
}

// getAllEmbeddings retrieves all embeddings with optional filtering
func (s *SQLiteStore) getAllEmbeddings(ctx context.Context, opts DumpOptions) ([]*Embedding, error) {
	query := `
		SELECT e.id, e.collection_id, c.name, e.vector, e.content, e.doc_id, e.metadata
		FROM embeddings e
		LEFT JOIN collections c ON e.collection_id = c.id
	`

	var args []interface{}
	if opts.Filter != nil && !opts.Filter.IsEmpty() {
		whereClause, params := opts.Filter.ToSQL()
		if whereClause != "" {
			// Prefix metadata with 'e.' to avoid ambiguity with collections table
			whereClause = strings.ReplaceAll(whereClause, "json_extract(metadata", "json_extract(e.metadata")
			query += " WHERE " + whereClause
			args = params
		}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError("get_all_embeddings", fmt.Errorf("failed to query: %w", err))
	}
	defer func() { _ = rows.Close() }()

	var embeddings []*Embedding
	for rows.Next() {
		scoredEmb, err := s.scanEmbedding(rows)
		if err != nil {
			// Log error but continue with other rows
			continue
		}

		// Convert ScoredEmbedding to Embedding
		emb := &Embedding{
			ID:           scoredEmb.ID,
			Collection:   scoredEmb.Collection,
			Vector:       scoredEmb.Vector,
			Content:      scoredEmb.Content,
			DocID:        scoredEmb.DocID,
			Metadata:     scoredEmb.Metadata,
		}

		if !opts.IncludeVectors {
			emb.Vector = nil
		}

		embeddings = append(embeddings, emb)
	}

	return embeddings, rows.Err()
}

// Load imports embeddings from a reader
func (s *SQLiteStore) Load(ctx context.Context, r io.Reader, opts LoadOptions) (*ImportStats, error) {
	if s.closed {
		return nil, wrapError("load", ErrStoreClosed)
	}

	switch opts.Format {
	case DumpFormatJSON:
		return s.loadJSON(ctx, r, opts)
	case DumpFormatJSONL:
		return s.loadJSONL(ctx, r, opts)
	default:
		return nil, wrapError("load", fmt.Errorf("unsupported format: %s", opts.Format))
	}
}

// LoadOptions defines options for data import
type LoadOptions struct {
	Format       DumpFormat // Import format
	SkipExisting bool       // Skip existing embeddings (by ID)
	Replace      bool       // Replace existing embeddings
	BatchSize    int        // Batch size for import (default: 100)
	Upsert       bool       // Use upsert instead of insert
}

// DefaultLoadOptions returns default load options
func DefaultLoadOptions() LoadOptions {
	return LoadOptions{
		Format:       DumpFormatJSON,
		SkipExisting: true,
		Replace:      false,
		BatchSize:    100,
		Upsert:       true,
	}
}

// loadJSON imports data from JSON format
func (s *SQLiteStore) loadJSON(ctx context.Context, r io.Reader, opts LoadOptions) (*ImportStats, error) {
	stats := &ImportStats{}

	var export struct {
		Metadata   ExportMetadata `json:"metadata"`
		Embeddings []*Embedding   `json:"embeddings"`
	}

	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&export); err != nil {
		return nil, wrapError("load_json", fmt.Errorf("failed to decode JSON: %w", err))
	}

	// Batch upsert
	for _, emb := range export.Embeddings {
		skipped, err := s.processImportEmbedding(ctx, emb, opts)
		if err != nil {
			stats.FailedCount++
			continue
		}
		if skipped {
			stats.SkippedCount++
		} else {
			stats.TotalEmbeddings++
		}
	}

	return stats, nil
}

// loadJSONL imports data from JSON Lines format
func (s *SQLiteStore) loadJSONL(ctx context.Context, r io.Reader, opts LoadOptions) (*ImportStats, error) {
	stats := &ImportStats{}

	decoder := json.NewDecoder(r)
	for {
		var emb Embedding
		if err := decoder.Decode(&emb); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			stats.FailedCount++
			continue
		}

		skipped, err := s.processImportEmbedding(ctx, &emb, opts)
		if err != nil {
			stats.FailedCount++
			continue
		}
		if skipped {
			stats.SkippedCount++
		} else {
			stats.TotalEmbeddings++
		}
	}

	return stats, nil
}

// processImportEmbedding processes a single embedding during import
func (s *SQLiteStore) processImportEmbedding(ctx context.Context, emb *Embedding, opts LoadOptions) (skipped bool, err error) {
	// Check if exists
	if opts.SkipExisting {
		existing, _ := s.GetByID(ctx, emb.ID)
		if existing != nil {
			return true, nil
		}
	}

	// Use upsert or insert
	if opts.Upsert {
		return false, s.Upsert(ctx, emb)
	}

	return false, s.Upsert(ctx, emb)
}

// DumpToFile exports data to a file
func (s *SQLiteStore) DumpToFile(ctx context.Context, filepath string, opts DumpOptions) (*DumpStats, error) {
	file, err := os.Create(filepath)
	if err != nil {
		return nil, wrapError("dump_to_file", fmt.Errorf("failed to create file: %w", err))
	}
	defer func() { _ = file.Close() }()

	stats, err := s.Dump(ctx, file, opts)
	if err != nil {
		// Remove partial file on error
		_ = os.Remove(filepath)
		return nil, err
	}

	return stats, nil
}

// LoadFromFile imports data from a file
func (s *SQLiteStore) LoadFromFile(ctx context.Context, filepath string, opts LoadOptions) (*ImportStats, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, wrapError("load_from_file", fmt.Errorf("failed to open file: %w", err))
	}
	defer func() { _ = file.Close() }()

	return s.Load(ctx, file, opts)
}

// ExportIndex exports the index data (HNSW/IVF) to a file
func (s *SQLiteStore) ExportIndex(ctx context.Context, filepath string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("export_index", ErrStoreClosed)
	}

	file, err := os.Create(filepath)
	if err != nil {
		return wrapError("export_index", fmt.Errorf("failed to create file: %w", err))
	}
	defer func() { _ = file.Close() }()

	// Export HNSW index if available
	if s.hnswIndex != nil {
		if err := s.hnswIndex.Save(file); err != nil {
			return wrapError("export_index", fmt.Errorf("failed to save HNSW: %w", err))
		}
		return nil
	}

	// Export IVF index if available
	if s.ivfIndex != nil {
		if err := s.ivfIndex.Save(file); err != nil {
			return wrapError("export_index", fmt.Errorf("failed to save IVF: %w", err))
		}
		return nil
	}

	return wrapError("export_index", fmt.Errorf("no index to export"))
}

// ImportIndex imports index data from a file
func (s *SQLiteStore) ImportIndex(ctx context.Context, filepath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return wrapError("import_index", ErrStoreClosed)
	}

	file, err := os.Open(filepath)
	if err != nil {
		return wrapError("import_index", fmt.Errorf("failed to open file: %w", err))
	}
	defer func() { _ = file.Close() }()

	// Try to load as HNSW first
	if s.config.HNSW.Enabled {
		if s.hnswIndex == nil {
			// Initialize index if not exists
			// HNSW needs distance function, use cosine similarity as default
			s.hnswIndex = index.NewHNSW(s.config.HNSW.M, s.config.HNSW.EfConstruction, func(a, b []float32) float32 {
				// Convert cosine similarity to distance (1 - similarity)
				return float32(1.0 - CosineSimilarity(a, b))
			})
		}

		if err := s.hnswIndex.Load(file); err != nil {
			// Try IVF format
			file.Seek(0, 0)
			if s.ivfIndex == nil {
				s.ivfIndex = index.NewIVFIndex(s.config.VectorDim, s.config.IVF.NCentroids)
			}
			if err := s.ivfIndex.Load(file); err != nil {
				return wrapError("import_index", fmt.Errorf("failed to load index: %w", err))
			}
		}
		return nil
	}

	return wrapError("import_index", fmt.Errorf("no index enabled in config"))
}

// Backup creates a full backup of the database to a file
func (s *SQLiteStore) Backup(ctx context.Context, filepath string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("backup", ErrStoreClosed)
	}

	// Use SQLite backup API
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", filepath))
	if err != nil {
		return wrapError("backup", fmt.Errorf("failed to create backup: %w", err))
	}

	return nil
}

// scanEmbeddingWithACL scans a row with ACL field
func (s *SQLiteStore) scanEmbeddingWithACL(rows *sql.Rows) (*Embedding, error) {
	var emb Embedding
	var vectorBytes []byte
	var metadataJSON, aclJSON []byte

	err := rows.Scan(&emb.ID, &vectorBytes, &emb.Content, &emb.DocID, &metadataJSON, &aclJSON)
	if err != nil {
		return nil, err
	}

	// Decode vector
	if len(vectorBytes) > 0 {
		emb.Vector, err = encoding.DecodeVector(vectorBytes)
		if err != nil {
			return nil, err
		}
	}

	// Decode metadata
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &emb.Metadata); err != nil {
			emb.Metadata = make(map[string]string)
		}
	}

	// Decode ACL
	if len(aclJSON) > 0 {
		if err := json.Unmarshal(aclJSON, &emb.ACL); err != nil {
			emb.ACL = nil
		}
	}

	return &emb, nil
}

// currentTimeStr returns current time as ISO string
func currentTimeStr() string {
	return time.Now().Format(time.RFC3339)
}

// Helper to convert import stats to string
func (s *ImportStats) String() string {
	return fmt.Sprintf("ImportStats{Total: %d, Failed: %d, Skipped: %d}",
		s.TotalEmbeddings, s.FailedCount, s.SkippedCount)
}
