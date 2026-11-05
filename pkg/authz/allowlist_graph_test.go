package authz

import (
	"errors"
	"strings"
	"testing"
)

// graphWalkCalls is one call to each graph tool the facade confines, with
// every argument its schema declares, the node ids among them.
var graphWalkCalls = map[string]string{
	"search_chunks_by_entities": `{"entity_names":["Kestrel Vale"],"top_k":5,"max_hops":2}`,
	"search_paths": `{"entity_names":["Kestrel Vale"],"node_ids":["entity:alpha_mill"],"max_depth":3,"direction":"both","edge_types":["runs"],
		"relation_policies":{"runs":{"weight":1}},"decay":0.5,"use_edge_weights":true,"max_paths":3,"max_expansions":100,"include_bookkeeping":false,"with_text":true}`,
	"knowledge_memory_expand_entity_context": `{"entity_ids":["entity:kestrel_vale"],"entity_names":["Kestrel Vale"],"max_hops":2,"edge_types":["runs"],"node_types":["entity"],
		"limit":10,"top_k_chunks":4,"max_context_chunks":4,"max_context_chars":2000,"per_document_limit":2,"chunk_window":1,"retrieval_mode":"graph",
		"disable_graph":false,"graph_light":true,"max_entities_per_chunk":4}`,
	"expand_graph":                   `{"node_ids":["entity:kestrel_vale"],"max_hops":2,"edge_types":["runs"],"node_types":["chunk"],"limit":10,"relation_policies":{"runs":{"weight":1}}}`,
	"get_nodes":                      `{"node_ids":["entity:kestrel_vale","chunk:b1:000"]}`,
	"find_nodes":                     `{"names":["Kestrel Vale"],"node_types":["entity"],"limit":5,"skip_chunks":true}`,
	"knowledge_memory_neighbors":     `{"node_id":"entity:kestrel_vale","entity_name":"Kestrel Vale","max_depth":2,"edge_types":["runs"],"node_types":["chunk"],"direction":"both","limit":10}`,
	"knowledge_memory_shortest_path": `{"from_node_id":"entity:kestrel_vale","to_node_id":"entity:alpha_mill","from_entity_name":"Kestrel Vale","to_entity_name":"Alpha Mill"}`,
}

// The graph tools that walk from a seed or read nodes by id have no scope
// field to force: the facade holds what they serve to the caller scope
// (cortexdb.CallerScope), documents by collection, memories by bucket and
// entities by the collections that list them. So a key confined on any
// field may call them with every argument they declare, node ids included:
// a node outside the scope answers as a node that does not exist.
func TestTheGraphWalksAreOpenToAConfinedKeyAndConfinedByTheFacade(t *testing.T) {
	keys := []Key{
		{ID: "alpha", Secret: "a", Clearance: ReadOnly, Scope: Scope{Collection: "alpha"}},
		{ID: "hermes", Secret: "h", Clearance: ReadOnly, Scope: Scope{UserID: "hermes"}},
		{ID: "team", Secret: "t", Clearance: ReadOnly, Scope: Scope{Namespace: "team"}},
	}
	for tool, args := range graphWalkCalls {
		rule, ok := confinedTools[tool]
		if !ok {
			t.Errorf("%s is not on the allowlist", tool)
			continue
		}
		if !rule.facade || len(rule.scope) != 0 || len(rule.gates) != 0 || rule.stores != storeKnowledge|storeMemory {
			t.Errorf("%s: rule %+v, want both stores confined by the facade, with no scope field and no gate", tool, rule)
		}
		for _, key := range keys {
			if err := key.AuthorizeToolArgs(tool, []byte(args)); err != nil {
				t.Errorf("%s for %s: %v", tool, key.ID, err)
			}
		}
	}
	// The table says how they are confined.
	if row := "| `expand_graph` | (by id only) | knowledge and memory |  | by the facade (CallerScope) | `as_of` |"; !strings.Contains(ConfinedToolsTable(), row) {
		t.Errorf("the table lacks the row %s", row)
	}
	// knowledge_get keeps its store: a key confined on a memory field is
	// still refused a tool of the other store with no gate.
	hermes := keys[1]
	if err := hermes.AuthorizeToolArgs("knowledge_get", []byte(`{"knowledge_id":"a1"}`)); !errors.Is(err, ErrDenied) {
		t.Fatalf("knowledge_get for a user-confined key: %v, want a denial", err)
	}
}

// The graph tools that read the whole stored graph, not a walk from a seed,
// stay refused, each with its reason, and so does find_nodes, which scans
// the content of every node and opens in a change of its own.
func TestTheWholeGraphReadsStayRefusedToAConfinedKey(t *testing.T) {
	alpha := Key{ID: "alpha", Secret: "a", Clearance: ReadOnly, Scope: Scope{Collection: "alpha"}}
	for _, tool := range []string{"graph_list_all", "graph_cypher_query", "rank_graph_nodes", "predict_graph_edges", "disambiguate_mentions", "graph_statistics"} {
		why := RefusedTools()[tool]
		if why == "" || why == defaultRefusal {
			t.Errorf("%s is refused without a reason of its own: %q", tool, why)
		}
		if err := alpha.AuthorizeToolArgs(tool, []byte(`{}`)); !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), why) {
			t.Errorf("%s for alpha: %v, want the denial %q", tool, err, why)
		}
	}
}
