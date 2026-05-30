// pkg/importflow/sink_kg.go
package importflow

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// kgSink batches RDF triples and writes them through UpsertKnowledgeGraph.
type kgSink struct {
	db        *cortexdb.DB
	batchSize int
	pending   []graph.RDFTriple
	written   int
}

func newKGSink(db *cortexdb.DB, batchSize int) *kgSink {
	if batchSize <= 0 {
		batchSize = 500
	}
	return &kgSink{db: db, batchSize: batchSize}
}

func (s *kgSink) add(ctx context.Context, triples []graph.RDFTriple) error {
	s.pending = append(s.pending, triples...)
	if len(s.pending) >= s.batchSize {
		return s.flush(ctx)
	}
	return nil
}

func (s *kgSink) flush(ctx context.Context) error {
	if len(s.pending) == 0 {
		return nil
	}
	resp, err := s.db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
		Triples: s.pending,
	})
	if err != nil {
		return err
	}
	s.written += resp.Count
	s.pending = s.pending[:0]
	return nil
}

func (s *kgSink) count() int { return s.written }
