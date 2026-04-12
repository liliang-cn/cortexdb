package cortexdb

import "context"

// UpsertKnowledgeNamespace stores one knowledge-graph namespace via the toolbox surface.
func (t *GraphRAGToolbox) UpsertKnowledgeNamespace(ctx context.Context, req KnowledgeGraphNamespaceUpsertRequest) (*KnowledgeGraphNamespaceUpsertResponse, error) {
	return t.db.UpsertKnowledgeNamespace(ctx, req)
}

// ListKnowledgeNamespaces returns all knowledge-graph namespaces via the toolbox surface.
func (t *GraphRAGToolbox) ListKnowledgeNamespaces(ctx context.Context) (*KnowledgeGraphNamespaceListResponse, error) {
	return t.db.ListKnowledgeNamespaces(ctx)
}

// UpsertKnowledgeGraph writes triples/quads via the toolbox surface.
func (t *GraphRAGToolbox) UpsertKnowledgeGraph(ctx context.Context, req KnowledgeGraphUpsertRequest) (*KnowledgeGraphUpsertResponse, error) {
	return t.db.UpsertKnowledgeGraph(ctx, req)
}

// FindKnowledgeGraph queries triples/quads via the toolbox surface.
func (t *GraphRAGToolbox) FindKnowledgeGraph(ctx context.Context, req KnowledgeGraphFindRequest) (*KnowledgeGraphFindResponse, error) {
	return t.db.FindKnowledgeGraph(ctx, req)
}

// DeleteKnowledgeGraph removes triples/quads via the toolbox surface.
func (t *GraphRAGToolbox) DeleteKnowledgeGraph(ctx context.Context, req KnowledgeGraphDeleteRequest) (*KnowledgeGraphDeleteResponse, error) {
	return t.db.DeleteKnowledgeGraph(ctx, req)
}

// ImportKnowledgeGraph imports RDF content via the toolbox surface.
func (t *GraphRAGToolbox) ImportKnowledgeGraph(ctx context.Context, req KnowledgeGraphImportRequest) (*KnowledgeGraphImportResponse, error) {
	return t.db.ImportKnowledgeGraph(ctx, req)
}

// ExportKnowledgeGraph exports RDF content via the toolbox surface.
func (t *GraphRAGToolbox) ExportKnowledgeGraph(ctx context.Context, req KnowledgeGraphExportRequest) (*KnowledgeGraphExportResponse, error) {
	return t.db.ExportKnowledgeGraph(ctx, req)
}

// QueryKnowledgeGraph executes a SPARQL query via the toolbox surface.
func (t *GraphRAGToolbox) QueryKnowledgeGraph(ctx context.Context, req KnowledgeGraphQueryRequest) (*KnowledgeGraphQueryResponse, error) {
	return t.db.QueryKnowledgeGraph(ctx, req)
}

// ValidateKnowledgeGraphSHACL validates graph data with supplied SHACL-lite shape triples via the toolbox surface.
func (t *GraphRAGToolbox) ValidateKnowledgeGraphSHACL(ctx context.Context, req KnowledgeGraphSHACLValidateRequest) (*KnowledgeGraphSHACLValidateResponse, error) {
	return t.db.ValidateKnowledgeGraphSHACL(ctx, req)
}

// RefreshKnowledgeGraphInference recomputes inferred triples via the toolbox surface.
func (t *GraphRAGToolbox) RefreshKnowledgeGraphInference(ctx context.Context, req KnowledgeGraphInferenceRefreshRequest) (*KnowledgeGraphInferenceRefreshResponse, error) {
	return t.db.RefreshKnowledgeGraphInference(ctx, req)
}

// SummarizeKnowledgeGraphInference returns persisted inference counts and rule breakdowns via the toolbox surface.
func (t *GraphRAGToolbox) SummarizeKnowledgeGraphInference(ctx context.Context, req KnowledgeGraphInferenceSummaryRequest) (*KnowledgeGraphInferenceSummaryResponse, error) {
	return t.db.SummarizeKnowledgeGraphInference(ctx, req)
}

// ExplainKnowledgeGraphInference fetches provenance for a triple via the toolbox surface.
func (t *GraphRAGToolbox) ExplainKnowledgeGraphInference(ctx context.Context, req KnowledgeGraphInferenceExplainRequest) (*KnowledgeGraphInferenceExplainResponse, error) {
	return t.db.ExplainKnowledgeGraphInference(ctx, req)
}

// ExplainKnowledgeGraphInferenceMatch fetches explanations for triples matched by a pattern.
func (t *GraphRAGToolbox) ExplainKnowledgeGraphInferenceMatch(ctx context.Context, req KnowledgeGraphInferenceExplainMatchRequest) (*KnowledgeGraphInferenceExplainMatchResponse, error) {
	return t.db.ExplainKnowledgeGraphInferenceMatch(ctx, req)
}
