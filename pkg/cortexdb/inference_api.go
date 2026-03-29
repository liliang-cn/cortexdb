package cortexdb

import (
	"context"
	"fmt"
)

// ApplyInferenceRules materializes inferred relation edges from explicit relation edges.
func (db *DB) ApplyInferenceRules(ctx context.Context, req ApplyInferenceRequest) (*ApplyInferenceResponse, error) {
	if len(req.Rules) == 0 {
		return nil, fmt.Errorf("rules are required")
	}
	return db.applyInferenceRules(ctx, req)
}

// ApplyInferenceRules executes deterministic inference through the tool surface.
func (t *GraphRAGToolbox) ApplyInferenceRules(ctx context.Context, req ApplyInferenceRequest) (*ApplyInferenceResponse, error) {
	return t.db.ApplyInferenceRules(ctx, req)
}
