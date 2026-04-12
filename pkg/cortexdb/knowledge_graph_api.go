package cortexdb

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// UpsertKnowledgeNamespace stores or replaces one knowledge-graph namespace.
func (db *DB) UpsertKnowledgeNamespace(ctx context.Context, req KnowledgeGraphNamespaceUpsertRequest) (*KnowledgeGraphNamespaceUpsertResponse, error) {
	ns := graph.Namespace{
		Prefix: req.Prefix,
		URI:    req.URI,
	}
	if err := db.graph.UpsertNamespace(ctx, ns); err != nil {
		return nil, err
	}
	return &KnowledgeGraphNamespaceUpsertResponse{Namespace: ns}, nil
}

// ListKnowledgeNamespaces returns the registered knowledge-graph namespaces.
func (db *DB) ListKnowledgeNamespaces(ctx context.Context) (*KnowledgeGraphNamespaceListResponse, error) {
	namespaces, err := db.graph.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	return &KnowledgeGraphNamespaceListResponse{Namespaces: namespaces}, nil
}

// UpsertKnowledgeGraph writes RDF triples/quads into the embedded knowledge graph.
func (db *DB) UpsertKnowledgeGraph(ctx context.Context, req KnowledgeGraphUpsertRequest) (*KnowledgeGraphUpsertResponse, error) {
	if len(req.Triples) == 0 {
		return nil, fmt.Errorf("triples are required")
	}

	triples := make([]*graph.RDFTriple, 0, len(req.Triples))
	ids := make([]string, 0, len(req.Triples))
	for i := range req.Triples {
		triple := req.Triples[i]
		triples = append(triples, &triple)
	}

	result, err := db.graph.UpsertTriplesBatch(ctx, triples)
	if err != nil {
		return nil, err
	}
	if result.FailedCount > 0 {
		return nil, result.Errors[0]
	}
	for _, triple := range triples {
		ids = append(ids, triple.ID)
	}
	return &KnowledgeGraphUpsertResponse{
		TripleIDs: ids,
		Count:     len(ids),
	}, nil
}

// FindKnowledgeGraph queries triples/quads from the embedded knowledge graph.
func (db *DB) FindKnowledgeGraph(ctx context.Context, req KnowledgeGraphFindRequest) (*KnowledgeGraphFindResponse, error) {
	triples, err := db.graph.FindTriples(ctx, graph.TriplePattern(req.Pattern))
	if err != nil {
		return nil, err
	}
	return &KnowledgeGraphFindResponse{Triples: triples}, nil
}

// DeleteKnowledgeGraph removes triples/quads from the embedded knowledge graph.
func (db *DB) DeleteKnowledgeGraph(ctx context.Context, req KnowledgeGraphDeleteRequest) (*KnowledgeGraphDeleteResponse, error) {
	deleted := 0

	for _, id := range req.TripleIDs {
		triple, err := db.graph.GetTriple(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := db.graph.DeleteTriple(ctx, *triple); err != nil {
			return nil, err
		}
		deleted++
	}

	for _, triple := range req.Triples {
		if err := db.graph.DeleteTriple(ctx, graph.RDFTriple(triple)); err != nil {
			return nil, err
		}
		deleted++
	}

	if req.Pattern != nil {
		count, err := db.graph.DeleteTriples(ctx, graph.TriplePattern(*req.Pattern))
		if err != nil {
			return nil, err
		}
		deleted += count
	}

	return &KnowledgeGraphDeleteResponse{Deleted: deleted}, nil
}

// ImportKnowledgeGraph parses RDF content into the embedded knowledge graph.
func (db *DB) ImportKnowledgeGraph(ctx context.Context, req KnowledgeGraphImportRequest) (*KnowledgeGraphImportResponse, error) {
	if strings.TrimSpace(req.Content) == "" {
		return nil, ErrEmptyText
	}
	format, err := parseKnowledgeGraphFormat(req.Format)
	if err != nil {
		return nil, err
	}
	count, err := db.graph.ImportRDF(ctx, strings.NewReader(req.Content), format)
	if err != nil {
		return nil, err
	}
	return &KnowledgeGraphImportResponse{
		Format: string(format),
		Count:  count,
	}, nil
}

// ExportKnowledgeGraph serializes RDF content from the embedded knowledge graph.
func (db *DB) ExportKnowledgeGraph(ctx context.Context, req KnowledgeGraphExportRequest) (*KnowledgeGraphExportResponse, error) {
	format, err := parseKnowledgeGraphFormat(req.Format)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err := db.graph.ExportRDF(ctx, &buffer, format); err != nil {
		return nil, err
	}
	return &KnowledgeGraphExportResponse{
		Format:  string(format),
		Content: buffer.String(),
	}, nil
}

// QueryKnowledgeGraph executes a SPARQL SELECT/ASK subset against the embedded knowledge graph.
func (db *DB) QueryKnowledgeGraph(ctx context.Context, req KnowledgeGraphQueryRequest) (*KnowledgeGraphQueryResponse, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, ErrEmptyText
	}
	result, err := db.graph.ExecuteSPARQL(ctx, req.Query)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return &KnowledgeGraphQueryResponse{}, nil
	}
	return &KnowledgeGraphQueryResponse{Result: *result}, nil
}

