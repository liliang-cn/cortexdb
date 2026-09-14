// pkg/importflow/sink_kg.go
package importflow

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// kgSink batches RDF triples and writes them through UpsertKnowledgeGraph.
type kgSink struct {
	db         *cortexdb.DB
	batchSize  int
	provenance map[string]string
	pending    []graph.RDFTriple
	written    int
}

func newKGSink(db *cortexdb.DB, batchSize int, provenance map[string]string) *kgSink {
	if batchSize <= 0 {
		batchSize = 500
	}
	return &kgSink{db: db, batchSize: batchSize, provenance: provenance}
}

func (s *kgSink) add(ctx context.Context, triples []graph.RDFTriple) error {
	// Stamped here rather than where each triple is built, because every path
	// that produces one — column mapping, relation mapping, AI extraction from
	// a text column — reaches the store through this sink, and a path that
	// forgot would produce facts indistinguishable from the anonymous ones
	// this exists to end.
	if len(s.provenance) > 0 {
		for i := range triples {
			if triples[i].Provenance == nil {
				triples[i].Provenance = s.provenance
			}
		}
	}
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
