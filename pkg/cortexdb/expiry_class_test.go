package cortexdb

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// readToolsAndTheExpiry is the class of the expiry gate: every tool that
// does not write, and what it does with an expired document. "gated" tools
// are called below and must not return the expired document's text, chunk
// or node; the others are named, with why, in the body of the PR that adds
// the expiry. A read tool added later lands in no entry and fails here.
var readToolsAndTheExpiry = map[string]string{
	"build_context":                          "gated",
	"cortex_query":                           "gated",
	"expand_graph":                           "gated",
	"find_nodes":                             "gated",
	"get_chunks":                             "gated",
	"get_nodes":                              "gated",
	"knowledge_search":                       "gated",
	"knowledge_memory_build_context_pack":    "gated",
	"knowledge_memory_expand_entity_context": "gated",
	"knowledge_memory_neighbors":             "gated",
	"knowledge_memory_recall":                "gated",
	"knowledge_memory_shortest_path":         "gated",
	"representative_records":                 "gated",
	"search_chunks_by_entities":              "gated",
	"search_graphrag_lexical":                "gated",
	"search_paths":                           "gated",
	"search_text":                            "gated",
	"aggregate_metadata":                     "gated: TestAggregateMetadataCountsVisibleRowsOnly",
	"search_vector_range":                    "gated: TestSearchVectorRangeLeavesExpiredDocumentsOut",

	"knowledge_get": "by id: the record, until the sweep",
	"changes_since": "the change feed: an insert happened",

	// The recall log keeps what a recall returned when it ran, titles
	// included, for its retention; a record that expired since is history.
	"recall_log_list":        "the recall log: what a recall returned then",
	"recall_log_get":         "the recall log: what a recall returned then",
	"recall_log_stats":       "the recall log: what recalls returned then",
	"recall_feedback_export": "the recall log: feedback on what a recall returned then",

	"graph_cypher_query":                  "the whole graph",
	"graph_diff":                          "the whole graph",
	"graph_health":                        "the whole graph",
	"graph_list_all":                      "the whole graph",
	"graph_property_values":               "the whole graph",
	"graph_schema":                        "the whole graph",
	"graph_snapshot":                      "the whole graph",
	"graph_statistics":                    "the whole graph",
	"knowledge_graph_export":              "the whole graph",
	"knowledge_graph_find":                "the whole graph",
	"knowledge_graph_infer_explain":       "the whole graph",
	"knowledge_graph_infer_explain_match": "the whole graph",
	"knowledge_graph_infer_summary":       "the whole graph",
	"knowledge_graph_namespace_list":      "the whole graph",
	"knowledge_graph_shacl_validate":      "the whole graph",
	"predict_graph_edges":                 "the whole graph",
	"rank_graph_nodes":                    "the whole graph",

	"disambiguate_mentions": "entities and relations",
	"fact_provenance":       "entities and relations: names the chunk ids, reports an expired one missing",
	"inference_explain":     "entities and relations",
	"uncited_facts":         "entities and relations",
	"verify_claims":         "entities and relations",

	"check_against_sources": "named sources: an expired or proposed one is missing (TestAnExpiredOrProposedSourceIsMissing)",

	"memory_get":      "memories: their own expires_at, as on main",
	"memory_list_all": "memories: their own expires_at, as on main",
	"memory_search":   "memories: their own expires_at, as on main",

	"contract_needs_attention": "no document: contracts",
	"contract_tally":           "no document: contracts",
	"decision_chain":           "no document: decisions",
	"decision_precedents":      "no document: decisions",
	"execution_run_get":        "no document: runs",
	"execution_run_replay":     "no document: runs",
	"execution_runs_list":      "no document: runs",
	"execution_step_lineage":   "no document: runs",
	"object_set_resolve":       "no document: ontologies",
	"ontology_action_list":     "no document: ontologies",
	"ontology_diff":            "no document: ontologies",
	"ontology_draft":           "no document: ontologies",
	"ontology_get":             "no document: ontologies",
	"ontology_list":            "no document: ontologies",
	"rules_list":               "no document: rules",
}

