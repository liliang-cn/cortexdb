package rpcserver

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type graphService struct {
	rpcv1.UnimplementedKnowledgeGraphServiceServer
	db *cortexdb.DB
}

func (s *graphService) UpsertNamespace(ctx context.Context, req *rpcv1.UpsertNamespaceRequest) (*rpcv1.UpsertNamespaceResponse, error) {
	resp, err := s.db.UpsertKnowledgeNamespace(ctx, cortexdb.KnowledgeGraphNamespaceUpsertRequest{
		Prefix: req.GetPrefix(),
		URI:    req.GetUri(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.UpsertNamespaceResponse{
		Namespace: &rpcv1.GraphNamespace{Prefix: resp.Namespace.Prefix, Uri: resp.Namespace.URI},
	}, nil
}

func (s *graphService) ListNamespaces(ctx context.Context, _ *rpcv1.ListNamespacesRequest) (*rpcv1.ListNamespacesResponse, error) {
	resp, err := s.db.ListKnowledgeNamespaces(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	namespaces := make([]*rpcv1.GraphNamespace, 0, len(resp.Namespaces))
	for _, ns := range resp.Namespaces {
		namespaces = append(namespaces, &rpcv1.GraphNamespace{Prefix: ns.Prefix, Uri: ns.URI})
	}
	return &rpcv1.ListNamespacesResponse{Namespaces: namespaces}, nil
}

func (s *graphService) UpsertKnowledgeGraph(ctx context.Context, req *rpcv1.UpsertKnowledgeGraphRequest) (*rpcv1.UpsertKnowledgeGraphResponse, error) {
	resp, err := s.db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
		Triples: triplesFromProto(req.GetTriples()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.UpsertKnowledgeGraphResponse{TripleIds: resp.TripleIDs, Count: int32(resp.Count)}, nil
}

func (s *graphService) FindKnowledgeGraph(ctx context.Context, req *rpcv1.FindKnowledgeGraphRequest) (*rpcv1.FindKnowledgeGraphResponse, error) {
	resp, err := s.db.FindKnowledgeGraph(ctx, cortexdb.KnowledgeGraphFindRequest{
		Pattern: patternFromProto(req.GetPattern()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.FindKnowledgeGraphResponse{Triples: triplesToProto(resp.Triples)}, nil
}

func (s *graphService) DeleteKnowledgeGraph(ctx context.Context, req *rpcv1.DeleteKnowledgeGraphRequest) (*rpcv1.DeleteKnowledgeGraphResponse, error) {
	resp, err := s.db.DeleteKnowledgeGraph(ctx, cortexdb.KnowledgeGraphDeleteRequest{
		TripleIDs: req.GetTripleIds(),
		Triples:   triplesFromProto(req.GetTriples()),
		Pattern:   patternPtrFromProto(req.GetPattern()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.DeleteKnowledgeGraphResponse{Deleted: int32(resp.Deleted)}, nil
}

func (s *graphService) ImportKnowledgeGraph(ctx context.Context, req *rpcv1.ImportKnowledgeGraphRequest) (*rpcv1.ImportKnowledgeGraphResponse, error) {
	resp, err := s.db.ImportKnowledgeGraph(ctx, cortexdb.KnowledgeGraphImportRequest{
		Format:  req.GetFormat(),
		Content: req.GetContent(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.ImportKnowledgeGraphResponse{Format: resp.Format, Count: int32(resp.Count)}, nil
}

func (s *graphService) ExportKnowledgeGraph(ctx context.Context, req *rpcv1.ExportKnowledgeGraphRequest) (*rpcv1.ExportKnowledgeGraphResponse, error) {
	resp, err := s.db.ExportKnowledgeGraph(ctx, cortexdb.KnowledgeGraphExportRequest{Format: req.GetFormat()})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.ExportKnowledgeGraphResponse{Format: resp.Format, Content: resp.Content}, nil
}

func (s *graphService) QuerySparql(ctx context.Context, req *rpcv1.QuerySparqlRequest) (*rpcv1.QuerySparqlResponse, error) {
	resp, err := s.db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: req.GetQuery()})
	if err != nil {
		return nil, toStatus(err)
	}
	bindings := make([]*rpcv1.SparqlBinding, 0, len(resp.Result.Bindings))
	for _, b := range resp.Result.Bindings {
		vars := make(map[string]*rpcv1.RdfTerm, len(b))
		for name, term := range b {
			vars[name] = termToProto(term)
		}
		bindings = append(bindings, &rpcv1.SparqlBinding{Vars: vars})
	}
	return &rpcv1.QuerySparqlResponse{Result: &rpcv1.SparqlResult{
		QueryType: resp.Result.QueryType,
		Vars:      resp.Result.Vars,
		Bindings:  bindings,
		Triples:   triplesToProto(resp.Result.Triples),
		Boolean:   resp.Result.Boolean,
		Count:     int32(resp.Result.Count),
	}}, nil
}

func (s *graphService) ValidateShacl(ctx context.Context, req *rpcv1.ValidateShaclRequest) (*rpcv1.ValidateShaclResponse, error) {
	resp, err := s.db.ValidateKnowledgeGraphSHACL(ctx, cortexdb.KnowledgeGraphSHACLValidateRequest{
		Shapes: triplesFromProto(req.GetShapes()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	results := make([]*rpcv1.ShaclValidationResult, 0, len(resp.Report.Results))
	for _, r := range resp.Report.Results {
		results = append(results, &rpcv1.ShaclValidationResult{
			FocusNode:   termToProto(r.FocusNode),
			Path:        termToProto(r.Path),
			Value:       termToProto(r.Value),
			Message:     r.Message,
			Severity:    r.Severity,
			SourceShape: termToProto(r.Source),
		})
	}
	return &rpcv1.ValidateShaclResponse{Report: &rpcv1.ShaclReport{
		Conforms: resp.Report.Conforms,
		Results:  results,
	}}, nil
}

func (s *graphService) RefreshInference(ctx context.Context, req *rpcv1.RefreshInferenceRequest) (*rpcv1.RefreshInferenceResponse, error) {
	resp, err := s.db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{
		Mode:      req.GetMode(),
		TripleIDs: req.GetTripleIds(),
		Triples:   triplesFromProto(req.GetTriples()),
		Pattern:   patternPtrFromProto(req.GetPattern()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.RefreshInferenceResponse{Result: &rpcv1.InferenceRefreshResult{
		ExplicitCount:         int32(resp.Result.ExplicitCount),
		InferredCount:         int32(resp.Result.InferredCount),
		Incremental:           resp.Result.Incremental,
		AffectedExplicitCount: int32(resp.Result.AffectedExplicitCount),
		RemovedInferredCount:  int32(resp.Result.RemovedInferredCount),
	}}, nil
}

func (s *graphService) SummarizeInference(ctx context.Context, _ *rpcv1.SummarizeInferenceRequest) (*rpcv1.SummarizeInferenceResponse, error) {
	resp, err := s.db.SummarizeKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceSummaryRequest{})
	if err != nil {
		return nil, toStatus(err)
	}
	rules := make(map[string]int32, len(resp.Result.Rules))
	for name, count := range resp.Result.Rules {
		rules[name] = int32(count)
	}
	return &rpcv1.SummarizeInferenceResponse{Result: &rpcv1.InferenceSummary{
		ExplicitCount: int32(resp.Result.ExplicitCount),
		InferredCount: int32(resp.Result.InferredCount),
		Rules:         rules,
	}}, nil
}

func (s *graphService) ExplainInference(ctx context.Context, req *rpcv1.ExplainInferenceRequest) (*rpcv1.ExplainInferenceResponse, error) {
	resp, err := s.db.ExplainKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceExplainRequest{
		TripleID: req.GetTripleId(),
		Depth:    int(req.GetDepth()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.ExplainInferenceResponse{
		Explanation: explanationToProto(resp.Explanation),
		Trace:       traceToProto(resp.Trace),
	}, nil
}

func (s *graphService) ExplainInferenceMatch(ctx context.Context, req *rpcv1.ExplainInferenceMatchRequest) (*rpcv1.ExplainInferenceMatchResponse, error) {
	resp, err := s.db.ExplainKnowledgeGraphInferenceMatch(ctx, cortexdb.KnowledgeGraphInferenceExplainMatchRequest{
		Pattern: patternFromProto(req.GetPattern()),
		Depth:   int(req.GetDepth()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	matches := make([]*rpcv1.InferenceMatchExplanation, 0, len(resp.Matches))
	for _, m := range resp.Matches {
		matches = append(matches, &rpcv1.InferenceMatchExplanation{
			Explanation: explanationToProto(m.Explanation),
			Trace:       traceToProto(m.Trace),
		})
	}
	return &rpcv1.ExplainInferenceMatchResponse{Matches: matches}, nil
}

func (s *graphService) SaveOntologySchema(ctx context.Context, req *rpcv1.SaveOntologySchemaRequest) (*rpcv1.SaveOntologySchemaResponse, error) {
	resp, err := s.db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{
		SchemaID:      req.GetSchemaId(),
		Name:          req.GetName(),
		Description:   req.GetDescription(),
		Version:       int(req.GetVersion()),
		Activate:      req.GetActivate(),
		Deactivate:    req.GetDeactivate(),
		Metadata:      req.GetMetadata(),
		EntityTypes:   ontologyEntityTypesFromProto(req.GetEntityTypes()),
		RelationTypes: ontologyRelationTypesFromProto(req.GetRelationTypes()),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.SaveOntologySchemaResponse{Schema: ontologySchemaToProto(resp.Schema)}, nil
}

func (s *graphService) GetOntologySchema(ctx context.Context, req *rpcv1.GetOntologySchemaRequest) (*rpcv1.GetOntologySchemaResponse, error) {
	resp, err := s.db.GetOntologySchema(ctx, cortexdb.OntologyGetRequest{SchemaID: req.GetSchemaId()})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.GetOntologySchemaResponse{Schema: ontologySchemaToProto(resp.Schema)}, nil
}

func (s *graphService) ListOntologySchemas(ctx context.Context, req *rpcv1.ListOntologySchemasRequest) (*rpcv1.ListOntologySchemasResponse, error) {
	resp, err := s.db.ListOntologySchemas(ctx, cortexdb.OntologyListRequest{ActiveOnly: req.GetActiveOnly()})
	if err != nil {
		return nil, toStatus(err)
	}
	schemas := make([]*rpcv1.OntologySchema, 0, len(resp.Schemas))
	for _, schema := range resp.Schemas {
		schemas = append(schemas, ontologySchemaToProto(schema))
	}
	return &rpcv1.ListOntologySchemasResponse{Schemas: schemas}, nil
}

func (s *graphService) DeleteOntologySchema(ctx context.Context, req *rpcv1.DeleteOntologySchemaRequest) (*rpcv1.DeleteOntologySchemaResponse, error) {
	resp, err := s.db.DeleteOntologySchema(ctx, cortexdb.OntologyDeleteRequest{SchemaID: req.GetSchemaId()})
	if err != nil {
		return nil, toStatus(err)
	}
	return &rpcv1.DeleteOntologySchemaResponse{SchemaId: resp.SchemaID, Deleted: resp.Deleted}, nil
}
