package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// toolListResponse wraps the definitions so the body is an object. A bare JSON
// array leaves nowhere to add a field later without breaking every reader.
type toolListResponse struct {
	Tools []cortexdb.ToolDefinition `json:"tools"`
}

// listTools returns the toolbox's own definitions, schemas included.
//
// Unlike the gRPC surface, which has to flatten each input schema into a string
// because proto has no JSON-object type, the schema goes out here as the object
// it already is — so a caller can read /v1/tools and construct a valid call to
// anything in it without a second document.
func listTools(tools *cortexdb.GraphRAGToolbox) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, toolListResponse{Tools: tools.Definitions()})
	}
}

// callTool dispatches POST /v1/tools/{name} to the toolbox.
//
// This one route is why the endpoint list above can stay short: every tool the
// MCP server exposes, and every tool added to it later, is reachable over HTTP
// the day it is registered, without a line of code here.
func callTool(tools *cortexdb.GraphRAGToolbox) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("tool")

		var args json.RawMessage
		if !decodeBody(w, r, &args) {
			return
		}
		if len(args) == 0 {
			// No body means no arguments. A tool whose fields are all optional
			// is callable with a bare POST, and one whose fields are not says
			// which it wanted.
			args = json.RawMessage("{}")
		}
		// Every tool's input schema is an object. A JSON array or number would
		// otherwise reach the tool's own json.Unmarshal and come back as a
		// decode failure, which is a caller's mistake wearing a server error's
		// clothes.
		if !isJSONObject(args) {
			writeError(w, http.StatusBadRequest, "tool arguments must be a JSON object")
			return
		}

		result, err := tools.Call(r.Context(), name, args)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unknown tool") {
				writeError(w, http.StatusNotFound, err.Error())
				return
			}
			writeError(w, statusFor(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// isJSONObject reports whether raw is a JSON object. The decoder has already
// proved raw is valid JSON, so the first non-space byte settles it.
func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '{'
}
