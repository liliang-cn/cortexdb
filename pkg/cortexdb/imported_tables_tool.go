package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The toolbox face of imported tables: list, describe, query. All three
// read. Importing is pkg/importflow's job, which is a workflow over this
// facade and has its own tools.

// ToolImportedTablesListRequest takes no arguments.
type ToolImportedTablesListRequest struct{}

// ToolImportedTablesListResponse lists the imported tables.
type ToolImportedTablesListResponse struct {
	Tables []ImportedTable `json:"tables"`
	Count  int             `json:"count"`
}

// ToolImportedTablesDescribeRequest names one imported table.
type ToolImportedTablesDescribeRequest struct {
	Table      string `json:"table"`
	SampleRows int    `json:"sample_rows,omitempty"`
}

// ImportedTablesList is the toolbox face of ListImportedTables.
func (t *GraphRAGToolbox) ImportedTablesList(ctx context.Context, _ ToolImportedTablesListRequest) (*ToolImportedTablesListResponse, error) {
	tables, err := t.db.ListImportedTables(ctx)
	if err != nil {
		return nil, err
	}
	return &ToolImportedTablesListResponse{Tables: tables, Count: len(tables)}, nil
}

// ImportedTablesDescribe is the toolbox face of DescribeImportedTable.
func (t *GraphRAGToolbox) ImportedTablesDescribe(ctx context.Context, req ToolImportedTablesDescribeRequest) (*ImportedTableDescription, error) {
	return t.db.DescribeImportedTable(ctx, req.Table, req.SampleRows)
}

// ImportedTablesQuery is the toolbox face of QueryImportedTables.
func (t *GraphRAGToolbox) ImportedTablesQuery(ctx context.Context, req ImportedTableQueryRequest) (*ImportedTableQueryResponse, error) {
	if req.MaxRows <= 0 {
		// A tool answer is read by a model; see defaultCypherToolRows.
		req.MaxRows = defaultImportedToolRows
	}
	return t.db.QueryImportedTables(ctx, req)
}

const defaultImportedToolRows = 200

func importedTablesToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:    "imported_tables_list",
			Mutates: false,
			Description: "List the tables that were imported from CSV files or SQL dumps and kept as tables: each one's name, where it came from, its columns with their types (integer, number, text, timestamp), its row count and when it was imported. " +
				"Call this first whenever a question is about imported rows as data — totals, averages, counts per group, rankings, joins between imported tables — and then imported_tables_query. An empty list means nothing was imported as a table, and those questions cannot be answered from tables; search instead. SQLite stores only.",
			InputSchema: toolObjectSchema(nil, map[string]any{}),
		},
		{
			Name:        "imported_tables_describe",
			Mutates:     false,
			Description: "Describe one imported table: its columns and types, its row count, and its first few rows exactly as stored. Call it before writing SQL for imported_tables_query — the sample rows show what a column really holds (how a date is written, whether a code is 'EU' or 'eu', which columns are empty), and a filter written from a column name alone returns a confident empty answer.",
			InputSchema: toolObjectSchema(
				[]string{"table"},
				map[string]any{
					"table":       toolStringSchema("The table's name as imported_tables_list gives it."),
					"sample_rows": toolIntegerSchema("How many rows to show. Default 5, at most 50."),
				},
			),
		},
		{
			Name:    "imported_tables_query",
			Mutates: false,
			Description: "Run one READ-ONLY SQL SELECT (SQLite dialect) over the imported tables and get columns and rows back. " +
				"Use it instead of a search when the answer is computed from rows rather than found in one: sums, averages, counts, GROUP BY, ORDER BY … LIMIT rankings, joins between imported tables, window functions, WITH / recursive CTEs, date arithmetic with strftime/date. A search returns the few most similar records; this reads every row. " +
				"Call imported_tables_list and imported_tables_describe first, and name tables and columns exactly as they report, in double quotes when a name is not a plain identifier (\"order id\"). A timestamp column holds text as the source wrote it; integer and number columns are numeric. " +
				"Only the imported tables exist here: the query cannot read memories, documents, the knowledge graph or any other table of the store, and cannot write, create, attach or set anything — INSERT, UPDATE, DELETE, DDL, PRAGMA, ATTACH and more than one statement are refused with an error. A CTE may not be named after something the database already holds. " +
				"Results are capped at max_rows (default 200) and flagged truncated; aggregate in SQL rather than fetching rows to add up. " +
				"Example: SELECT region, strftime('%Y-%m', ordered_at) AS month, round(avg(total), 2) AS avg_order FROM orders GROUP BY region, month ORDER BY region, month.",
			InputSchema: toolObjectSchema(
				[]string{"sql"},
				map[string]any{
					"sql":        toolStringSchema("One SELECT statement, e.g. SELECT c.country, sum(o.total) FROM orders o JOIN customers c ON c.id = o.customer_id GROUP BY c.country"),
					"params":     map[string]any{"type": "array", "description": "Values for ? placeholders in the SQL, in order."},
					"max_rows":   toolIntegerSchema("Cap on rows returned. Default 200, at most 10000."),
					"timeout_ms": toolIntegerSchema("Time budget in milliseconds. Default 10000, at most 60000."),
				},
			),
		},
	}
}

// callImportedTablesTool dispatches the three tools from JSON, the shape
// callExpiryTool uses.
func (t *GraphRAGToolbox) callImportedTablesTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	switch name {
	case "imported_tables_list":
		var req ToolImportedTablesListRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.ImportedTablesList(ctx, req)
		return resp, true, err
	case "imported_tables_describe":
		var req ToolImportedTablesDescribeRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.ImportedTablesDescribe(ctx, req)
		return resp, true, err
	case "imported_tables_query":
		var req ImportedTableQueryRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.ImportedTablesQuery(ctx, req)
		return resp, true, err
	}
	return nil, false, nil
}

// addImportedTablesMCPTools registers the three tools on the MCP server,
// next to their definitions.
func addImportedTablesMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, toolbox *GraphRAGToolbox) {
	addGraphRAGMCPTool(server, definitions["imported_tables_list"], toolbox.ImportedTablesList)
	addGraphRAGMCPTool(server, definitions["imported_tables_describe"], toolbox.ImportedTablesDescribe)
	addGraphRAGMCPTool(server, definitions["imported_tables_query"], toolbox.ImportedTablesQuery)
}
