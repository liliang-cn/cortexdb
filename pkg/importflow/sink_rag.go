// pkg/importflow/sink_rag.go
package importflow

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// ragSink batches ragChunks and writes them through the cortexdb facade.
// It uses SaveKnowledge, which has a lexical fallback when no embedder is
// configured, so the inserted text is still indexed for FTS5 retrieval.
type ragSink struct {
	db        *cortexdb.DB
	batchSize int
	pending   []ragChunk
	written   int
}

func newRAGSink(db *cortexdb.DB, batchSize int) *ragSink {
	if batchSize <= 0 {
		batchSize = 500
	}
	return &ragSink{db: db, batchSize: batchSize}
}

func (s *ragSink) add(ctx context.Context, c ragChunk) error {
	s.pending = append(s.pending, c)
	if len(s.pending) >= s.batchSize {
		return s.flush(ctx)
	}
	return nil
}

func (s *ragSink) flush(ctx context.Context) error {
	if len(s.pending) == 0 {
		return nil
	}
	// SaveKnowledge persists each chunk with its own ID, metadata and namespace.
	// Unlike InsertTextBatch (which requires an embedder), SaveKnowledge falls
	// back to lexical indexing when no embedder is set, keeping the FTS5 path
	// working and preserving per-chunk metadata.
	for _, c := range s.pending {
		if _, err := s.db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
			KnowledgeID: c.id,
			Content:     c.content,
			Collection:  c.namespace,
			Metadata:    c.metadata,
		}); err != nil {
			return err
		}
		s.written++
	}
	s.pending = s.pending[:0]
	return nil
}

func (s *ragSink) count() int { return s.written }
