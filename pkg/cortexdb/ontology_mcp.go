package cortexdb

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addOntologyMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, toolbox *GraphRAGToolbox) {
	addGraphRAGMCPTool(server, definitions["ontology_save"], func(ctx context.Context, req OntologySaveRequest) (OntologySaveResponse, error) {
		resp, err := toolbox.SaveOntologySchema(ctx, req)
		if err != nil {
			return OntologySaveResponse{}, err
		}
		if resp == nil {
			return OntologySaveResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["ontology_get"], func(ctx context.Context, req OntologyGetRequest) (OntologyGetResponse, error) {
		resp, err := toolbox.GetOntologySchema(ctx, req)
		if err != nil {
			return OntologyGetResponse{}, err
		}
		if resp == nil {
			return OntologyGetResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["ontology_list"], func(ctx context.Context, req OntologyListRequest) (OntologyListResponse, error) {
		resp, err := toolbox.ListOntologySchemas(ctx, req)
		if err != nil {
			return OntologyListResponse{}, err
		}
		if resp == nil {
			return OntologyListResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["ontology_delete"], func(ctx context.Context, req OntologyDeleteRequest) (OntologyDeleteResponse, error) {
		resp, err := toolbox.DeleteOntologySchema(ctx, req)
		if err != nil {
			return OntologyDeleteResponse{}, err
		}
		if resp == nil {
			return OntologyDeleteResponse{}, nil
		}
		return *resp, nil
	})
}
