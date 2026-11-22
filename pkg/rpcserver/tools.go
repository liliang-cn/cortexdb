package rpcserver

import (
	"context"
	"encoding/json"
	"google.golang.org/protobuf/proto"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type toolsService struct {
	rpcv1.UnimplementedToolsServiceServer
	db *cortexdb.DB
	// extras are the tools served beside the toolbox (Options.ExtraTools).
	extras *extraTools
}

func (s *toolsService) ListTools(context.Context, *rpcv1.ListToolsRequest) (*rpcv1.ListToolsResponse, error) {
	defs := append(s.db.GraphRAGTools().Definitions(), s.extras.definitions()...)
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
			Mutates:         proto.Bool(d.Mutates),
			Idempotent:      d.Idempotent,
			Destructive:     d.Destructive,
		})
	}
	return &rpcv1.ListToolsResponse{Tools: tools}, nil
}

func (s *toolsService) CallTool(ctx context.Context, req *rpcv1.CallToolRequest) (*rpcv1.CallToolResponse, error) {
	args := req.GetArgsJson()
	if args == "" {
		args = "{}"
	}
	if tool, ok := s.extras.get(req.GetName()); ok {
		return s.callExtra(ctx, tool, args)
	}
	if err := guardToolRows(ctx, s.db, req.GetName(), args); err != nil {
		return nil, err
	}
	result, err := s.db.GraphRAGTools().Call(ctx, req.GetName(), json.RawMessage(args))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unknown tool") {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, oneNotFound(ctx, toStatus(err))
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal result: %v", err)
	}
	return &rpcv1.CallToolResponse{ResultJson: string(payload)}, nil
}

// callExtra runs a tool served beside the toolbox. It has no row guard: the
// interceptor refuses every extra tool to a confined key, and an interceptor
// that was not given the extras refuses them to every key.
func (s *toolsService) callExtra(ctx context.Context, tool Tool, args string) (*rpcv1.CallToolResponse, error) {
	result, err := tool.Call(ctx, json.RawMessage(args))
	if err != nil {
		return nil, extraToolStatus(err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal result: %v", err)
	}
	return &rpcv1.CallToolResponse{ResultJson: string(payload)}, nil
}
