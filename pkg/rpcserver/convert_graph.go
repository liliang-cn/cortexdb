package rpcserver

import (
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func termFromProto(t *rpcv1.RdfTerm) graph.RDFTerm {
	if t == nil {
		return graph.RDFTerm{}
	}
	return graph.RDFTerm{Kind: t.GetKind(), Value: t.GetValue(), Datatype: t.GetDatatype(), Language: t.GetLanguage()}
}

func termPtrFromProto(t *rpcv1.RdfTerm) *graph.RDFTerm {
	if t == nil {
		return nil
	}
	v := termFromProto(t)
	return &v
}

func termToProto(t graph.RDFTerm) *rpcv1.RdfTerm {
	return &rpcv1.RdfTerm{Kind: t.Kind, Value: t.Value, Datatype: t.Datatype, Language: t.Language}
}

func tripleFromProto(t *rpcv1.RdfTriple) graph.RDFTriple {
	out := graph.RDFTriple{
		ID:         t.GetId(),
		Subject:    termFromProto(t.GetSubject()),
		Predicate:  termFromProto(t.GetPredicate()),
		Object:     termFromProto(t.GetObject()),
		Inferred:   t.GetInferred(),
		Rule:       t.GetRule(),
		SupportIDs: t.GetSupportIds(),
	}
	out.Graph = termPtrFromProto(t.GetGraph())
	return out
}

func triplesFromProto(in []*rpcv1.RdfTriple) []graph.RDFTriple {
	out := make([]graph.RDFTriple, 0, len(in))
	for _, t := range in {
		out = append(out, tripleFromProto(t))
	}
	return out
}

func tripleToProto(t graph.RDFTriple) *rpcv1.RdfTriple {
	out := &rpcv1.RdfTriple{
		Id:         t.ID,
		Subject:    termToProto(t.Subject),
		Predicate:  termToProto(t.Predicate),
		Object:     termToProto(t.Object),
		Inferred:   t.Inferred,
		Rule:       t.Rule,
		SupportIds: t.SupportIDs,
	}
	if t.Graph != nil {
		out.Graph = termToProto(*t.Graph)
	}
	return out
}

func triplesToProto(in []graph.RDFTriple) []*rpcv1.RdfTriple {
	out := make([]*rpcv1.RdfTriple, 0, len(in))
	for _, t := range in {
		out = append(out, tripleToProto(t))
	}
	return out
}

func patternFromProto(p *rpcv1.TriplePattern) graph.TriplePattern {
	if p == nil {
		return graph.TriplePattern{}
	}
	return graph.TriplePattern{
		Subject:   termPtrFromProto(p.GetSubject()),
		Predicate: termPtrFromProto(p.GetPredicate()),
		Object:    termPtrFromProto(p.GetObject()),
		Graph:     termPtrFromProto(p.GetGraph()),
		Inferred:  p.Inferred,
		Limit:     int(p.GetLimit()),
	}
}

func patternPtrFromProto(p *rpcv1.TriplePattern) *graph.TriplePattern {
	if p == nil {
		return nil
	}
	v := patternFromProto(p)
	return &v
}

func explanationToProto(e graph.RDFSInferenceExplanation) *rpcv1.InferenceExplanation {
	return &rpcv1.InferenceExplanation{
		Triple:           tripleToProto(e.Triple),
		Explicit:         e.Explicit,
		Rule:             e.Rule,
		SupportTripleIds: e.SupportTripleIDs,
	}
}

func traceToProto(in []graph.RDFSInferenceTraceEntry) []*rpcv1.InferenceTraceEntry {
	out := make([]*rpcv1.InferenceTraceEntry, 0, len(in))
	for _, e := range in {
		out = append(out, &rpcv1.InferenceTraceEntry{
			TripleId:       e.TripleID,
			ParentTripleId: e.ParentTripleID,
			Depth:          int32(e.Depth),
			Explanation:    explanationToProto(e.Explanation),
			Truncated:      e.Truncated,
		})
	}
	return out
}

func ontologySchemaToProto(s cortexdb.OntologySchema) *rpcv1.OntologySchema {
	entityTypes := make([]*rpcv1.OntologyEntityType, 0, len(s.EntityTypes))
	for _, e := range s.EntityTypes {
		entityTypes = append(entityTypes, &rpcv1.OntologyEntityType{
			Name: e.Name, Description: e.Description, RequiredProperties: e.RequiredProperties,
		})
	}
	relationTypes := make([]*rpcv1.OntologyRelationType, 0, len(s.RelationTypes))
	for _, r := range s.RelationTypes {
		relationTypes = append(relationTypes, &rpcv1.OntologyRelationType{
			Name: r.Name, Description: r.Description,
			AllowedFromTypes: r.AllowedFromTypes, AllowedToTypes: r.AllowedToTypes,
			RequiredProperties: r.RequiredProperties,
		})
	}
	return &rpcv1.OntologySchema{
		SchemaId: s.SchemaID, Name: s.Name, Description: s.Description,
		Version: int32(s.Version), Active: s.Active, Metadata: s.Metadata,
		EntityTypes: entityTypes, RelationTypes: relationTypes,
		CreatedAt: tsFromTime(s.CreatedAt), UpdatedAt: tsFromTime(s.UpdatedAt),
	}
}

func ontologyEntityTypesFromProto(in []*rpcv1.OntologyEntityType) []cortexdb.OntologyEntityType {
	out := make([]cortexdb.OntologyEntityType, 0, len(in))
	for _, e := range in {
		out = append(out, cortexdb.OntologyEntityType{
			Name: e.GetName(), Description: e.GetDescription(), RequiredProperties: e.GetRequiredProperties(),
		})
	}
	return out
}

func ontologyRelationTypesFromProto(in []*rpcv1.OntologyRelationType) []cortexdb.OntologyRelationType {
	out := make([]cortexdb.OntologyRelationType, 0, len(in))
	for _, r := range in {
		out = append(out, cortexdb.OntologyRelationType{
			Name: r.GetName(), Description: r.GetDescription(),
			AllowedFromTypes: r.GetAllowedFromTypes(), AllowedToTypes: r.GetAllowedToTypes(),
			RequiredProperties: r.GetRequiredProperties(),
		})
	}
	return out
}