// ValidateKnowledgeGraphSHACL validates graph data with supplied SHACL-lite shape triples.
func (db *DB) ValidateKnowledgeGraphSHACL(ctx context.Context, req KnowledgeGraphSHACLValidateRequest) (*KnowledgeGraphSHACLValidateResponse, error) {
	if len(req.Shapes) == 0 {
		return nil, fmt.Errorf("shapes are required")
	}
	report, err := db.graph.ValidateSHACL(ctx, req.Shapes)
	if err != nil {
		return nil, err
	}
	if report == nil {
		return &KnowledgeGraphSHACLValidateResponse{}, nil
	}
	return &KnowledgeGraphSHACLValidateResponse{Report: *report}, nil
}

// RefreshKnowledgeGraphInference recomputes persisted RDFS-lite inferred triples.
func (db *DB) RefreshKnowledgeGraphInference(ctx context.Context, req KnowledgeGraphInferenceRefreshRequest) (*KnowledgeGraphInferenceRefreshResponse, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = KnowledgeGraphInferenceRefreshModeFull
		if len(req.TripleIDs) > 0 || len(req.Triples) > 0 || req.Pattern != nil {
			mode = KnowledgeGraphInferenceRefreshModeIncremental
		}
	}

	var (
		result *graph.RDFSInferenceRefreshResult
		err    error
	)
	if mode == KnowledgeGraphInferenceRefreshModeIncremental {
		seeds := make([]graph.RDFTriple, 0, len(req.TripleIDs)+len(req.Triples))
		for _, id := range req.TripleIDs {
			triple, getErr := db.graph.GetTriple(ctx, id)
			if getErr != nil {
				return nil, getErr
			}
			seeds = append(seeds, *triple)
		}
		seeds = append(seeds, req.Triples...)
		if req.Pattern != nil {
			matches, findErr := db.graph.FindTriples(ctx, graph.TriplePattern(*req.Pattern))
			if findErr != nil {
				return nil, findErr
			}
			seeds = append(seeds, matches...)
		}
		if len(seeds) == 0 {
			return nil, fmt.Errorf("incremental inference refresh requires triples, triple_ids, or a pattern")
		}
		result, err = db.graph.RefreshRDFSInferencesIncremental(ctx, seeds)
	} else {
		result, err = db.graph.RefreshRDFSInferences(ctx)
	}
	if err != nil {
		return nil, err
	}
	if result == nil {
		return &KnowledgeGraphInferenceRefreshResponse{}, nil
	}
	return &KnowledgeGraphInferenceRefreshResponse{Result: *result}, nil
}

// SummarizeKnowledgeGraphInference returns persisted inference counts and rule breakdowns.
func (db *DB) SummarizeKnowledgeGraphInference(ctx context.Context, _ KnowledgeGraphInferenceSummaryRequest) (*KnowledgeGraphInferenceSummaryResponse, error) {
	result, err := db.graph.InferenceSummary(ctx)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return &KnowledgeGraphInferenceSummaryResponse{}, nil
	}
	return &KnowledgeGraphInferenceSummaryResponse{Result: *result}, nil
}

// ExplainKnowledgeGraphInference returns provenance for one explicit or inferred triple.
func (db *DB) ExplainKnowledgeGraphInference(ctx context.Context, req KnowledgeGraphInferenceExplainRequest) (*KnowledgeGraphInferenceExplainResponse, error) {
	if strings.TrimSpace(req.TripleID) == "" {
		return nil, fmt.Errorf("triple_id is required")
	}
	explanation, err := db.graph.ExplainTriple(ctx, req.TripleID)
	if err != nil {
		return nil, err
	}
	if explanation == nil {
		return &KnowledgeGraphInferenceExplainResponse{}, nil
	}
	response := &KnowledgeGraphInferenceExplainResponse{Explanation: *explanation}
	if req.Depth > 0 {
		trace, err := db.graph.ExplainTripleTrace(ctx, req.TripleID, req.Depth)
		if err != nil {
			return nil, err
		}
		response.Trace = trace
	}
	return response, nil
}

// ExplainKnowledgeGraphInferenceMatch expands explanations for all triples matched by a pattern.
func (db *DB) ExplainKnowledgeGraphInferenceMatch(ctx context.Context, req KnowledgeGraphInferenceExplainMatchRequest) (*KnowledgeGraphInferenceExplainMatchResponse, error) {
	matches, err := db.graph.ExplainTriplesByPattern(ctx, graph.TriplePattern(req.Pattern), req.Depth)
	if err != nil {
		return nil, err
	}
	return &KnowledgeGraphInferenceExplainMatchResponse{Matches: matches}, nil
}

func parseKnowledgeGraphFormat(value string) (graph.RDFFormat, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", KnowledgeGraphFormatNQuads:
		return graph.RDFFormatNQuads, nil
	case KnowledgeGraphFormatNTriples:
		return graph.RDFFormatNTriples, nil
	case KnowledgeGraphFormatTurtle:
		return graph.RDFFormatTurtle, nil
	case KnowledgeGraphFormatTriG:
		return graph.RDFFormatTriG, nil
	default:
		return "", fmt.Errorf("unsupported knowledge graph format: %s", value)
	}
}
