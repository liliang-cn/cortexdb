package core

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"

	"github.com/liliang-cn/sqvect/internal/encoding"
	"github.com/liliang-cn/sqvect/pkg/index"
	"github.com/liliang-cn/sqvect/pkg/quantization"
)

// initHNSWIndex initializes the HNSW index if enabled in configuration
func (s *SQLiteStore) initHNSWIndex(ctx context.Context) error {
	if !s.config.HNSW.Enabled {
		return nil
	}

	// Initialize Quantizer if enabled
	if s.config.Quantization.Enabled && s.config.VectorDim > 0 {
		if s.config.Quantization.Type == "binary" {
			s.quantizer = quantization.NewBinaryQuantizer(s.config.VectorDim)
		} else {
			sq, err := quantization.NewScalarQuantizer(s.config.VectorDim, s.config.Quantization.NBits)
			if err != nil {
				s.logger.Warn("failed to create scalar quantizer", "error", err)
			} else {
				s.quantizer = sq
			}
		}
	}

	// Create HNSW index with appropriate distance function
	// Since we can't compare functions directly, we'll use cosine distance as default
	// which works well for most similarity functions
	distFunc := index.CosineDistance

	s.hnswIndex = index.NewHNSW(
		s.config.HNSW.M,
		s.config.HNSW.EfConstruction,
		distFunc,
	)

	// Set quantizer to HNSW index if available
	if s.quantizer != nil {
		s.hnswIndex.SetQuantizer(s.quantizer)
	}

	// Try to load from snapshot first
	loaded, err := s.loadIndexSnapshot(ctx, "HNSW")
	if err != nil {
		s.logger.Warn("failed to load HNSW snapshot, rebuilding", "error", err)
	}

	if loaded {
		s.logger.Info("HNSW index loaded from snapshot")
		return nil
	}

	// If quantization enabled but not loaded from snapshot, we need to train it before rebuilding
	if s.quantizer != nil && !loaded {
		if err := s.TrainQuantizer(ctx); err != nil {
			s.logger.Warn("failed to train quantizer", "error", err)
		}
	}

	// Load existing vectors into HNSW index
	return s.rebuildHNSWIndex(ctx)
}

// rebuildHNSWIndex rebuilds the HNSW index from existing vectors in the database
func (s *SQLiteStore) rebuildHNSWIndex(ctx context.Context) error {
	if s.hnswIndex == nil {
		return nil
	}

	s.logger.Info("rebuilding HNSW index from database")

	// Query all vectors from database
	rows, err := s.db.QueryContext(ctx, "SELECT id, vector FROM embeddings")
	if err != nil {
		return fmt.Errorf("failed to query existing vectors: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows during HNSW rebuild", "error", closeErr)
		}
	}()

	var insertCount int
	var errorCount int

	// Insert each vector into HNSW index
	for rows.Next() {
		var id string
		var vectorBytes []byte

		if err := rows.Scan(&id, &vectorBytes); err != nil {
			s.logger.Warn("failed to scan row during HNSW rebuild", "error", err)
			errorCount++
			continue
		}

		vec, err := encoding.DecodeVector(vectorBytes)
		if err != nil {
			s.logger.Warn("failed to decode vector during HNSW rebuild", "id", id, "error", err)
			errorCount++
			continue
		}

		// Insert into HNSW index
		if err := s.hnswIndex.Insert(id, vec); err != nil {
			s.logger.Warn("failed to insert vector into HNSW index", "id", id, "error", err)
			errorCount++
			continue
		}
		insertCount++
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating rows: %w", err)
	}

	s.logger.Info("HNSW index rebuild complete", "inserted", insertCount, "errors", errorCount)

	return nil
}

// initIVFIndex initializes the IVF index if enabled
func (s *SQLiteStore) initIVFIndex(ctx context.Context) error {
	if s.config.IndexType != IndexTypeIVF {
		return nil
	}

	if s.config.VectorDim <= 0 {
		return nil // Cannot initialize without dimension
	}

	// Default to 100 centroids if not specified
	nCentroids := s.config.IVF.NCentroids
	if nCentroids <= 0 {
		nCentroids = 100
	}

	s.ivfIndex = index.NewIVFIndex(s.config.VectorDim, nCentroids)

	// Set probe count
	if s.config.IVF.NProbe > 0 {
		s.ivfIndex.SetNProbe(s.config.IVF.NProbe)
	}

	// Try to load from snapshot
	loaded, err := s.loadIndexSnapshot(ctx, "IVF")
	if err != nil {
		s.logger.Warn("failed to load IVF snapshot", "error", err)
	}

	if loaded {
		s.logger.Info("IVF index loaded from snapshot")
		return nil
	}

	s.logger.Info("IVF index initialized (not trained yet)", "nCentroids", nCentroids)

	// Note: IVF index requires training.
	// We don't automatically train here because we might not have enough data.
	// User should call TrainIndex() explicitly or we could implement auto-training later.

	return nil
}

