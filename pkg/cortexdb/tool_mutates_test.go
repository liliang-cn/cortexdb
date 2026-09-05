package cortexdb

import (
	"sort"
	"testing"
)

// toolWrites is the deliberate decision for every tool the toolbox defines:
// true when calling it can change the brain, false when it only reads.
//
// It is an expectation table and not a derived list on purpose. Mutates is a
// bool, so its zero value is false, so a tool definition that says nothing
// about itself is claiming to be a read — the one direction that is dangerous,
// because a read-only key would then be handed a write. Reviewer attention is
// not a control for that: the missing line looks like every other definition.
// So the completeness test below fails on any name that is not in this table,
// which makes adding a tool a compile-and-CI-visible decision taken here, in
// the file whose whole subject is which tools write.
var toolWrites = map[string]bool{
	// GraphRAG ingest and graph editing.
	"ingest_document":       true,
	"upsert_entities":       true,
	"upsert_relations":      true,
	"delete_entities":       true,
	"delete_document_graph": true,
	// dry_run makes the two deletes above read-only for one argument value,
	// which authorization never sees. A tool that can write is a write.
	"extract_conversation":    true, // persist=true writes graph and knowledge
	"vector_dimension_repair": true, // re-embeds and rewrites vectors unless dry_run
	"apply_inference":         true, // materializes inferred edges

	// GraphRAG retrieval. All of these only read what is stored; none of them
	// records the access, caches an embedding, or writes a rendered file.
	"search_text":               false,
	"cortex_query":              false,
	"search_chunks_by_entities": false,
	"expand_graph":              false,
	"get_nodes":                 false,
	"find_nodes":                false,
	"get_chunks":                false,
	"build_context":             false,
	"search_graphrag_lexical":   false,
	"fact_provenance":           false,
	"uncited_facts":             false,

	// Ontology.
	"ontology_save":         true,
	"ontology_delete":       true,
	"ontology_action_apply": true, // writes, and appends to the audit trail
	"ontology_get":          false,
	"ontology_list":         false,
	"ontology_diff":         false, // compares a candidate, stores nothing
	// Reads the graph's vocabulary and proposes a schema for it. A deriver
	// that saved would be a deriver that decided, so it hands the draft back
	// and a person calls ontology_save.
	"ontology_draft":       false,
	"ontology_action_list": false,
	"object_set_resolve":   false,

	// Knowledge.
	"knowledge_save":   true,
	"knowledge_update": true,
	"knowledge_delete": true,
	"knowledge_get":    false,
	"knowledge_search": false,

	// Knowledge graph / RDF.
	"knowledge_graph_namespace_upsert": true,
	"knowledge_graph_upsert":           true,
	"knowledge_graph_delete":           true,
	"knowledge_graph_import":           true,
	"knowledge_graph_infer_refresh":    true,
	// knowledge_graph_query is the trap in this table. CortexDB's SPARQL
	// subset executes INSERT DATA and the DELETE forms, so the tool named
	// "query" rewrites the graph.
	"knowledge_graph_query":               true,
	"knowledge_graph_namespace_list":      false,
	"knowledge_graph_find":                false,
	"knowledge_graph_export":              false,
	"knowledge_graph_shacl_validate":      false,
	"knowledge_graph_infer_summary":       false,
	"knowledge_graph_infer_explain":       false,
	"knowledge_graph_infer_explain_match": false,

	// Scoped agent memory.
	"memory_save":     true,
	"memory_update":   true,
	"memory_delete":   true,
	"memory_get":      false,
	"memory_search":   false,
	"memory_list_all": false,
	"graph_list_all":  false,

	// The knowledge contract. Both read: one counts what is on the shelf, the
	// other lists what needs a person. Neither writes a grade — a producer
	// grades its own records, and a reader that could re-grade them would be
	// able to mark something verified without anything having checked it.
	"contract_tally":           false,
	"contract_needs_attention": false,

	// The decision ledger. Recording writes a node and its edges; the other
	// two walk what is already there. decision_chain is the one worth saying
	// out loud: it reads a decision's premises and their grades and records
	// nothing about having been asked, which is what lets a read-only key ask
	// why something was done.
	"decision_record":     true,
	"decision_chain":      false,
	"decision_precedents": false,

	// KnowledgeMemory facade.
	"knowledge_memory_remember":             true,
	"knowledge_memory_promote_to_knowledge": true,
	"knowledge_memory_consolidate":          true, // persists a summary memory
	// Reflect is a write because a configured KnowledgeMemoryReflector is
	// caller-supplied and nothing in the interface stops it writing. Not
	// decidable from this repository, so: write.
	"knowledge_memory_reflect":               true,
	"knowledge_memory_recall":                false,
	"knowledge_memory_build_context_pack":    false,
	"knowledge_memory_expand_entity_context": false,
	"knowledge_memory_neighbors":             false,
	"knowledge_memory_shortest_path":         false,
}

// toolCount is what the toolbox holds today. Asserted separately from the
// table so that a tool added without a decision cannot slip through by sharing
// a name with one already listed, and so that a tool quietly disappearing is
// noticed too. Change it in the same commit that adds the tool and its row.
const toolCount = 67

// TestEveryToolDeclaresWhetherItWrites is the test the Mutates doc comment
// promises: it makes forgetting impossible rather than merely unlikely.
//
// Without it, the failure is silent in the worst direction. A new tool that
// writes and never sets Mutates is classified as a read by pkg/authz, and every
// read-only key on every shared brain can call it — with nothing in the build,
// the tests, or the diff to say so.
func TestEveryToolDeclaresWhetherItWrites(t *testing.T) {
	definitions := ToolDefinitions()
	if len(definitions) != toolCount {
		t.Errorf("the toolbox defines %d tools, toolCount says %d; a tool was added or removed "+
			"without a decision about whether it writes", len(definitions), toolCount)
	}

	seen := make(map[string]struct{}, len(definitions))
	for _, d := range definitions {
		if _, dup := seen[d.Name]; dup {
			t.Errorf("%s is defined twice; the policy would keep whichever came last", d.Name)
		}
		seen[d.Name] = struct{}{}

		want, decided := toolWrites[d.Name]
		if !decided {
			t.Errorf("%s is defined but nobody decided whether it writes; add it to toolWrites in "+
				"this file after reading what it calls, and set Mutates to match. Mutates defaults "+
				"to false, so leaving it out claims the tool is a read", d.Name)
			continue
		}
		if d.Mutates != want {
			t.Errorf("%s declares Mutates=%v, this table says %v", d.Name, d.Mutates, want)
		}
	}

	stale := make([]string, 0)
	for name := range toolWrites {
		if _, ok := seen[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("toolWrites decides about tools that no longer exist: %v — drop the rows", stale)
	}
}

// TestTheToolboxIsMostlyReadsSoReadOnlyKeysAreWorthHaving is a sanity floor,
// not an assertion about an exact split. The point of deriving authorization
// from Mutates was that a read-only key can do useful work; if a change ever
// left almost every tool a write, the feature would be back where it started
// and this says so instead of leaving it to be noticed in production.
func TestTheToolboxIsMostlyReadsSoReadOnlyKeysAreWorthHaving(t *testing.T) {
	reads, writes := 0, 0
	for _, d := range ToolDefinitions() {
		if d.Mutates {
			writes++
			continue
		}
		reads++
	}
	if reads <= writes {
		t.Fatalf("%d reads against %d writes: a read-only key can reach less than half the toolbox", reads, writes)
	}
}
