package cortexdb

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addInferenceMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, toolbox *GraphRAGToolbox) {
	addGraphRAGMCPTool(server, definitions["apply_inference"], func(ctx context.Context, req ApplyInferenceRequest) (ApplyInferenceResponse, error) {
		resp, err := toolbox.ApplyInferenceRules(ctx, req)
		if err != nil {
			return ApplyInferenceResponse{}, err
		}
		if resp == nil {
			return ApplyInferenceResponse{}, nil
		}
		return *resp, nil
	})
}
