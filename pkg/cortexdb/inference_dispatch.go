package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
)

func (t *GraphRAGToolbox) callInferenceTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	switch name {
	case "apply_inference":
		var req ApplyInferenceRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.ApplyInferenceRules(ctx, req)
		return resp, true, err
	default:
		return nil, false, nil
	}
}
