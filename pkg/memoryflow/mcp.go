package memoryflow

import (
	"context"
	"log/slog"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultMCPServerName  = "cortexdb-memoryflow"
	defaultMCPServerTitle = "CortexDB Memoryflow"
)

// MCPServerOptions configures the memoryflow MCP server wrapper.
type MCPServerOptions struct {
	Implementation *mcp.Implementation
	Instructions   string
	Logger         *slog.Logger
}

// NewMCPServer returns an MCP server that exposes the memoryflow tool surface.
func (s *Service) NewMCPServer(opts MCPServerOptions) (*mcp.Server, error) {
	toolbox, err := NewToolbox(s)
	if err != nil {
		return nil, err
	}

	impl := opts.Implementation
	if impl == nil {
		impl = &mcp.Implementation{
			Name:    defaultMCPServerName,
			Title:   defaultMCPServerTitle,
			Version: cortexdbroot.Version,
		}
	}

	server := mcp.NewServer(impl, &mcp.ServerOptions{
		Instructions: opts.Instructions,
		Logger:       opts.Logger,
	})

	definitions := make(map[string]cortexdb.ToolDefinition, len(toolbox.Definitions()))
	for _, definition := range toolbox.Definitions() {
		definitions[definition.Name] = definition
	}

	addMemoryflowMCPTool(server, definitions["memoryflow_ingest_transcript"], func(ctx context.Context, req IngestTranscriptRequest) (IngestTranscriptResponse, error) {
		resp, err := s.IngestTranscript(ctx, req)
		if err != nil {
			return IngestTranscriptResponse{}, err
		}
		if resp == nil {
			return IngestTranscriptResponse{}, nil
		}
		return *resp, nil
	})
	addMemoryflowMCPTool(server, definitions["memoryflow_recall"], func(ctx context.Context, req RecallRequest) (RecallResponse, error) {
		resp, err := s.Recall(ctx, req)
		if err != nil {
			return RecallResponse{}, err
		}
		if resp == nil {
			return RecallResponse{}, nil
		}
		return *resp, nil
	})
	addMemoryflowMCPTool(server, definitions["memoryflow_wake_up_layers"], func(ctx context.Context, req WakeUpLayersRequest) (WakeUpLayersResponse, error) {
		resp, err := s.WakeUpLayers(ctx, req)
		if err != nil {
			return WakeUpLayersResponse{}, err
		}
		if resp == nil {
			return WakeUpLayersResponse{}, nil
		}
		return *resp, nil
	})
	addMemoryflowMCPTool(server, definitions["memoryflow_close_session"], func(ctx context.Context, req CloseSessionRequest) (CloseSessionResponse, error) {
		resp, err := s.CloseSession(ctx, req)
		if err != nil {
			return CloseSessionResponse{}, err
		}
		if resp == nil {
			return CloseSessionResponse{}, nil
		}
		return *resp, nil
	})
	addMemoryflowMCPTool(server, definitions["memoryflow_append_diary"], func(ctx context.Context, req DiaryEntryRequest) (DiaryEntryResponse, error) {
		resp, err := s.AppendDiaryEntry(ctx, req)
		if err != nil {
			return DiaryEntryResponse{}, err
		}
		if resp == nil {
			return DiaryEntryResponse{}, nil
		}
		return *resp, nil
	})
	addMemoryflowMCPTool(server, definitions["memoryflow_list_diary"], func(ctx context.Context, req DiaryListRequest) (DiaryListResponse, error) {
		resp, err := s.ListDiaryEntries(ctx, req)
		if err != nil {
			return DiaryListResponse{}, err
		}
		if resp == nil {
			return DiaryListResponse{}, nil
		}
		return *resp, nil
	})
	addMemoryflowMCPTool(server, definitions["memoryflow_prepare_reply"], func(ctx context.Context, req ReplyProtocolRequest) (ReplyProtocolResponse, error) {
		resp, err := s.PrepareReply(ctx, req)
		if err != nil {
			return ReplyProtocolResponse{}, err
		}
		if resp == nil {
			return ReplyProtocolResponse{}, nil
		}
		return *resp, nil
	})

	return server, nil
}

func addMemoryflowMCPTool[In, Out any](server *mcp.Server, definition cortexdb.ToolDefinition, handler func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        definition.Name,
		Description: definition.Description,
		InputSchema: definition.InputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		output, err := handler(ctx, input)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return &mcp.CallToolResult{}, output, nil
	})
}
