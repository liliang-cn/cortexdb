package graphflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The global-search tools: the whole-corpus question as something an agent
// calls, not something a query is routed into.
//
// GlobalSearch existed as a Go function and a one-shot CLI mode and was
// reachable from no tool surface, so an agent connected over MCP could not ask
// "what is this brain about" at all. These two tools expose it. Choosing them
// is the caller's decision: nothing inspects a query's wording to send it
// here, because deciding that a question is "global" is understanding it, and
// a phrase list that tried would serve only the languages someone enumerated.

const (
	globalSearchToolName   = "global_search"
	buildHierarchyToolName = "build_community_hierarchy"
)

// GlobalSearchToolRequest is the global_search tool input.
type GlobalSearchToolRequest struct {
	Query     string `json:"query"`
	Level     *int   `json:"level,omitempty"`
	MaxPoints int    `json:"max_points,omitempty"`
	// BuildIfEmpty defaults to true for the tool: a first call on a brain
	// with no reports builds them rather than failing.
	BuildIfEmpty *bool `json:"build_if_empty,omitempty"`
}

// BuildHierarchyToolRequest is the build_community_hierarchy tool input.
type BuildHierarchyToolRequest struct {
	MinSize    int     `json:"min_size,omitempty"`
	MaxLevels  int     `json:"max_levels,omitempty"`
	Resolution float64 `json:"resolution,omitempty"`
}

// BuildHierarchyToolResponse summarises a build without every report body.
type BuildHierarchyToolResponse struct {
	EntityCount    int                  `json:"entity_count"`
	EdgeCount      int                  `json:"edge_count"`
	Levels         []HierarchyLevelInfo `json:"levels"`
	ReportsWritten int                  `json:"reports_written"`
	ModelCalls     int                  `json:"model_calls"`
	Mode           string               `json:"mode"`
}

// GlobalSearchToolDefinitions returns the definitions of global_search and
// build_community_hierarchy.
func GlobalSearchToolDefinitions() []cortexdb.ToolDefinition {
	return []cortexdb.ToolDefinition{
		{
			Name: globalSearchToolName,
			Description: "GraphRAG global search: answer a question about the WHOLE knowledge graph — its main themes, what areas it covers, what it says overall about a topic — by map-reducing over community reports rather than retrieving individual chunks. " +
				"Use it when the answer is spread across the corpus; use knowledge_memory_recall or search tools for a specific fact. " +
				"Reports come from build_community_hierarchy (built automatically on first use unless build_if_empty is false). " +
				"level picks the granularity: 0 is the finest, most numerous reports; higher levels merge them into broader themes; omitted, the coarsest level is used (fewest reports, cheapest). " +
				"With a model configured the answer is synthesised (mode \"model\"); without one the most relevant reports are ranked lexically and returned as supporting_points with no synthesis (mode \"no_model\"). " +
				"The response names the level used and levels_available.",
			InputSchema: gfObjectSchema(
				[]string{"query"},
				map[string]any{
					"query":          gfStringSchema("The whole-corpus question."),
					"level":          gfIntegerSchema("Hierarchy level to answer from: 0 = finest. Omit for the coarsest level."),
					"max_points":     gfIntegerSchema("Key points passed to the reduce step, or reports returned without a model. Default 12."),
					"build_if_empty": gfBooleanSchema("Build the community hierarchy first if none exists. Default true."),
				},
			),
		},
		{
			Name: buildHierarchyToolName,
			Description: "Detect the knowledge graph's community hierarchy (Louvain, every level kept, finest first) over entity nodes and write a report for every community bottom-up: level-0 reports from each community's entities and relations, higher levels from the reports of the communities they merged. Replaces any previous build. " +
				"Reports are written by the configured model, or assembled deterministically (most connected members, relations, child titles) when there is none. One model call per community above min_size, so on a large graph this is slow; call it after the graph changes substantially, not per question. Returns the level shapes (community counts, modularity, largest community), not the report bodies.",
			InputSchema: gfObjectSchema(
				nil,
				map[string]any{
					"min_size":   gfIntegerSchema("Skip communities with fewer entities. Default 3."),
					"max_levels": gfIntegerSchema("Keep at most this many levels. Default: all Louvain produces."),
					"resolution": gfNumberSchema("Louvain resolution. Default 1.0; higher gives smaller communities."),
				},
			),
		},
	}
}

func callGlobalSearch(ctx context.Context, db *cortexdb.DB, llm JSONGenerator, req GlobalSearchToolRequest) (*GlobalSearchResult, error) {
	build := true
	if req.BuildIfEmpty != nil {
		build = *req.BuildIfEmpty
	}
	return GlobalSearch(ctx, db, req.Query, GlobalSearchOptions{
		LLM:          llm,
		MaxPoints:    req.MaxPoints,
		Level:        req.Level,
		BuildIfEmpty: build,
	})
}

func callBuildHierarchy(ctx context.Context, db *cortexdb.DB, llm JSONGenerator, req BuildHierarchyToolRequest) (*BuildHierarchyToolResponse, error) {
	rep, err := BuildCommunityHierarchy(ctx, db, HierarchyOptions{
		LLM:        llm,
		MinSize:    req.MinSize,
		MaxLevels:  req.MaxLevels,
		Resolution: req.Resolution,
	})
	if err != nil {
		return nil, err
	}
	mode := "model"
	if llm == nil {
		mode = "no_model"
	}
	return &BuildHierarchyToolResponse{
		EntityCount:    rep.EntityCount,
		EdgeCount:      rep.EdgeCount,
		Levels:         rep.Levels,
		ReportsWritten: len(rep.Communities),
		ModelCalls:     rep.ModelCalls,
		Mode:           mode,
	}, nil
}

// callGlobalSearchTool dispatches one of the two tools from raw JSON; ok is
// false when name is neither.
func callGlobalSearchTool(ctx context.Context, db *cortexdb.DB, llm JSONGenerator, name string, input json.RawMessage) (any, bool, error) {
	switch name {
	case globalSearchToolName:
		var req GlobalSearchToolRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		out, err := callGlobalSearch(ctx, db, llm, req)
		return out, true, err
	case buildHierarchyToolName:
		var req BuildHierarchyToolRequest
		if len(input) > 0 {
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, true, fmt.Errorf("decode %s: %w", name, err)
			}
		}
		out, err := callBuildHierarchy(ctx, db, llm, req)
		return out, true, err
	}
	return nil, false, nil
}

// AddGlobalSearchMCPTools registers global_search and
// build_community_hierarchy on an MCP server. llm may be nil: both tools then
// run in their no-model mode. The cortexdb-mcp-stdio binary calls this beside
// the facade's tools; NewMCPServer calls it for the graphflow surface.
func AddGlobalSearchMCPTools(server *mcp.Server, db *cortexdb.DB, llm JSONGenerator) {
	defs := make(map[string]cortexdb.ToolDefinition)
	for _, d := range GlobalSearchToolDefinitions() {
		defs[d.Name] = d
	}
	addGraphflowMCPTool(server, defs[globalSearchToolName], func(ctx context.Context, req GlobalSearchToolRequest) (*GlobalSearchResult, error) {
		return callGlobalSearch(ctx, db, llm, req)
	})
	addGraphflowMCPTool(server, defs[buildHierarchyToolName], func(ctx context.Context, req BuildHierarchyToolRequest) (*BuildHierarchyToolResponse, error) {
		return callBuildHierarchy(ctx, db, llm, req)
	})
}
