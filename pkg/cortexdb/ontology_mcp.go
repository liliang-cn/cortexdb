package cortexdb

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addOntologyMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, toolbox *GraphRAGToolbox) {
	addOntologyMCPTool(server, definitions["ontology_save"], func(ctx context.Context, req OntologySaveRequest) (OntologySaveResponse, error) {
		resp, err := toolbox.SaveOntologySchema(ctx, req)
		if err != nil {
			return OntologySaveResponse{}, err
		}
		if resp == nil {
			return OntologySaveResponse{}, nil
		}
		return *resp, nil
	})
	addOntologyMCPTool(server, definitions["ontology_get"], func(ctx context.Context, req OntologyGetRequest) (OntologyGetResponse, error) {
		resp, err := toolbox.GetOntologySchema(ctx, req)
		if err != nil {
			return OntologyGetResponse{}, err
		}
		if resp == nil {
			return OntologyGetResponse{}, nil
		}
		return *resp, nil
	})
	addOntologyMCPTool(server, definitions["ontology_list"], func(ctx context.Context, req OntologyListRequest) (OntologyListResponse, error) {
		resp, err := toolbox.ListOntologySchemas(ctx, req)
		if err != nil {
			return OntologyListResponse{}, err
		}
		if resp == nil {
			return OntologyListResponse{}, nil
		}
		return *resp, nil
	})
	// Through addOntologyMCPTool, not addGraphRAGMCPTool: ObjectSet refers to
	// itself through Source and Operands, and the SDK's schema inference
	// panics on a recursive type rather than returning an error.
	addOntologyMCPTool(server, definitions["object_set_resolve"], func(ctx context.Context, req ObjectSetResolveRequest) (ObjectSetResolveResponse, error) {
		resp, err := toolbox.ResolveObjectSet(ctx, req)
		if err != nil {
			return ObjectSetResolveResponse{}, err
		}
		if resp == nil {
			return ObjectSetResolveResponse{}, nil
		}
		return *resp, nil
	})
	addOntologyMCPTool(server, definitions["ontology_delete"], func(ctx context.Context, req OntologyDeleteRequest) (OntologyDeleteResponse, error) {
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

// addOntologyMCPTool registers an ontology tool using the schemas declared in
// ontology_tooldefs.go instead of the ones the MCP SDK infers from Go types.
// Inference cannot work here: OntologyDataType is recursive — an array nests
// its item type and a struct nests fields, each of which is a property with a
// data type — and the SDK's reflection reports that as a cycle and panics.
// Every ontology response embeds OntologySchema, so the output schema needs the
// same treatment; it is declared permissively rather than enumerated, because a
// faithful JSON Schema for a recursive type needs the $ref/$defs that
// ToolDefinition does not model.
func addOntologyMCPTool[In, Out any](server *mcp.Server, definition ToolDefinition, handler func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:         definition.Name,
		Description:  definition.Description,
		InputSchema:  definition.InputSchema,
		OutputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		output, err := handler(ctx, input)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, output, nil
	})
}