func TestEveryReadToolIsGatedOrNamed(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "read-class")
	everyGatedToolHides(t, db, tools, ctx, func(r *KnowledgeSaveRequest) { r.ExpiresAt = strPtr(pastInstant()) })
}

// everyGatedToolHides saves the document "old", hidden by hide, and the
// document "live", then calls every gated read tool and checks that none
// returns the hidden one and each returns the live one.
func everyGatedToolHides(t *testing.T, db *DB, tools *GraphRAGToolbox, ctx context.Context, hide func(*KnowledgeSaveRequest)) {
	t.Helper()
	oldChunk, liveChunk := graphChunkNodeID("old", 0), graphChunkNodeID("live", 0)
	// Each document mentions Arvid Lund and a place, and states one
	// relation between them with its chunk as provenance, so the path and
	// walk tools have a fact from each document to reach.
	facts := func(chunk, place, relation string) ([]ToolEntityInput, []ToolRelationInput) {
		return []ToolEntityInput{{Name: "Arvid Lund", ChunkIDs: []string{chunk}}, {Name: place, ChunkIDs: []string{chunk}}},
			[]ToolRelationInput{{From: "Arvid Lund", To: place, Type: relation, ChunkIDs: []string{chunk}}}
	}
	oldEntities, oldRelations := facts(oldChunk, "Lund", "raised_in")
	old := KnowledgeSaveRequest{KnowledgeID: "old", Content: "Arvid Lund grew up by the harbour of Lund.", Entities: oldEntities, Relations: oldRelations}
	hide(&old)
	if _, err := db.SaveKnowledge(ctx, old); err != nil {
		t.Fatal(err)
	}
	liveEntities, liveRelations := facts(liveChunk, "Uppsala", "teaches_in")
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "live", Content: "Arvid Lund teaches in Uppsala.", Entities: liveEntities, Relations: liveRelations}); err != nil {
		t.Fatal(err)
	}
	entity, oldDoc := EntityNodeID("Arvid Lund"), graphDocumentNodeID("old")
	// One request for every gated tool, which asks for the expired document
	// by id wherever a tool takes ids. search_paths echoes its seeds, so it
	// starts from the entity alone. knowledge_memory_shortest_path starts
	// from the chunk, since the edges run from the chunk to the entity:
	// from the expired chunk it must find no path, from the live one it
	// must find one.
	argsOf := func(name, from, to string) json.RawMessage {
		a := map[string]any{
			"query": "Arvid Lund raised Lund Uppsala", "keywords": []string{"Arvid", "Lund", "raised", "Uppsala"},
			"entity_names": []string{"Arvid Lund"}, "entity_name": "Arvid Lund", "top_k": 10, "max_hops": 2, "detail": "full",
			"chunk_ids": []string{oldChunk, liveChunk}, "node_ids": []string{entity, oldChunk, oldDoc, liveChunk},
			"from_node_id": from, "to_node_id": to, "group_by": "title",
			"names": []string{"Arvid Lund"},
		}
		if name == "search_paths" {
			delete(a, "node_ids")
			a["include_bookkeeping"], a["direction"] = true, "both"
		}
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	// The relation is a fact between entities, kept by every tool; the
	// passage is what the expiry hides.
	leaks := []string{"harbour", oldChunk, oldDoc, url.PathEscape(oldChunk), `"document_id":"old"`}

	var unclassified []string
	for _, d := range tools.Definitions() {
		if d.Mutates {
			continue
		}
		how, ok := readToolsAndTheExpiry[d.Name]
		if !ok {
			unclassified = append(unclassified, d.Name)
			continue
		}
		if how != "gated" {
			continue
		}
		from, to := entity, oldDoc
		if d.Name == "knowledge_memory_shortest_path" {
			if _, err := tools.Call(ctx, d.Name, argsOf(d.Name, oldChunk, EntityNodeID("Lund"))); err == nil || !strings.Contains(err.Error(), "no path") {
				t.Errorf("%s from the expired chunk: %v, want no path", d.Name, err)
			}
			from, to = liveChunk, EntityNodeID("Uppsala")
		}
		out, err := tools.Call(ctx, d.Name, argsOf(d.Name, from, to))
		if err != nil {
			t.Errorf("%s: %v", d.Name, err)
			continue
		}
		body, _ := json.Marshal(out)
		for _, leak := range leaks {
			if strings.Contains(string(body), leak) {
				t.Errorf("%s returned the expired document (%s): %.300s", d.Name, leak, body)
				break
			}
		}
		// A check that cannot fail proves nothing: the same call returns
		// the live document, by its chunk or its text, which the request
		// does not echo.
		if !strings.Contains(string(body), liveChunk) && !strings.Contains(string(body), "teaches in Uppsala") {
			t.Errorf("%s did not return the live document either, so its check proves nothing: %.300s", d.Name, body)
		}
	}
	// knowledge_memory_expand_entity_context also takes node ids alone, and
	// then names each node it reads after its content: the expired chunk
	// is not read.
	out, err := tools.Call(ctx, "knowledge_memory_expand_entity_context", json.RawMessage(`{"entity_ids":["`+oldChunk+`","`+liveChunk+`"]}`))
	if err != nil {
		t.Fatalf("knowledge_memory_expand_entity_context by entity_ids: %v", err)
	}
	expanded, _ := out.(*KnowledgeMemoryExpandEntityContextResponse)
	if expanded == nil {
		t.Fatalf("knowledge_memory_expand_entity_context answered %T", out)
	}
	expanded.EntityNodeIDs = nil // the caller's own ids
	body, _ := json.Marshal(expanded)
	for _, leak := range leaks {
		if strings.Contains(string(body), leak) {
			t.Errorf("knowledge_memory_expand_entity_context by entity_ids returned the hidden document (%s): %.300s", leak, body)
		}
	}
	if !strings.Contains(string(body), "teaches in Uppsala") {
		t.Errorf("knowledge_memory_expand_entity_context by entity_ids did not return the live document either: %.300s", body)
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("read tools with no entry in readToolsAndTheExpiry: %v", unclassified)
	}
	for name := range readToolsAndTheExpiry {
		if def, ok := toolDefinitionByName(tools, name); !ok || def.Mutates {
			t.Errorf("readToolsAndTheExpiry names %s, which is not a read tool", name)
		}
	}
}

