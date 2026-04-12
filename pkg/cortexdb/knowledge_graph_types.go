package cortexdb

import "github.com/liliang-cn/cortexdb/v2/pkg/graph"

// KnowledgeGraphNamespace aliases the low-level graph namespace type for the high-level DB API.
type KnowledgeGraphNamespace = graph.Namespace

// KnowledgeGraphTerm aliases the low-level RDF term type for the high-level DB API.
type KnowledgeGraphTerm = graph.RDFTerm

// KnowledgeGraphTriple aliases the low-level RDF triple type for the high-level DB API.
type KnowledgeGraphTriple = graph.RDFTriple

// KnowledgeGraphTriplePattern aliases the low-level triple pattern type for the high-level DB API.
type KnowledgeGraphTriplePattern = graph.TriplePattern

// KnowledgeGraphQueryResult aliases the low-level SPARQL result type for the high-level DB API.
type KnowledgeGraphQueryResult = graph.SPARQLResult

// KnowledgeGraphInferenceRefreshResult aliases the low-level RDFS inference refresh result type.
type KnowledgeGraphInferenceRefreshResult = graph.RDFSInferenceRefreshResult

// KnowledgeGraphInferenceExplanation aliases the low-level inference explanation type.
type KnowledgeGraphInferenceExplanation = graph.RDFSInferenceExplanation

// KnowledgeGraphInferenceTraceEntry aliases the low-level flattened inference trace entry type.
type KnowledgeGraphInferenceTraceEntry = graph.RDFSInferenceTraceEntry

// KnowledgeGraphInferenceSummary aliases the low-level inference summary type.
type KnowledgeGraphInferenceSummary = graph.RDFSInferenceSummary

// KnowledgeGraphInferenceMatchExplanation aliases the low-level match explanation type.
type KnowledgeGraphInferenceMatchExplanation = graph.RDFSInferenceMatchExplanation

// KnowledgeGraphSHACLReport aliases the low-level SHACL validation report type.
type KnowledgeGraphSHACLReport = graph.SHACLReport

const (
	// KnowledgeGraphFormatNTriples exports triples in N-Triples format.
	KnowledgeGraphFormatNTriples = string(graph.RDFFormatNTriples)
	// KnowledgeGraphFormatNQuads exports triples/quads in N-Quads format.
	KnowledgeGraphFormatNQuads = string(graph.RDFFormatNQuads)
	// KnowledgeGraphFormatTurtle exports default-graph triples in Turtle format.
	KnowledgeGraphFormatTurtle = string(graph.RDFFormatTurtle)
	// KnowledgeGraphFormatTriG exports quads in TriG format.
	KnowledgeGraphFormatTriG = string(graph.RDFFormatTriG)

	// KnowledgeGraphInferenceRefreshModeFull forces a full inferred-triple rebuild.
	KnowledgeGraphInferenceRefreshModeFull = "full"
	// KnowledgeGraphInferenceRefreshModeIncremental recomputes only the neighborhood
	// affected by the supplied triples, IDs, or pattern.
	KnowledgeGraphInferenceRefreshModeIncremental = "incremental"
)

// KnowledgeGraphNamespaceUpsertRequest stores one namespace mapping.
type KnowledgeGraphNamespaceUpsertRequest struct {
	Prefix string `json:"prefix"`
	URI    string `json:"uri"`
}

// KnowledgeGraphNamespaceUpsertResponse returns the stored namespace mapping.
type KnowledgeGraphNamespaceUpsertResponse struct {
	Namespace KnowledgeGraphNamespace `json:"namespace"`
}

// KnowledgeGraphNamespaceListResponse returns all namespaces visible to the graph layer.
type KnowledgeGraphNamespaceListResponse struct {
	Namespaces []KnowledgeGraphNamespace `json:"namespaces"`
}

// KnowledgeGraphUpsertRequest writes triples/quads into the knowledge graph.
type KnowledgeGraphUpsertRequest struct {
	Triples []KnowledgeGraphTriple `json:"triples"`
}

// KnowledgeGraphUpsertResponse summarizes a triple write operation.
type KnowledgeGraphUpsertResponse struct {
	TripleIDs []string `json:"triple_ids"`
	Count     int      `json:"count"`
}

// KnowledgeGraphFindRequest queries triples/quads by pattern.
type KnowledgeGraphFindRequest struct {
	Pattern KnowledgeGraphTriplePattern `json:"pattern"`
}

