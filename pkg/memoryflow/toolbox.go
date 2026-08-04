package memoryflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Toolbox exposes memoryflow as a tool-call surface.
type Toolbox struct {
	service *Service
}

// NewToolbox constructs a tool facade for a workflow service.
func NewToolbox(service *Service) (*Toolbox, error) {
	if service == nil {
		return nil, fmt.Errorf("service is required")
	}
	return &Toolbox{service: service}, nil
}

// Definitions returns JSON-schema-like tool definitions.
func (t *Toolbox) Definitions() []cortexdb.ToolDefinition {
	return []cortexdb.ToolDefinition{
		{
			Name:        "memoryflow_resolve_taxonomy",
			Description: "Resolve deterministic memory taxonomy from project conventions and source hints.",
			InputSchema: mfObjectSchema(
				nil,
				map[string]any{
					"taxonomy": mfTaxonomySchema(),
					"hint":     mfSourceHintSchema(),
				},
			),
		},
		{
			Name:        "memoryflow_ingest_transcript",
			Description: "Ingest a transcript as episodic exchange memories.",
			InputSchema: mfObjectSchema(
				[]string{"transcript"},
				map[string]any{
					"transcript": mfTranscriptSchema(),
					"scope":      mfStringSchema("Optional memory scope."),
					"namespace":  mfStringSchema("Optional memory namespace."),
					"wing":       mfStringSchema("Optional taxonomy wing."),
					"room":       mfStringSchema("Optional taxonomy room."),
					"tags":       mfStringArraySchema("Optional tags."),
					"taxonomy":   mfTaxonomySchema(),
				},
			),
		},
		{
			Name:        "memoryflow_recall",
			Description: "Run workflow recall over episodic memory and durable knowledge.",
			InputSchema: mfObjectSchema(
				[]string{"query"},
				map[string]any{
					"query":              mfStringSchema("Recall query."),
					"user_id":            mfStringSchema("Optional user ID."),
					"session_id":         mfStringSchema("Optional session ID."),
					"scope":              mfStringSchema("Optional memory scope."),
					"namespace":          mfStringSchema("Optional memory namespace."),
					"collection":         mfStringSchema("Optional knowledge collection."),
					"top_k_memories":     mfIntegerSchema("Optional memory hit limit."),
					"top_k_knowledge":    mfIntegerSchema("Optional knowledge hit limit."),
					"retrieval_mode":     mfStringSchema("Optional retrieval mode."),
					"disable_memory":     mfBooleanSchema("Disable episodic memory recall."),
					"disable_knowledge":  mfBooleanSchema("Disable durable knowledge recall."),
					"disable_graph":      mfBooleanSchema("Disable graph expansion."),
					"graph_light":        mfBooleanSchema("Use lighter graph expansion."),
					"max_context_chars":  mfIntegerSchema("Optional context char budget."),
					"max_context_chunks": mfIntegerSchema("Optional context chunk budget."),
				},
			),
		},
		{
			Name:        "memoryflow_wake_up_layers",
			Description: "Build layered startup context blocks L0-L3.",
			InputSchema: mfObjectSchema(
				[]string{"recall"},
				map[string]any{
					"identity": mfStringSchema("Optional identity/system prompt."),
					"recall":   mfRecallSchema(),
				},
			),
		},
		{
			Name:        "memoryflow_close_session",
			Description: "Close a session and optionally promote extracted durable knowledge.",
			InputSchema: mfObjectSchema(
				[]string{"transcript"},
				map[string]any{
					"transcript": mfTranscriptSchema(),
					"scope":      mfStringSchema("Optional memory scope."),
					"namespace":  mfStringSchema("Optional memory namespace."),
					"promote":    mfBooleanSchema("Promote durable knowledge at close."),
					"collection": mfStringSchema("Optional target collection."),
					"author":     mfStringSchema("Optional author."),
				},
			),
		},
		{
			Name:        "memoryflow_list_episodes",
			Description: "List stored episodic transcript exchanges for a resolved session bucket.",
			InputSchema: mfObjectSchema(
				nil,
				map[string]any{
					"user_id":    mfStringSchema("Optional user ID."),
					"session_id": mfStringSchema("Optional session ID."),
					"scope":      mfStringSchema("Optional memory scope."),
					"namespace":  mfStringSchema("Optional memory namespace."),
					"limit":      mfIntegerSchema("Optional episode limit."),
				},
			),
		},
		{
			Name:        "memoryflow_get_transcript",
			Description: "Reconstruct the original transcript from stored episodic exchanges.",
			InputSchema: mfObjectSchema(
				nil,
				map[string]any{
					"user_id":    mfStringSchema("Optional user ID."),
					"session_id": mfStringSchema("Optional session ID."),
					"scope":      mfStringSchema("Optional memory scope."),
					"namespace":  mfStringSchema("Optional memory namespace."),
					"limit":      mfIntegerSchema("Optional episode limit."),
				},
			),
		},
		{
			Name:        "memoryflow_append_diary",
			Description: "Append a workflow diary entry.",
			InputSchema: mfObjectSchema(
				[]string{"content"},
				map[string]any{
					"entry_id":   mfStringSchema("Optional entry ID."),
					"user_id":    mfStringSchema("Optional user ID."),
					"session_id": mfStringSchema("Optional session ID."),
					"scope":      mfStringSchema("Optional memory scope."),
					"namespace":  mfStringSchema("Optional diary namespace."),
					"taxonomy":   mfTaxonomySchema(),
					"path_hint":  mfStringSchema("Optional path hint for conventions."),
					"content":    mfStringSchema("Diary content."),
					"importance": mfNumberSchema("Optional importance score."),
				},
			),
		},
		{
			Name:        "memoryflow_list_diary",
			Description: "List diary entries from a resolved diary bucket.",
			InputSchema: mfObjectSchema(
				nil,
				map[string]any{
					"user_id":    mfStringSchema("Optional user ID."),
					"session_id": mfStringSchema("Optional session ID."),
					"scope":      mfStringSchema("Optional memory scope."),
					"namespace":  mfStringSchema("Optional diary namespace."),
					"limit":      mfIntegerSchema("Optional maximum entries."),
				},
			),
		},
		{
			Name:        "memoryflow_prepare_reply",
			Description: "Prepare deterministic reply protocol guidance and wake-up layers.",
			InputSchema: mfObjectSchema(
				[]string{"recall"},
				map[string]any{
					"identity": mfStringSchema("Optional identity/system prompt."),
					"recall":   mfRecallSchema(),
				},
			),
		},
		{
			Name: "memoryflow_apply_memory_edits",
			Description: "Apply memory edits you have already decided on: add new memories, update wording, " +
				"or supersede a memory the conversation has just made untrue. Superseding keeps the old " +
				"memory and links it forward, so it is reversible; it must be enabled with allow_supersede " +
				"and is capped. Use dry_run first to see what a plan would do.",
			InputSchema: mfObjectSchema(
				[]string{"edits"},
				map[string]any{
					"edits": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":     "object",
							"required": []string{"op"},
							"properties": map[string]any{
								"op":        map[string]any{"type": "string", "enum": []string{"add", "update", "supersede"}},
								"memory_id": map[string]any{"type": "string", "description": "target of update/supersede"},
								"content":   map[string]any{"type": "string"},
								"reason":    map[string]any{"type": "string", "description": "why; stored on a superseded memory"},
							},
						},
					},
					"allow_supersede": map[string]any{"type": "boolean"},
					"max_supersedes":  map[string]any{"type": "integer"},
					"dry_run":         map[string]any{"type": "boolean"},
					"scope":           map[string]any{"type": "string"},
					"user_id":         map[string]any{"type": "string"},
					"namespace":       map[string]any{"type": "string"},
				},
			),
		},
	}
}

