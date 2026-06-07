package rpcserver

import (
	"context"
	"encoding/json"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type toolsService struct {
	rpcv1.UnimplementedToolsServiceServer
	db *cortexdb.DB
}

func (s *toolsService) ListTools(context.Context, *rpcv1.ListToolsRequest) (*rpcv1.ListToolsResponse, error) {
	defs := s.db.GraphRAGTools().Definitions()
	tools := make([]*rpcv1.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		schema, err := json.Marshal(d.InputSchema)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "marshal schema for %s: %v", d.Name, err)
		}
		tools = append(tools, &rpcv1.ToolDefinition{
			Name:            d.Name,
			Description:     d.Description,
			InputSchemaJson: string(schema),
		})
	}
	return &rpcv1.ListToolsResponse{Tools: tools}, nil
}

func (s *toolsService) CallTool(ctx context.Context, req *rpcv1.CallToolRequest) (*rpcv1.CallToolResponse, error) {
	args := req.GetArgsJson()
	if args == "" {
		args = "{}"
	}
	result, err := s.db.GraphRAGTools().Call(ctx, req.GetName(), json.RawMessage(args))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unknown tool") {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, toStatus(err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal result: %v", err)
	}
	return &rpcv1.CallToolResponse{ResultJson: string(payload)}, nil
}
