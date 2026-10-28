package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The recall log's tools: read the timeline, one recall with its explain
// record, and which records recalls reach; mark a hit useful or noise; and
// export the marks as golden-set cases. Each answers "the recall log is off"
// on a brain that does not keep one, and a confined key sees and marks only
// its own recalls.

func recallLogToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name: "recall_log_list",
			Description: "List past recalls, newest first: the query, who asked (key, host, project), latency, whether it was degraded, " +
				"and its hits with how each was found (via), score, writer and source. Filter by record_id (recalls that returned it), " +
				"contains (query text), key, project, trace_id (the W3C trace a request's traceparent carried: what this brain did for that flow) or empty. Page with before = next_before. Needs CORTEXDB_RECALL_LOG=1 on the brain. Reads only.",
			InputSchema: toolObjectSchema(nil, map[string]any{
				"limit":     toolIntegerSchema("Recalls per page (default 50, max 200)."),
				"before":    toolIntegerSchema("next_before of the previous page."),
				"record_id": toolStringSchema("Only recalls that returned this memory or knowledge id."),
				"contains":  toolStringSchema("Only recalls whose query contains this text."),
				"key":       toolStringSchema("Only recalls made with this key id. A confined key always sees its own only."),
				"project":   toolStringSchema("Only recalls asked from this project."),
				"trace_id":  toolStringSchema("Only recalls of this flow: the trace id (32 lowercase hex) of the traceparent the request carried."),
				"empty":     toolBooleanSchema("Only recalls that returned nothing."),
			}),
		},
		{
			Name:        "recall_log_get",
			Description: "One past recall with its explain record: the plan it searched with, each retriever's candidates with raw scores, what each relevance floor or filter dropped, and the final hits, plus any feedback given on them. Reads only.",
			InputSchema: toolObjectSchema([]string{"recall_id"}, map[string]any{
				"recall_id": toolStringSchema("The id from recall_log_list."),
			}),
		},
		{
			Name: "recall_log_stats",
			Description: "Which records recalls reach: totals (recalls, empty, degraded), the most-recalled records with their last recall and the queries that reached them, " +
				"one record's history (record_id), and memories never recalled, oldest first. Reads only.",
			InputSchema: toolObjectSchema(nil, map[string]any{
				"limit":     toolIntegerSchema("Records per list (default 25, max 500)."),
				"record_id": toolStringSchema("Also report this record: how often it was recalled, the last time, and by which queries."),
				"key":       toolStringSchema("Only recalls made with this key id. A confined key always sees its own only."),
			}),
		},
		{
			Name:        "recall_feedback",
			Description: "Mark a hit of a past recall as useful or noise (or clear the mark). The marks become recall-eval golden cases through recall_feedback_export.",
			InputSchema: toolObjectSchema([]string{"recall_id", "record_id", "verdict"}, map[string]any{
				"recall_id": toolStringSchema("The recall, from recall_log_list."),
				"record_id": toolStringSchema("The memory or knowledge id of the hit."),
				"verdict":   toolEnumSchema("What the hit was for this query.", RecallVerdictUseful, RecallVerdictNoise, RecallVerdictClear),
				"note":      toolStringSchema("Optional: why."),
			}),
			Mutates: true,
		},
		{
			Name:        "recall_feedback_export",
			Description: "The feedback given on recalls as recall-eval golden cases (the JSONL format of cortexdb-mcp-stdio --recall-eval): one per recall, useful hits expected, noise forbidden. Reads only.",
			InputSchema: toolObjectSchema(nil, map[string]any{
				"since_ms": toolIntegerSchema("Only feedback given at or after this Unix time in milliseconds."),
			}),
		},
	}
}

func decodeToolInput[T any](name string, input json.RawMessage) (T, error) {
	var req T
	if len(input) > 0 {
		if err := json.Unmarshal(input, &req); err != nil {
			return req, fmt.Errorf("decode %s: %w", name, err)
		}
	}
	return req, nil
}

func (t *GraphRAGToolbox) callRecallLogTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	switch name {
	case "recall_log_list":
		req, err := decodeToolInput[RecallLogListRequest](name, input)
		if err != nil {
			return nil, true, err
		}
		resp, err := t.db.ListRecalls(ctx, req)
		return resp, true, err
	case "recall_log_get":
		req, err := decodeToolInput[RecallLogGetRequest](name, input)
		if err != nil {
			return nil, true, err
		}
		resp, err := t.db.GetRecall(ctx, req)
		return resp, true, err
	case "recall_log_stats":
		req, err := decodeToolInput[RecallStatsRequest](name, input)
		if err != nil {
			return nil, true, err
		}
		resp, err := t.db.RecallStats(ctx, req)
		return resp, true, err
	case "recall_feedback":
		req, err := decodeToolInput[RecallFeedbackRequest](name, input)
		if err != nil {
			return nil, true, err
		}
		resp, err := t.db.RecallFeedback(ctx, req)
		return resp, true, err
	case "recall_feedback_export":
		req, err := decodeToolInput[RecallFeedbackExportRequest](name, input)
		if err != nil {
			return nil, true, err
		}
		resp, err := t.db.ExportRecallFeedback(ctx, req)
		return resp, true, err
	}
	return nil, false, nil
}

// addRecallLogMCPTools registers the recall-log tools beside their
// definitions.
func addRecallLogMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, db *DB) {
	addGraphRAGMCPTool(server, definitions["recall_log_list"], func(ctx context.Context, req RecallLogListRequest) (RecallLogListResponse, error) {
		resp, err := db.ListRecalls(ctx, req)
		if err != nil {
			return RecallLogListResponse{}, err
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["recall_log_get"], func(ctx context.Context, req RecallLogGetRequest) (RecallLogEntry, error) {
		resp, err := db.GetRecall(ctx, req)
		if err != nil {
			return RecallLogEntry{}, err
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["recall_log_stats"], func(ctx context.Context, req RecallStatsRequest) (RecallStatsResponse, error) {
		resp, err := db.RecallStats(ctx, req)
		if err != nil {
			return RecallStatsResponse{}, err
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["recall_feedback"], func(ctx context.Context, req RecallFeedbackRequest) (RecallFeedbackResponse, error) {
		resp, err := db.RecallFeedback(ctx, req)
		if err != nil {
			return RecallFeedbackResponse{}, err
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["recall_feedback_export"], func(ctx context.Context, req RecallFeedbackExportRequest) (RecallFeedbackExportResponse, error) {
		resp, err := db.ExportRecallFeedback(ctx, req)
		if err != nil {
			return RecallFeedbackExportResponse{}, err
		}
		return *resp, nil
	})
}
