package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
)

func TestHealthAndInfoAnswerWithoutOpeningAnything(t *testing.T) {
	srv := newTestServer(t, "")

	health := mustDo(t, srv, http.MethodGet, "/v1/health", "", http.StatusOK)
	if health["ok"] != true {
		t.Fatalf("health = %v, want ok:true", health)
	}

	info := mustDo(t, srv, http.MethodGet, "/v1/info", "", http.StatusOK)
	if info["version"] != cortexdbroot.Version {
		t.Fatalf("info version = %v, want %s", info["version"], cortexdbroot.Version)
	}
	if info["has_embedder"] != false {
		t.Fatalf("info has_embedder = %v, want false for a lexical server", info["has_embedder"])
	}
	if path, _ := info["db_path"].(string); !strings.HasSuffix(path, ".db") {
		t.Fatalf("info db_path = %q, want the path the server was told about", path)
	}
}

func TestASavedDocumentComesBackFromSearchOverHTTP(t *testing.T) {
	srv := newTestServer(t, "")

	saved := mustDo(t, srv, http.MethodPost, "/v1/knowledge", `{
		"knowledge_id": "k1",
		"title": "Go concurrency",
		"content": "Goroutines are lightweight threads managed by the Go runtime. Channels connect goroutines.",
		"metadata": {"lang": "en"}
	}`, http.StatusOK)
	knowledge, _ := saved["knowledge"].(map[string]any)
	if knowledge["id"] != "k1" {
		t.Fatalf("saved knowledge = %v, want id k1", saved)
	}

	got := mustDo(t, srv, http.MethodGet, "/v1/knowledge?knowledge_id=k1", "", http.StatusOK)
	record, _ := got["knowledge"].(map[string]any)
	if record["title"] != "Go concurrency" {
		t.Fatalf("get = %v, want the saved title", got)
	}

	found := mustDo(t, srv, http.MethodPost, "/v1/knowledge/search",
		`{"query":"goroutines channels","top_k":3}`, http.StatusOK)
	results, _ := found["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("search over HTTP found nothing: %v", found)
	}
	top, _ := results[0].(map[string]any)
	if top["knowledge_id"] != "k1" {
		t.Fatalf("top hit = %v, want k1", top)
	}
	// The hit has to carry the text back, not just an id: a search that
	// returns identifiers a caller then has to fetch one by one is not the
	// thing anyone is evaluating in their first five minutes.
	if snippet, _ := top["snippet"].(string); !strings.Contains(snippet, "Goroutines") {
		t.Fatalf("top hit snippet = %q, want the saved content", snippet)
	}

	deleted := mustDo(t, srv, http.MethodDelete, "/v1/knowledge?knowledge_id=k1", "", http.StatusOK)
	if deleted["deleted"] != true {
		t.Fatalf("delete = %v, want deleted:true", deleted)
	}
	status, payload := do(t, srv, http.MethodGet, "/v1/knowledge?knowledge_id=k1", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404: %s", status, payload)
	}
}

func TestAMemoryRoundTripsThroughSaveSearchGetAndDelete(t *testing.T) {
	srv := newTestServer(t, "")

	mustDo(t, srv, http.MethodPost, "/v1/memory", `{
		"memory_id": "m1",
		"user_id": "ada",
		"scope": "user",
		"content": "Ada prefers the metric system for every measurement in the report."
	}`, http.StatusOK)

	found := mustDo(t, srv, http.MethodPost, "/v1/memory/search",
		`{"query":"metric system","user_id":"ada","scope":"user","top_k":3}`, http.StatusOK)
	results, _ := found["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("memory search found nothing: %v", found)
	}

	got := mustDo(t, srv, http.MethodGet, "/v1/memory?memory_id=m1", "", http.StatusOK)
	memory, _ := got["memory"].(map[string]any)
	if content, _ := memory["content"].(string); !strings.Contains(content, "metric system") {
		t.Fatalf("get = %v, want the saved content", got)
	}

	deleted := mustDo(t, srv, http.MethodDelete, "/v1/memory?memory_id=m1", "", http.StatusOK)
	if deleted["deleted"] != true {
		t.Fatalf("delete = %v, want deleted:true", deleted)
	}
}

func TestQueryFusesTwoLanesOverHTTP(t *testing.T) {
	srv := newTestServer(t, "")

	mustDo(t, srv, http.MethodPost, "/v1/knowledge", `{
		"knowledge_id": "ops",
		"title": "Apollo",
		"content": "The apollo launch checklist covers telemetry and abort criteria."
	}`, http.StatusOK)

	// Two lexical lanes rather than one, because fusion is the thing being
	// exercised: a single lane would pass even if the fuser were skipped.
	resp := mustDo(t, srv, http.MethodPost, "/v1/query", `{
		"query": "apollo launch",
		"limit": 5,
		"include_raw": true,
		"prefetch": [
			{"name": "checklist", "kind": "lexical", "query": "apollo launch checklist", "limit": 5},
			{"name": "telemetry", "kind": "lexical", "query": "telemetry abort", "limit": 5}
		]
	}`, http.StatusOK)

	results, _ := resp["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("query returned nothing: %v", resp)
	}
	lanes, _ := resp["prefetches"].([]any)
	if len(lanes) != 2 {
		t.Fatalf("prefetches = %v, want both named lanes", resp["prefetches"])
	}
	top, _ := results[0].(map[string]any)
	scores, _ := top["source_scores"].(map[string]any)
	if len(scores) == 0 {
		t.Fatalf("top result carries no per-lane scores: %v", top)
	}
}