// TrainIndex trains the index with existing data
func (s *SQLiteStore) TrainIndex(ctx context.Context, numCentroids int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return wrapError("train_index", ErrStoreClosed)
	}

	// Ensure we are in IVF mode
	if s.config.IndexType != IndexTypeIVF {
		return wrapError("train_index", fmt.Errorf("index type is not IVF"))
	}

	// Use config value if numCentroids is 0
	if numCentroids <= 0 {
		numCentroids = s.config.IVF.NCentroids
		if numCentroids <= 0 {
			numCentroids = 100
		}
	}

	if s.ivfIndex == nil {
		if s.config.VectorDim <= 0 {
			return wrapError("train_index", fmt.Errorf("vector dimension not set"))
		}
		s.ivfIndex = index.NewIVFIndex(s.config.VectorDim, numCentroids)
	} else {
		// Re-initialize to change number of centroids if needed, or just retrain
		if s.ivfIndex.NCentroids != numCentroids {
			s.ivfIndex = index.NewIVFIndex(s.config.VectorDim, numCentroids)
		} else {
			s.ivfIndex.Clear() // Clear existing data to re-train
		}
	}

	s.logger.Info("training IVF index", "nCentroids", numCentroids)

	// Fetch all vectors for training
	rows, err := s.db.QueryContext(ctx, "SELECT id, vector FROM embeddings")
	if err != nil {
		return wrapError("train_index", fmt.Errorf("failed to fetch vectors: %w", err))
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows during IVF training", "error", closeErr)
		}
	}()

	var ids []string
	var vectors [][]float32

	for rows.Next() {
		var id string
		var vectorBytes []byte
		if err := rows.Scan(&id, &vectorBytes); err != nil {
			s.logger.Warn("failed to scan row during IVF training", "error", err)
			continue
		}
		vec, err := encoding.DecodeVector(vectorBytes)
		if err != nil {
			s.logger.Warn("failed to decode vector during IVF training", "id", id, "error", err)
			continue
		}
		ids = append(ids, id)
		vectors = append(vectors, vec)
	}

	if len(vectors) == 0 {
		return wrapError("train_index", fmt.Errorf("no vectors found for training"))
	}

	// Train the index
	if err := s.ivfIndex.Train(vectors); err != nil {
		return wrapError("train_index", err)
	}

	s.logger.Info("IVF index training complete", "vectors", len(vectors))

	// Add all vectors to the index
	var errorCount int
	for i, vec := range vectors {
		if err := s.ivfIndex.Add(ids[i], vec); err != nil {
			s.logger.Warn("failed to add vector to IVF index", "id", ids[i], "error", err)
			errorCount++
		}
	}

	if errorCount > 0 {
		s.logger.Warn("some vectors failed to add to IVF index", "count", errorCount)
	}

	return nil
}

// TrainQuantizer trains the quantizer on existing vectors
func (s *SQLiteStore) TrainQuantizer(ctx context.Context) error {
	if s.quantizer == nil {
		return nil
	}

	s.logger.Info("training quantizer")

	// Sample up to 1000 vectors for training
	rows, err := s.db.QueryContext(ctx, "SELECT vector FROM embeddings LIMIT 1000")
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows during quantizer training", "error", closeErr)
		}
	}()

	var trainingVectors [][]float32
	for rows.Next() {
		var vectorBytes []byte
		if err := rows.Scan(&vectorBytes); err != nil {
			continue
		}
		vec, err := encoding.DecodeVector(vectorBytes)
		if err == nil {
			trainingVectors = append(trainingVectors, vec)
		}
	}

	if len(trainingVectors) == 0 {
		return fmt.Errorf("no vectors available for quantizer training")
	}

	if sq, ok := s.quantizer.(*quantization.ScalarQuantizer); ok {
		if err := sq.Train(trainingVectors); err != nil {
			return err
		}
		s.logger.Info("scalar quantizer trained", "vectors", len(trainingVectors))
	} else if bq, ok := s.quantizer.(*quantization.BinaryQuantizer); ok {
		if err := bq.Train(trainingVectors); err != nil {
			return err
		}
		s.logger.Info("binary quantizer trained", "vectors", len(trainingVectors))
	}

	return nil
}

