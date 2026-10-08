package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/index"
	"github.com/liliang-cn/cortexdb/v2/pkg/quantization"
)

// initHNSWIndex initializes the HNSW index if enabled in configuration
func (s *SQLiteStore) initHNSWIndex(ctx context.Context) error {
	if !s.config.HNSW.Enabled {
		return nil
	}

	// A quantized index needs its dimension before the first vector goes in.
	// Reopening a store whose caller left the dimension to auto-detection
	// would otherwise build the index from float32 vectors, the very memory
	// quantization was turned on to save.
	if s.config.Quantization.Enabled && s.config.VectorDim == 0 {
		if dim := s.storedVectorDim(ctx); dim > 0 {
			s.config.VectorDim = dim
		}
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

// storedVectorDim is the dimension of a vector already stored, or 0.
func (s *SQLiteStore) storedVectorDim(ctx context.Context) int {
	var vectorBytes []byte
	if err := s.db.QueryRowContext(ctx, "SELECT vector FROM embeddings LIMIT 1").Scan(&vectorBytes); err != nil {
		return 0
	}
	vec, err := encoding.DecodeVector(vectorBytes)
	if err != nil {
		return 0
	}
	return len(vec)
}

// rebuildHNSWIndex rebuilds the HNSW index from the stored vectors.
//
// It streams: rows are inserted a chunk at a time as they arrive, so one
// chunk of float32 vectors is alive at once rather than a decoded copy of
// every stored vector beside the index being built. And it builds one graph
// on one goroutine. It used to split the vectors among four workers, build
// a graph from each and merge them, which left four graphs that hardly
// linked to each other: on 20,000 clustered 768-d vectors recall@10 was 0.24,
// against 0.73 for the same vectors inserted one after another.
func (s *SQLiteStore) rebuildHNSWIndex(ctx context.Context) error {
	if s.hnswIndex == nil {
		return nil
	}
	s.logger.Info("rebuilding HNSW index from database")

	rows, err := s.db.QueryContext(ctx, "SELECT id, vector FROM embeddings")
	if err != nil {
		return fmt.Errorf("failed to query existing vectors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	const chunk = 256
	batch := make([]struct {
		ID     string
		Vector []float32
	}, 0, chunk)
	inserted := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.hnswIndex.InsertBatch(batch); err != nil {
			for _, v := range batch {
				if err := s.hnswIndex.Insert(v.ID, v.Vector); err != nil {
					s.logger.Warn("failed to insert vector", "id", v.ID, "error", err)
				}
			}
		}
		inserted += len(batch)
		clear(batch)
		batch = batch[:0]
	}
	for rows.Next() {
		var id string
		var vectorBytes []byte
		if err := rows.Scan(&id, &vectorBytes); err != nil {
			s.logger.Warn("failed to scan row during HNSW rebuild", "error", err)
			continue
		}
		vec, err := encoding.DecodeVector(vectorBytes)
		if err != nil {
			s.logger.Warn("failed to decode vector during HNSW rebuild", "id", id, "error", err)
			continue
		}
		batch = append(batch, struct {
			ID     string
			Vector []float32
		}{ID: id, Vector: vec})
		if len(batch) == chunk {
			flush()
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating rows: %w", err)
	}
	flush()
	s.logger.Info("HNSW index rebuild complete", "inserted", inserted)
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

// indexMutations is the persisted indexes' combined change count.
func (s *SQLiteStore) indexMutations() uint64 {
	var n uint64
	if s.hnswIndex != nil {
		n += s.hnswIndex.Mutations()
	}
	if s.ivfIndex != nil {
		n += s.ivfIndex.Mutations()
	}
	return n
}

// snapshotChanges is how many changes the index has had since its snapshot
// was last written or read. A snapshot is the whole index serialized into one
// blob, so rewriting it when nothing changed is a full copy of the index in
// memory and a full write to disk for nothing — on an SD card, wear.
func (s *SQLiteStore) snapshotChanges() uint64 {
	return s.indexMutations() - s.savedMutations.Load()
}

// saveIndexSnapshot saves the current index to the database, when it has
// changed since the snapshot already there.
func (s *SQLiteStore) saveIndexSnapshot(ctx context.Context) error {
	if s.snapshotChanges() == 0 {
		return nil
	}
	// Read before serializing: a change made while the snapshot is written
	// leaves it stale, and the next save picks it up.
	version := s.indexMutations()
	var indexType string
	var encode func(io.Writer) error

	if s.config.IndexType == IndexTypeHNSW && s.hnswIndex != nil {
		indexType, encode = "HNSW", s.hnswIndex.Save
	} else if s.config.IndexType == IndexTypeIVF && s.ivfIndex != nil && s.ivfIndex.Trained {
		indexType, encode = "IVF", s.ivfIndex.Save
	} else {
		return nil // No index to save
	}

	if err := s.writeSnapshotChunks(ctx, indexType, encode); err != nil {
		return fmt.Errorf("failed to save %s index snapshot: %w", indexType, err)
	}

	s.logger.Info("index snapshot saved", "type", indexType)
	s.savedMutations.Store(version)

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
			if _, err := s.db.ExecContext(ctx, "INSERT OR REPLACE INTO index_snapshots (type, data, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)", "QUANTIZER", qBuf.Bytes()); err != nil {
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

	// An HNSW snapshot written without quantization holds float32 vectors.
	// Loading it under a quantized configuration would keep them all, so the
	// index is rebuilt instead, as codes.
	if indexType == "HNSW" && s.config.Quantization.Enabled && qData == nil {
		s.logger.Info("HNSW snapshot is not quantized, rebuilding")
		return false, nil
	}

	buf, closeSnapshot, found, err := s.openSnapshot(ctx, indexType)
	if err != nil {
		return false, fmt.Errorf("failed to query index snapshot: %w", err)
	}
	if !found {
		return false, nil
	}
	defer closeSnapshot()

	if indexType == "HNSW" && s.hnswIndex != nil {
		if err := s.hnswIndex.Load(buf); err != nil {
			return false, fmt.Errorf("failed to deserialize HNSW index: %w", err)
		}
		s.savedMutations.Store(s.indexMutations())
		return true, nil
	} else if indexType == "IVF" && s.ivfIndex != nil {
		if err := s.ivfIndex.Load(buf); err != nil {
			return false, fmt.Errorf("failed to deserialize IVF index: %w", err)
		}
		s.savedMutations.Store(s.indexMutations())
		return true, nil
	}

	return false, nil
}

// startAutoSave starts the periodic auto-save timer
func (s *SQLiteStore) startAutoSave() {
	interval := s.config.AutoSave.Interval
	if interval <= 0 {
		interval = 5 * time.Minute // Default 5 minutes
	}

	s.saveTimer = time.AfterFunc(interval, func() {
		s.autoSaveLoop()
	})
	s.logger.Info("auto-save started", "interval", interval)
}

// autoSaveLoop runs the periodic save loop
func (s *SQLiteStore) autoSaveLoop() {
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()

	if closed {
		return
	}

	// Save only once enough has changed; Close saves whatever is left.
	minChanges := uint64(max(s.config.AutoSave.MinChanges, 1))
	if s.snapshotChanges() >= minChanges {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotWriteTimeout)
		if err := s.saveIndexSnapshot(ctx); err != nil {
			s.logger.Warn("auto-save failed", "error", err)
		} else {
			s.logger.Debug("auto-save completed")
		}
		cancel()
	}

	// Schedule next save
	s.saveMu.Lock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
	}
	interval := s.config.AutoSave.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	s.saveTimer = time.AfterFunc(interval, func() {
		s.autoSaveLoop()
	})
	s.saveMu.Unlock()
}

// IncrementChanges increments the change counter for auto-save tracking
func (s *SQLiteStore) IncrementChanges() {
	if s.config.AutoSave.Enabled {
		atomic.AddInt32(&s.changesCounter, 1)
	}
}
