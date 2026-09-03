package httpapi

import (
	"context"
	"net/http"
	"strings"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// registerRoutes binds every endpoint to the facade method behind it.
//
// Each line is a route and a facade method and nothing else. That is the whole
// design: if a handler ever needs a body of its own, the capability belongs in
// pkg/cortexdb where both this and the gRPC server can reach it.
func registerRoutes(rt *router, db *cortexdb.DB, opts Options) {
	tools := db.GraphRAGTools()
	memory := db.KnowledgeMemory()

	rt.handle(http.MethodGet, "/v1/health", health)
	rt.handle(http.MethodGet, "/v1/info", info(db, opts.DBPath))

	rt.handle(http.MethodPost, "/v1/knowledge", jsonCall(db.SaveKnowledge))
	rt.handle(http.MethodPost, "/v1/knowledge/search", jsonCall(db.SearchKnowledge))
	rt.handle(http.MethodGet, "/v1/knowledge", byID("knowledge_id",
		func(id string) cortexdb.KnowledgeGetRequest {
			return cortexdb.KnowledgeGetRequest{KnowledgeID: id}
		}, db.GetKnowledge))
	rt.handle(http.MethodDelete, "/v1/knowledge", byID("knowledge_id",
		func(id string) cortexdb.KnowledgeDeleteRequest {
			return cortexdb.KnowledgeDeleteRequest{KnowledgeID: id}
		}, db.DeleteKnowledge))

	rt.handle(http.MethodPost, "/v1/memory", jsonCall(db.SaveMemory))
	rt.handle(http.MethodPost, "/v1/memory/search", jsonCall(db.SearchMemory))
	rt.handle(http.MethodGet, "/v1/memory", byID("memory_id",
		func(id string) cortexdb.MemoryGetRequest {
			return cortexdb.MemoryGetRequest{MemoryID: id}
		}, db.GetMemory))
	rt.handle(http.MethodDelete, "/v1/memory", byID("memory_id",
		func(id string) cortexdb.MemoryDeleteRequest {
			return cortexdb.MemoryDeleteRequest{MemoryID: id}
		}, db.DeleteMemory))

	rt.handle(http.MethodPost, "/v1/query", jsonCall(db.Query))
	rt.handle(http.MethodPost, "/v1/recall", jsonCall(memory.Recall))

	rt.handle(http.MethodPost, "/v1/graph/entities", jsonCall(tools.UpsertEntities))
	rt.handle(http.MethodPost, "/v1/graph/relations", jsonCall(tools.UpsertRelations))
	rt.handle(http.MethodPost, "/v1/graph/expand", jsonCall(tools.ExpandGraph))
	rt.handle(http.MethodPost, "/v1/graph/triples", jsonCall(db.UpsertKnowledgeGraph))
	rt.handle(http.MethodPost, "/v1/graph/sparql", jsonCall(db.QueryKnowledgeGraph))

	rt.handle(http.MethodGet, "/v1/tools", listTools(tools))
	rt.handleTool(callTool(tools))
}

// jsonCall binds a facade method to a route: JSON in, the facade's own request
// struct, the facade's own response struct, JSON out.
//
// The facade types carry their json tags already, so nothing in between gets to
// rename a field or drop one — which is what keeps this layer from becoming a
// second definition of the API.
func jsonCall[Req, Resp any](call func(context.Context, Req) (Resp, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Req
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := call(r.Context(), req)
		if err != nil {
			writeError(w, statusFor(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// byID binds a facade method whose whole input is one identifier to a GET or
// DELETE, so that reading a record does not need a request body.
func byID[Req, Resp any](param string, build func(id string) Req, call func(context.Context, Req) (Resp, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.URL.Query().Get(param))
		if id == "" {
			// Named rather than passed through empty: some facade methods
			// answer an empty id with "not found", and a 404 for a request
			// that was never well formed sends the caller looking for the
			// wrong problem.
			writeError(w, http.StatusBadRequest, param+" is required")
			return
		}
		resp, err := call(r.Context(), build(id))
		if err != nil {
			writeError(w, statusFor(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

type healthResponse struct {
	OK bool `json:"ok"`
}

type infoResponse struct {
	Version     string `json:"version"`
	DBPath      string `json:"db_path,omitempty"`
	HasEmbedder bool   `json:"has_embedder"`
}

// health answers without touching the database, like AdminService.Health: a
// probe that opened a connection would report the pool's health, not the
// server's, and would be a second writer against the same SQLite file.
func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{OK: true})
}

func info(db *cortexdb.DB, dbPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, infoResponse{
			Version:     cortexdbroot.Version,
			DBPath:      dbPath,
			HasEmbedder: db.HasEmbedder(),
		})
	}
}