func TestRecallReturnsAContextPackOverHTTP(t *testing.T) {
	srv := newTestServer(t, "")

	mustDo(t, srv, http.MethodPost, "/v1/memory", `{
		"memory_id": "m1",
		"user_id": "ada",
		"scope": "user",
		"content": "The deploy runbook lives in the ops repository."
	}`, http.StatusOK)
	mustDo(t, srv, http.MethodPost, "/v1/knowledge", `{
		"knowledge_id": "k1",
		"title": "Deploys",
		"content": "The deploy runbook describes how the ops repository is released."
	}`, http.StatusOK)

	resp := mustDo(t, srv, http.MethodPost, "/v1/recall",
		`{"query":"deploy runbook","user_id":"ada","scope":"user"}`, http.StatusOK)
	pack, _ := resp["context_pack"].(map[string]any)
	if text, _ := pack["text"].(string); text == "" {
		t.Fatalf("recall returned an empty context pack: %v", resp)
	}
}

func TestTheGraphEndpointsWriteAndTraverse(t *testing.T) {
	srv := newTestServer(t, "")

	// A document first: entities hang off chunks, and an entity with no
	// document to mention it is not what a caller would ever build.
	mustDo(t, srv, http.MethodPost, "/v1/tools/ingest_document", `{
		"document_id": "doc1",
		"content": "Ada maintains the ops repository."
	}`, http.StatusOK)

	entities := mustDo(t, srv, http.MethodPost, "/v1/graph/entities", `{
		"document_id": "doc1",
		"entities": [
			{"id": "ada", "name": "Ada", "type": "Person"},
			{"id": "ops", "name": "ops repository", "type": "Repository"}
		]
	}`, http.StatusOK)
	ids, _ := entities["entity_node_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("upsert entities = %v, want two node ids", entities)
	}

	relations := mustDo(t, srv, http.MethodPost, "/v1/graph/relations", `{
		"document_id": "doc1",
		"relations": [{"from": "ada", "to": "ops", "type": "maintains"}]
	}`, http.StatusOK)
	if written, _ := relations["written"].(float64); written != 1 {
		t.Fatalf("upsert relations wrote %v edges, want 1: %v", relations["written"], relations)
	}

	// Expanded from the id the store handed back, not from the id that was
	// sent: entity nodes are keyed by the graph, and asking for "ada" reaches
	// nothing whatever the graph holds.
	seed, _ := ids[0].(string)
	expanded := mustDo(t, srv, http.MethodPost, "/v1/graph/expand",
		`{"node_ids":["`+seed+`"],"max_hops":1}`, http.StatusOK)
	edges, _ := expanded["edges"].([]any)
	if len(edges) == 0 {
		t.Fatalf("expand_graph found no edges from a node that has one: %v", expanded)
	}
}

func TestTriplesWrittenOverHTTPAreVisibleToSPARQL(t *testing.T) {
	srv := newTestServer(t, "")

	upserted := mustDo(t, srv, http.MethodPost, "/v1/graph/triples", `{
		"triples": [{
			"subject":   {"kind": "iri", "value": "http://example.org/ada"},
			"predicate": {"kind": "iri", "value": "http://example.org/maintains"},
			"object":    {"kind": "iri", "value": "http://example.org/ops"}
		}]
	}`, http.StatusOK)
	if count, _ := upserted["count"].(float64); count != 1 {
		t.Fatalf("upsert triples = %v, want count 1", upserted)
	}

	queried := mustDo(t, srv, http.MethodPost, "/v1/graph/sparql",
		`{"query":"SELECT ?o WHERE { <http://example.org/ada> <http://example.org/maintains> ?o }"}`,
		http.StatusOK)
	result, _ := queried["result"].(map[string]any)
	bindings, _ := result["bindings"].([]any)
	if len(bindings) != 1 {
		t.Fatalf("sparql returned %v bindings, want the one triple just written: %v", len(bindings), queried)
	}
}

func TestTheGenericToolEndpointRunsARealTool(t *testing.T) {
	srv := newTestServer(t, "")

	ingested := mustDo(t, srv, http.MethodPost, "/v1/tools/ingest_document",
		`{"document_id":"d1","content":"hello tools world"}`, http.StatusOK)
	if ingested["document_node_id"] == nil {
		t.Fatalf("ingest_document returned no document node: %v", ingested)
	}

	// search_text over the same toolbox: the tool endpoint has to be able to
	// read back what the tool endpoint wrote, or it is not one surface.
	found := mustDo(t, srv, http.MethodPost, "/v1/tools/search_text",
		`{"query":"hello tools","top_k":3}`, http.StatusOK)
	chunks, _ := found["chunks"].([]any)
	if len(chunks) == 0 {
		t.Fatalf("search_text found nothing it had just ingested: %v", found)
	}
}

func TestToolsAreListedWithTheirSchemas(t *testing.T) {
	srv := newTestServer(t, "")

	status, payload := do(t, srv, http.MethodGet, "/v1/tools", "", "")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/tools = %d: %s", status, payload)
	}
	var listed struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(payload, &listed); err != nil {
		t.Fatalf("tools list is not JSON: %v: %s", err, payload)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("tools list is empty")
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
		if len(tool.InputSchema) == 0 {
			t.Fatalf("tool %s is listed without a schema, so a caller cannot call it", tool.Name)
		}
	}
	if !names["ingest_document"] || !names["knowledge_search"] {
		t.Fatalf("expected the core tools to be reachable, got %d tools", len(names))
	}
}
