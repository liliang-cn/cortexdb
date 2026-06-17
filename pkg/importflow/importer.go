// pkg/importflow/importer.go
package importflow

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// Importer orchestrates Source -> MappingPlan -> RAG + KG sinks.
type Importer struct {
	db        *cortexdb.DB
	inferer   MappingInferer
	refiner   TextRefiner
	extractor graphflow.Extractor
	batchSize int
	strict    bool
}

// Option configures an Importer.
type Option func(*Importer)

// WithMappingInferer sets the AI mapping inferer used by Plan/AutoImport.
func WithMappingInferer(m MappingInferer) Option { return func(im *Importer) { im.inferer = m } }

// WithTextRefiner sets the AI text refiner used when RAGPlan.Refine is true.
func WithTextRefiner(r TextRefiner) Option { return func(im *Importer) { im.refiner = r } }

// WithExtractor sets the graphflow extractor used for KGPlan.TextExtract columns.
func WithExtractor(e graphflow.Extractor) Option { return func(im *Importer) { im.extractor = e } }

// WithBatchSize sets the sink batch size (default 500).
func WithBatchSize(n int) Option { return func(im *Importer) { im.batchSize = n } }

// WithStrictMode aborts the run on the first row error instead of collecting it.
func WithStrictMode() Option { return func(im *Importer) { im.strict = true } }

// New constructs an Importer over an open cortexdb.DB.
func New(db *cortexdb.DB, opts ...Option) *Importer {
	im := &Importer{db: db, batchSize: 500}
	for _, o := range opts {
		o(im)
	}
	return im
}

// Plan asks the configured MappingInferer to propose a plan from the source schemas.
func (im *Importer) Plan(ctx context.Context, src Source, goal Goal) (MappingPlan, error) {
	if im.inferer == nil {
		return MappingPlan{}, fmt.Errorf("importflow: Plan requires a MappingInferer (use WithMappingInferer)")
	}
	schemas, err := src.Schemas(ctx)
	if err != nil {
		return MappingPlan{}, fmt.Errorf("importflow: read schemas: %w", err)
	}
	return im.inferer.InferPlan(ctx, schemas, goal)
}

// AutoImport runs Plan then Run in one step.
func (im *Importer) AutoImport(ctx context.Context, src Source, goal Goal) (*Report, error) {
	plan, err := im.Plan(ctx, src, goal)
	if err != nil {
		return nil, err
	}
	return im.Run(ctx, src, plan)
}

// Run streams every record through the plan into the RAG and KG sinks.
func (im *Importer) Run(ctx context.Context, src Source, plan MappingPlan) (*Report, error) {
	rep := &Report{}
	if ds, ok := src.(*SQLDumpSource); ok {
		rep.UnparsedStatements = ds.Unparsed()
	}
	rag := newRAGSink(im.db, im.batchSize)
	kg := newKGSink(im.db, im.batchSize)

	err := src.Records(ctx, func(r Record) error {
		rep.RowsRead++
		tp, ok := plan.Tables[r.Table]
		if !ok || tp.Skip {
			rep.Skipped++
			return nil
		}
		if err := im.processRow(ctx, r, tp, rag, kg, rep); err != nil {
			if im.strict {
				return err
			}
			rep.addError(err)
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	if err := rag.flush(ctx); err != nil {
		return rep, err
	}
	if err := kg.flush(ctx); err != nil {
		return rep, err
	}
	rep.ChunksIndexed = rag.count()
	rep.TriplesCreated = kg.count()
	return rep, nil
}

func (im *Importer) processRow(ctx context.Context, r Record, tp TablePlan, rag *ragSink, kg *kgSink, rep *Report) error {
	if tp.RAG != nil {
		if chunk, ok := mapRAG(tp.RAG, r); ok {
			if chunk.refine && im.refiner != nil {
				refined, err := im.refiner.Refine(ctx, r.Table, tp.RAG.ContentTmpl, chunk.content)
				if err != nil {
					return err
				}
				chunk.content = refined
			}
			if err := rag.add(ctx, chunk); err != nil {
				return err
			}
		}
	}
	if tp.KG != nil {
		triples := mapTriples(tp.KG, r)
		if len(triples) > 0 {
			if err := kg.add(ctx, triples); err != nil {
				return err
			}
		}
		if im.extractor != nil {
			extracted, err := im.extractFromText(ctx, r, tp.KG)
			if err != nil {
				return err
			}
			if len(extracted) > 0 {
				if err := kg.add(ctx, extracted); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// extractFromText runs the graphflow extractor over configured free-text columns
// and converts the resulting edges into RDF triples.
func (im *Importer) extractFromText(ctx context.Context, r Record, kp *KGPlan) ([]graph.RDFTriple, error) {
	var triples []graph.RDFTriple
	for _, te := range kp.TextExtract {
		text, ok := r.Get(te.Column)
		if !ok || text == "" {
			continue
		}
		doc := graphflow.SourceDocument{
			ID:      fmt.Sprintf("%s:%d:%s", r.Table, r.Row, te.Column),
			Content: text,
		}
		res, err := im.extractor.Extract(ctx, doc)
		if err != nil {
			return nil, err
		}
		for _, e := range res.Edges {
			src := strings.TrimSpace(e.Source)
			tgt := strings.TrimSpace(e.Target)
			if src == "" || tgt == "" {
				continue
			}
			// RDF subjects must be IRIs/blank nodes, so mint stable IRIs for the
			// extracted entities and keep the original text as an rdfs:label.
			si := graph.NewIRI(textEntityIRI(src))
			oi := graph.NewIRI(textEntityIRI(tgt))
			triples = append(triples,
				graph.RDFTriple{Subject: si, Predicate: graph.NewIRI(rdfsLabelIRI), Object: graph.NewLiteral(src)},
				graph.RDFTriple{Subject: oi, Predicate: graph.NewIRI(rdfsLabelIRI), Object: graph.NewLiteral(tgt)},
				graph.RDFTriple{Subject: si, Predicate: graph.NewIRI("urn:cortexdb:rel:" + e.Relation), Object: oi},
			)
		}
	}
	return triples, nil
}

const rdfsLabelIRI = "http://www.w3.org/2000/01/rdf-schema#label"

// textEntityIRI builds a stable IRI for an entity extracted from free text.
func textEntityIRI(name string) string {
	return "urn:cortexdb:text:" + url.PathEscape(strings.ToLower(strings.TrimSpace(name)))
}
