package graphflow

import (
	"context"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Confidence labels whether an edge was directly found or only inferred by an upstream extractor.
type Confidence string

const (
	ConfidenceExtracted Confidence = "EXTRACTED"
	ConfidenceInferred  Confidence = "INFERRED"
	ConfidenceAmbiguous Confidence = "AMBIGUOUS"
)

// SourceDocument is one input document identified by a detector stage.
type SourceDocument struct {
	ID       string            `json:"id"`
	Path     string            `json:"path,omitempty"`
	Type     string            `json:"type,omitempty"`
	Title    string            `json:"title,omitempty"`
	Content  string            `json:"content,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ExtractionNode is one extracted graph node before persistence.
type ExtractionNode struct {
	ID             string            `json:"id"`
	Label          string            `json:"label"`
	Type           string            `json:"type,omitempty"`
	Summary        string            `json:"summary,omitempty"`
	SourceFile     string            `json:"source_file,omitempty"`
	SourceLocation string            `json:"source_location,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// ExtractionEdge is one extracted graph edge before persistence.
type ExtractionEdge struct {
	Source         string            `json:"source"`
	Target         string            `json:"target"`
	Relation       string            `json:"relation"`
	Confidence     Confidence        `json:"confidence"`
	Directed       bool              `json:"directed,omitempty"`
	SourceFile     string            `json:"source_file,omitempty"`
	SourceLocation string            `json:"source_location,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// ExtractionResult is the canonical schema that all extractors must emit.
type ExtractionResult struct {
	SourceID   string            `json:"source_id"`
	SourceType string            `json:"source_type,omitempty"`
	Title      string            `json:"title,omitempty"`
	Nodes      []ExtractionNode  `json:"nodes"`
	Edges      []ExtractionEdge  `json:"edges"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// Detector enumerates input documents.
type Detector interface {
	Detect(ctx context.Context, root string) ([]SourceDocument, error)
}

// Extractor converts one input document into the canonical extraction schema.
type Extractor interface {
	Extract(ctx context.Context, doc SourceDocument) (*ExtractionResult, error)
}

// Analyzer derives deterministic graph summaries from a persisted graphflow subgraph.
type Analyzer interface {
	Analyze(ctx context.Context, db *cortexdb.DB, req AnalyzeRequest) (*AnalysisReport, error)
}

// Reporter renders an analysis report.
type Reporter interface {
	Render(ctx context.Context, report *AnalysisReport) (string, error)
}

// Exporter writes graphflow outputs to disk.
type Exporter interface {
	Export(ctx context.Context, db *cortexdb.DB, req ExportRequest) (*ExportResult, error)
}

// BuildOptions controls persistence behavior.
type BuildOptions struct {
	Collection   string `json:"collection,omitempty"`
	ReplaceEdges bool   `json:"replace_edges,omitempty"`
}

// BuildResult summarizes one build operation.
type BuildResult struct {
	NodeCount int `json:"node_count"`
	EdgeCount int `json:"edge_count"`
}

// AnalyzeRequest scopes deterministic graph analysis.
type AnalyzeRequest struct {
	TopN int `json:"top_n,omitempty"`
}

// TopNode is one ranked node from analysis.
type TopNode struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Type  string  `json:"type,omitempty"`
	Score float64 `json:"score"`
}

// AnalysisReport is the deterministic summary of a graphflow graph.
type AnalysisReport struct {
	GeneratedAt        time.Time      `json:"generated_at"`
	NodeCount          int            `json:"node_count"`
	EdgeCount          int            `json:"edge_count"`
	NodeTypes          map[string]int `json:"node_types,omitempty"`
	RelationTypes      map[string]int `json:"relation_types,omitempty"`
	Confidence         map[string]int `json:"confidence,omitempty"`
	TopNodes           []TopNode      `json:"top_nodes,omitempty"`
	SuggestedQuestions []string       `json:"suggested_questions,omitempty"`
}

// ExportRequest writes a graphflow bundle to disk.
type ExportRequest struct {
	OutputDir string          `json:"output_dir"`
	Analysis  *AnalysisReport `json:"analysis,omitempty"`
	Report    string          `json:"report,omitempty"`
}

// ExportResult returns the written file paths.
type ExportResult struct {
	OutputDir      string `json:"output_dir"`
	GraphJSON      string `json:"graph_json"`
	ReportMarkdown string `json:"report_markdown,omitempty"`
	GraphHTML      string `json:"graph_html,omitempty"`
}

// RunRequest is the end-to-end graphflow pipeline input.
type RunRequest struct {
	Root      string         `json:"root"`
	OutputDir string         `json:"output_dir"`
	Build     BuildOptions   `json:"build,omitempty"`
	Analyze   AnalyzeRequest `json:"analyze,omitempty"`
}

// RunResult summarizes a full graphflow pipeline execution.
type RunResult struct {
	Documents   []SourceDocument   `json:"documents,omitempty"`
	Extractions []ExtractionResult `json:"extractions,omitempty"`
	Build       BuildResult        `json:"build"`
	Analysis    *AnalysisReport    `json:"analysis,omitempty"`
	Report      string             `json:"report,omitempty"`
	Export      *ExportResult      `json:"export,omitempty"`
}
