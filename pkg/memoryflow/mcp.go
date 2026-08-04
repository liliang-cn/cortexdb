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

	// Every tool in Definitions() must be registered here; the two lists are
	// hand-kept and a coverage test now fails when they drift apart.
	addMemoryflowMCPTool(server, definitions["memoryflow_apply_memory_edits"], func(ctx context.Context, req applyMemoryEditsInput) (MemoryEditReport, error) {
		rep, err := ApplyMemoryEdits(ctx, s.db, MemoryEditPlan{Edits: req.Edits}, MemoryEditOptions{
			AllowSupersede: req.AllowSupersede,
			MaxSupersedes:  req.MaxSupersedes,
			DryRun:         req.DryRun,
			Scope:          req.Scope,
			UserID:         req.UserID,
			Namespace:      req.Namespace,
		})
		if err != nil || rep == nil {
			return MemoryEditReport{}, err
		}
		return *rep, nil
	})
	addMemoryflowMCPTool(server, definitions["memoryflow_resolve_taxonomy"], func(_ context.Context, req resolveTaxonomyInput) (Taxonomy, error) {
		return s.ResolveTaxonomy(req.Taxonomy, req.Hint), nil
	})
	addMemoryflowMCPTool(server, definitions["memoryflow_list_episodes"], func(ctx context.Context, req ListEpisodesRequest) (ListEpisodesResponse, error) {
		resp, err := s.ListEpisodes(ctx, req)
		if err != nil || resp == nil {
			return ListEpisodesResponse{}, err
		}
		return *resp, nil
	})
	addMemoryflowMCPTool(server, definitions["memoryflow_get_transcript"], func(ctx context.Context, req GetTranscriptRequest) (GetTranscriptResponse, error) {
		resp, err := s.GetTranscript(ctx, req)
		if err != nil || resp == nil {
			return GetTranscriptResponse{}, err
		}
		return *resp, nil
	})
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

// applyMemoryEditsInput mirrors the tool's input schema.
type applyMemoryEditsInput struct {
	Edits          []MemoryEdit `json:"edits"`
	AllowSupersede bool         `json:"allow_supersede"`
	MaxSupersedes  int          `json:"max_supersedes"`
	DryRun         bool         `json:"dry_run"`
	Scope          string       `json:"scope"`
	UserID         string       `json:"user_id"`
	Namespace      string       `json:"namespace"`
}

// resolveTaxonomyInput mirrors memoryflow_resolve_taxonomy's input schema.
type resolveTaxonomyInput struct {
	Taxonomy Taxonomy   `json:"taxonomy"`
	Hint     SourceHint `json:"hint"`
}