// KnowledgeGraphFindResponse returns matched triples/quads.
type KnowledgeGraphFindResponse struct {
	Triples []KnowledgeGraphTriple `json:"triples"`
}

// KnowledgeGraphDeleteRequest removes triples/quads by IDs, explicit triples, or a pattern.
type KnowledgeGraphDeleteRequest struct {
	TripleIDs []string                     `json:"triple_ids,omitempty"`
	Triples   []KnowledgeGraphTriple       `json:"triples,omitempty"`
	Pattern   *KnowledgeGraphTriplePattern `json:"pattern,omitempty"`
}

// KnowledgeGraphDeleteResponse summarizes deletion results.
type KnowledgeGraphDeleteResponse struct {
	Deleted int `json:"deleted"`
}

// KnowledgeGraphImportRequest loads RDF content into the graph.
type KnowledgeGraphImportRequest struct {
	Format  string `json:"format"`
	Content string `json:"content"`
}

// KnowledgeGraphImportResponse summarizes import results.
type KnowledgeGraphImportResponse struct {
	Format string `json:"format"`
	Count  int    `json:"count"`
}

// KnowledgeGraphExportRequest serializes RDF content out of the graph.
type KnowledgeGraphExportRequest struct {
	Format string `json:"format"`
}

// KnowledgeGraphExportResponse returns serialized RDF content.
type KnowledgeGraphExportResponse struct {
	Format  string `json:"format"`
	Content string `json:"content"`
}

// KnowledgeGraphQueryRequest executes a SPARQL SELECT/ASK subset against the embedded knowledge graph.
type KnowledgeGraphQueryRequest struct {
	Query string `json:"query"`
}

// KnowledgeGraphQueryResponse returns the SPARQL execution result.
type KnowledgeGraphQueryResponse struct {
	Result KnowledgeGraphQueryResult `json:"result"`
}

// KnowledgeGraphSHACLValidateRequest validates graph data with supplied SHACL-lite shape triples.
type KnowledgeGraphSHACLValidateRequest struct {
	Shapes []KnowledgeGraphTriple `json:"shapes"`
}

// KnowledgeGraphSHACLValidateResponse returns the SHACL-lite validation report.
type KnowledgeGraphSHACLValidateResponse struct {
	Report KnowledgeGraphSHACLReport `json:"report"`
}

// KnowledgeGraphInferenceRefreshRequest recomputes inferred triples.
type KnowledgeGraphInferenceRefreshRequest struct {
	Mode      string                       `json:"mode,omitempty"`
	TripleIDs []string                     `json:"triple_ids,omitempty"`
	Triples   []KnowledgeGraphTriple       `json:"triples,omitempty"`
	Pattern   *KnowledgeGraphTriplePattern `json:"pattern,omitempty"`
}

// KnowledgeGraphInferenceRefreshResponse summarizes an inference refresh run.
type KnowledgeGraphInferenceRefreshResponse struct {
	Result KnowledgeGraphInferenceRefreshResult `json:"result"`
}

// KnowledgeGraphInferenceSummaryRequest fetches inference summary counts.
type KnowledgeGraphInferenceSummaryRequest struct{}

// KnowledgeGraphInferenceSummaryResponse returns persisted inference counts and rule breakdowns.
type KnowledgeGraphInferenceSummaryResponse struct {
	Result KnowledgeGraphInferenceSummary `json:"result"`
}

// KnowledgeGraphInferenceExplainRequest fetches provenance for a triple.
type KnowledgeGraphInferenceExplainRequest struct {
	TripleID string `json:"triple_id"`
	Depth    int    `json:"depth,omitempty"`
}

// KnowledgeGraphInferenceExplainResponse returns inference provenance for a triple.
type KnowledgeGraphInferenceExplainResponse struct {
	Explanation KnowledgeGraphInferenceExplanation  `json:"explanation"`
	Trace       []KnowledgeGraphInferenceTraceEntry `json:"trace,omitempty"`
}

// KnowledgeGraphInferenceExplainMatchRequest explains all triples matched by a pattern.
type KnowledgeGraphInferenceExplainMatchRequest struct {
	Pattern KnowledgeGraphTriplePattern `json:"pattern"`
	Depth   int                         `json:"depth,omitempty"`
}

// KnowledgeGraphInferenceExplainMatchResponse returns matched explanations.
type KnowledgeGraphInferenceExplainMatchResponse struct {
	Matches []KnowledgeGraphInferenceMatchExplanation `json:"matches"`
}