// saveIndexSnapshot saves the current index to the database
func (s *SQLiteStore) saveIndexSnapshot(ctx context.Context) error {
	var buf bytes.Buffer
	var indexType string

	if s.config.IndexType == IndexTypeHNSW && s.hnswIndex != nil {
		indexType = "HNSW"
		if err := s.hnswIndex.Save(&buf); err != nil {
			return fmt.Errorf("failed to serialize HNSW index: %w", err)
		}
	} else if s.config.IndexType == IndexTypeIVF && s.ivfIndex != nil && s.ivfIndex.Trained {
		indexType = "IVF"
		if err := s.ivfIndex.Save(&buf); err != nil {
			return fmt.Errorf("failed to serialize IVF index: %w", err)
		}
	} else {
		return nil // No index to save
	}

	// Save to database
	query := `
		INSERT OR REPLACE INTO index_snapshots (type, data, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`
	_, err := s.db.ExecContext(ctx, query, indexType, buf.Bytes())
	if err != nil {
		return fmt.Errorf("failed to save index snapshot: %w", err)
	}

	s.logger.Info("index snapshot saved", "type", indexType)

	// Also save quantizer if available
	if s.quantizer != nil {
		var qBuf bytes.Buffer
		var saveErr error
		if sq, ok := s.quantizer.(*quantization.ScalarQuantizer); ok {
			saveErr = sq.Save(&qBuf)
		} else if bq, ok := s.quantizer.(*quantization.BinaryQuantizer); ok {
			saveErr = bq.Save(&qBuf)
		}

		if saveErr == nil && qBuf.Len() > 0 {
			_, err = s.db.ExecContext(ctx, "INSERT OR REPLACE INTO index_snapshots (type, data, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)", "QUANTIZER", qBuf.Bytes())
			if err != nil {
				s.logger.Warn("failed to save quantizer snapshot", "error", err)
			} else {
				s.logger.Info("quantizer snapshot saved")
			}
		}
	}

	return nil
}

// loadIndexSnapshot tries to load the index from the database
func (s *SQLiteStore) loadIndexSnapshot(ctx context.Context, indexType string) (bool, error) {
	// First try to load quantizer if we're loading an index
	var qData []byte
	err := s.db.QueryRowContext(ctx, "SELECT data FROM index_snapshots WHERE type = ?", "QUANTIZER").Scan(&qData)
	if err == nil {
		if s.config.Quantization.Type == "binary" {
			bq := quantization.NewBinaryQuantizer(s.config.VectorDim)
			if loadErr := bq.Load(bytes.NewReader(qData)); loadErr == nil {
				s.quantizer = bq
				s.logger.Info("binary quantizer loaded from snapshot")
			}
		} else {
			sq, _ := quantization.NewScalarQuantizer(s.config.VectorDim, s.config.Quantization.NBits)
			if loadErr := sq.Load(bytes.NewReader(qData)); loadErr == nil {
				s.quantizer = sq
				s.logger.Info("scalar quantizer loaded from snapshot")
			}
		}

		if s.quantizer != nil && s.hnswIndex != nil {
			s.hnswIndex.SetQuantizer(s.quantizer)
		}
	}

	var data []byte
	err = s.db.QueryRowContext(ctx, "SELECT data FROM index_snapshots WHERE type = ?", indexType).Scan(&data)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to query index snapshot: %w", err)
	}

	buf := bytes.NewReader(data)

	if indexType == "HNSW" && s.hnswIndex != nil {
		if err := s.hnswIndex.Load(buf); err != nil {
			return false, fmt.Errorf("failed to deserialize HNSW index: %w", err)
		}
		return true, nil
	} else if indexType == "IVF" && s.ivfIndex != nil {
		if err := s.ivfIndex.Load(buf); err != nil {
			return false, fmt.Errorf("failed to deserialize IVF index: %w", err)
		}
		return true, nil
	}

	return false, nil
}
