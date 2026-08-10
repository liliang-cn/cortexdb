package rpcserver

import (
	"encoding/json"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

// ontologySchemaToProto carries the v2 schema in schema_json. The deprecated
// entity_types / relation_types fields are left empty: they cannot represent
// typed properties or a primary key, so populating them would be misleading.
func ontologySchemaToProto(s cortexdb.OntologySchema) *rpcv1.OntologySchema {
	encoded, err := json.Marshal(s)
	if err != nil {
		// Marshalling a plain struct of strings, slices and maps cannot fail;
		// an empty schema_json is still better than dropping the response.
		encoded = nil
	}
	return &rpcv1.OntologySchema{
		SchemaId: s.SchemaID, Name: s.Name, Description: s.Description,
		Version: int32(s.Version), Active: s.Active, Metadata: s.Metadata,
		SchemaJson: string(encoded),
		CreatedAt:  tsFromTime(s.CreatedAt), UpdatedAt: tsFromTime(s.UpdatedAt),
	}
}

// ontologySchemaFromProto decodes the v2 schema out of schema_json. A caller
// that still sends only the deprecated repeated fields is on the pre-v2 API and
// is told so explicitly rather than having its request silently ignored.
func ontologySchemaFromProto(req *rpcv1.SaveOntologySchemaRequest) (cortexdb.OntologySchema, error) {
	raw := strings.TrimSpace(req.GetSchemaJson())
	if raw == "" {
		if len(req.GetEntityTypes()) > 0 || len(req.GetRelationTypes()) > 0 {
			return cortexdb.OntologySchema{}, status.Error(codes.InvalidArgument,
				"this server uses the v2 ontology and needs schema_json; entity_types and relation_types are from the pre-v2 ontology API and are no longer accepted")
		}
		return cortexdb.OntologySchema{}, status.Error(codes.InvalidArgument, "schema_json is required")
	}

	var schema cortexdb.OntologySchema
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		return cortexdb.OntologySchema{}, status.Errorf(codes.InvalidArgument, "decode schema_json: %v", err)
	}

	// The scalar request fields stay authoritative when set, so a caller can
	// send a bare schema_json body and still steer id, name, version.
	if id := strings.TrimSpace(req.GetSchemaId()); id != "" {
		schema.SchemaID = id
	}
	if name := strings.TrimSpace(req.GetName()); name != "" {
		schema.Name = name
	}
	if description := strings.TrimSpace(req.GetDescription()); description != "" {
		schema.Description = description
	}
	// version is deliberately not carried over: the store derives it, so
	// setting it here would be dead weight that reads like a supported knob.
	if len(req.GetMetadata()) > 0 {
		schema.Metadata = req.GetMetadata()
	}
	return schema, nil
}