// Call dispatches a tool call into the workflow service.
func (t *Toolbox) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	switch name {
	case "memoryflow_apply_memory_edits":
		var req struct {
			Edits          []MemoryEdit `json:"edits"`
			AllowSupersede bool         `json:"allow_supersede"`
			MaxSupersedes  int          `json:"max_supersedes"`
			DryRun         bool         `json:"dry_run"`
			Scope          string       `json:"scope"`
			UserID         string       `json:"user_id"`
			Namespace      string       `json:"namespace"`
		}
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return ApplyMemoryEdits(ctx, t.service.db, MemoryEditPlan{Edits: req.Edits}, MemoryEditOptions{
			AllowSupersede: req.AllowSupersede,
			MaxSupersedes:  req.MaxSupersedes,
			DryRun:         req.DryRun,
			Scope:          req.Scope,
			UserID:         req.UserID,
			Namespace:      req.Namespace,
		})
	case "memoryflow_resolve_taxonomy":
		var req struct {
			Taxonomy Taxonomy   `json:"taxonomy"`
			Hint     SourceHint `json:"hint"`
		}
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.service.ResolveTaxonomy(req.Taxonomy, req.Hint), nil
	case "memoryflow_ingest_transcript":
		var req IngestTranscriptRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.service.IngestTranscript(ctx, req)
	case "memoryflow_recall":
		var req RecallRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.service.Recall(ctx, req)
	case "memoryflow_wake_up_layers":
		var req WakeUpLayersRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.service.WakeUpLayers(ctx, req)
	case "memoryflow_close_session":
		var req CloseSessionRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.service.CloseSession(ctx, req)
	case "memoryflow_list_episodes":
		var req ListEpisodesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.service.ListEpisodes(ctx, req)
	case "memoryflow_get_transcript":
		var req GetTranscriptRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.service.GetTranscript(ctx, req)
	case "memoryflow_append_diary":
		var req DiaryEntryRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.service.AppendDiaryEntry(ctx, req)
	case "memoryflow_list_diary":
		var req DiaryListRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.service.ListDiaryEntries(ctx, req)
	case "memoryflow_prepare_reply":
		var req ReplyProtocolRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.service.PrepareReply(ctx, req)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func mfObjectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	// Omit "required" when empty; a nil slice serializes as "required": null,
	// which strict function-calling validators reject.
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func mfStringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func mfIntegerSchema(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func mfNumberSchema(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}

func mfBooleanSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func mfStringArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

func mfTaxonomySchema() map[string]any {
	return mfObjectSchema(nil, map[string]any{
		"kind":     mfStringSchema("Optional taxonomy kind."),
		"wing":     mfStringSchema("Optional wing."),
		"room":     mfStringSchema("Optional room."),
		"source":   mfStringSchema("Optional source."),
		"tags":     mfStringArraySchema("Optional tags."),
		"entities": mfStringArraySchema("Optional entity names."),
	})
}

func mfSourceHintSchema() map[string]any {
	return mfObjectSchema(nil, map[string]any{
		"project": mfStringSchema("Optional project name."),
		"path":    mfStringSchema("Optional source path."),
		"source":  mfStringSchema("Optional source name."),
		"title":   mfStringSchema("Optional source title."),
		"tags":    mfStringArraySchema("Optional source tags."),
	})
}

func mfTranscriptSchema() map[string]any {
	return mfObjectSchema(
		[]string{"turns"},
		map[string]any{
			"session_id": mfStringSchema("Optional session ID."),
			"user_id":    mfStringSchema("Optional user ID."),
			"source":     mfStringSchema("Optional source label."),
			"title":      mfStringSchema("Optional title."),
			"turns": map[string]any{
				"type": "array",
				"items": mfObjectSchema(
					[]string{"role", "content"},
					map[string]any{
						"role":      mfStringSchema("Turn role."),
						"content":   mfStringSchema("Turn content."),
						"source_id": mfStringSchema("Optional source turn ID."),
					},
				),
			},
		},
	)
}

func mfRecallSchema() map[string]any {
	return mfObjectSchema(
		[]string{"query"},
		map[string]any{
			"query":              mfStringSchema("Recall query."),
			"user_id":            mfStringSchema("Optional user ID."),
			"session_id":         mfStringSchema("Optional session ID."),
			"scope":              mfStringSchema("Optional memory scope."),
			"namespace":          mfStringSchema("Optional namespace."),
			"collection":         mfStringSchema("Optional knowledge collection."),
			"top_k_memories":     mfIntegerSchema("Optional memory limit."),
			"top_k_knowledge":    mfIntegerSchema("Optional knowledge limit."),
			"retrieval_mode":     mfStringSchema("Optional retrieval mode."),
			"disable_memory":     mfBooleanSchema("Disable memory recall."),
			"disable_knowledge":  mfBooleanSchema("Disable knowledge recall."),
			"disable_graph":      mfBooleanSchema("Disable graph recall."),
			"graph_light":        mfBooleanSchema("Use lighter graph recall."),
			"max_context_chars":  mfIntegerSchema("Optional context char budget."),
			"max_context_chunks": mfIntegerSchema("Optional context chunk budget."),
		},
	)
}