func toolDefinitionByName(tools *GraphRAGToolbox, name string) (ToolDefinition, bool) {
	for _, d := range tools.Definitions() {
		if d.Name == name {
			return d, true
		}
	}
	return ToolDefinition{}, false
}

// DB.SearchGraphRAG, the library entry the typed SearchGraphRag RPC calls,
// leaves an expired passage out of its seeds and of the walk from them, in
// every retrieval mode, while the live passage that shares its entity stays.
func TestSearchGraphRAGLeavesAnExpiredPassageOut(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "sgr.db")), WithEmbedder(newKeywordEmbedder("arvid", "lund", "raised", "teaches", "uppsala")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	oldChunk, liveChunk := graphChunkNodeID("old", 0), graphChunkNodeID("live", 0)
	mentions := func(chunk string) []ToolEntityInput {
		return []ToolEntityInput{{Name: "Arvid Lund", ChunkIDs: []string{chunk}}}
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "old", Content: "Arvid Lund was raised in Lund.", Entities: mentions(oldChunk), ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "live", Content: "Arvid Lund teaches in Uppsala.", Entities: mentions(liveChunk)}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "graph", "auto", "lexical"} {
		res, err := db.SearchGraphRAG(ctx, "Arvid Lund raised in Lund", GraphRAGQueryOptions{TopK: 5, RetrievalMode: mode, MaxRelatedChunks: 5})
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		body, _ := json.Marshal(res)
		// The response repeats the query, so the expired text is looked for
		// by what only the passage says.
		if strings.Contains(string(body), "was raised") || strings.Contains(string(body), oldChunk) {
			t.Errorf("mode %q returned the expired passage: %.300s", mode, body)
		}
		if !strings.Contains(string(body), "Uppsala") {
			t.Errorf("mode %q lost the live passage: %.300s", mode, body)
		}
	}
}
