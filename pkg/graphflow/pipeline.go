package graphflow

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Pipeline wires detector and extractor into the deterministic graphflow loop.
type Pipeline struct {
	Detector  Detector
	Extractor Extractor
}

// NewPipeline constructs a graphflow pipeline.
func NewPipeline(detector Detector, extractor Extractor) (*Pipeline, error) {
	if detector == nil {
		return nil, fmt.Errorf("detector is required")
	}
	if extractor == nil {
		return nil, fmt.Errorf("extractor is required")
	}
	return &Pipeline{Detector: detector, Extractor: extractor}, nil
}

// Run executes detect -> extract -> build -> analyze -> report -> export.
func (p *Pipeline) Run(ctx context.Context, db *cortexdb.DB, req RunRequest) (*RunResult, error) {
	if db == nil {
		return nil, fmt.Errorf("db is required")
	}
	docs, err := p.Detector.Detect(ctx, req.Root)
	if err != nil {
		return nil, err
	}
	extractions := make([]ExtractionResult, 0, len(docs))
	for _, doc := range docs {
		extraction, err := p.Extractor.Extract(ctx, doc)
		if err != nil {
			return nil, err
		}
		extractions = append(extractions, *extraction)
	}
	buildResp, err := Build(ctx, db, extractions, req.Build)
	if err != nil {
		return nil, err
	}
	analysis, err := Analyze(ctx, db, req.Analyze)
	if err != nil {
		return nil, err
	}
	report, err := RenderReport(ctx, analysis)
	if err != nil {
		return nil, err
	}
	var exportResp *ExportResult
	if req.OutputDir != "" {
		exportResp, err = Export(ctx, db, ExportRequest{
			OutputDir: req.OutputDir,
			Analysis:  analysis,
			Report:    report,
		})
		if err != nil {
			return nil, err
		}
	}
	return &RunResult{
		Documents:   docs,
		Extractions: extractions,
		Build:       *buildResp,
		Analysis:    analysis,
		Report:      report,
		Export:      exportResp,
	}, nil
}
