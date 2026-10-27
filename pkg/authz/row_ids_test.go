package authz

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// loggedPaths is, tool by tool, every argument path that names rows and that
// the request log records: the paths the guard loads (ids), plus the two
// data paths that name rows without being guarded (memory_ids, which the
// facade answers with missing, and plan.filters.document_ids, a pin the
// collection filter decides). A tool of the allowlist that names no row is
// listed with none.
var loggedPaths = map[string][]string{
	"ingest_document":                        {"document_id"},
	"knowledge_save":                         {"knowledge_id"},
	"knowledge_update":                       {"knowledge_id"},
	"knowledge_get":                          {"knowledge_id", "knowledge_ids"},
	"knowledge_delete":                       {"knowledge_id"},
	"memory_save":                            {"memory_id", "supersedes"},
	"knowledge_memory_remember":              {"memory_id"},
	"memory_get":                             {"memory_id", "memory_ids"},
	"memory_update":                          {"memory_id"},
	"memory_delete":                          {"memory_id"},
	"knowledge_search":                       {"plan.filters.document_ids"},
	"search_text":                            {"plan.filters.document_ids"},
	"search_graphrag_lexical":                {"plan.filters.document_ids"},
	"memory_search":                          {"plan.filters.document_ids"},
	"knowledge_memory_recall":                {"plan.filters.document_ids"},
	"knowledge_memory_build_context_pack":    {"plan.filters.document_ids"},
	"search_vector_range":                    nil,
	"aggregate_metadata":                     nil,
	"representative_records":                 nil,
	"extract_conversation":                   nil,
	"check_against_sources":                  {"sentences[].sources"},
	"search_chunks_by_entities":              nil,
	"search_paths":                           {"node_ids"},
	"knowledge_memory_expand_entity_context": {"entity_ids"},
	"expand_graph":                           {"node_ids"},
	"get_nodes":                              {"node_ids"},
	"knowledge_memory_neighbors":             {"node_id"},
	"knowledge_memory_shortest_path":         {"from_node_id", "to_node_id"},
	"recall_log_list":                        {"record_id"},
	"recall_log_get":                         {"recall_id"},
	"recall_log_stats":                       {"record_id"},
	"recall_feedback":                        {"recall_id", "record_id"},
	"recall_feedback_export":                 nil,
}

// argsAt builds tool arguments with a value at each path: two ids in an
// array for a path whose last segment is plural or a list (supersedes,
// sources), one id otherwise, under the path as spelled; a segment that ends
// in [] is an array holding one object.
func argsAt(paths []string, extra map[string]any) ([]byte, map[string][]string) {
	obj := map[string]any{}
	for k, v := range extra {
		obj[k] = v
	}
	want := map[string][]string{}
	for _, p := range paths {
		segs := strings.Split(p, ".")
		last := segs[len(segs)-1]
		var v any = "id-" + last
		want[p] = []string{"id-" + last}
		if strings.HasSuffix(last, "_ids") || last == "supersedes" || last == "sources" {
			v = []any{"id-" + last + "-2", "id-" + last + "-1"}
			want[p] = []string{"id-" + last + "-1", "id-" + last + "-2"}
		}
		at := obj
		for _, s := range segs[:len(segs)-1] {
			name, each := strings.CutSuffix(s, "[]")
			var next map[string]any
			if each {
				list, _ := at[name].([]any)
				if len(list) == 0 {
					list = []any{map[string]any{}}
					at[name] = list
				}
				next = list[0].(map[string]any)
			} else if next, _ = at[name].(map[string]any); next == nil {
				next = map[string]any{}
				at[name] = next
			}
			at = next
		}
		at[last] = v
	}
	raw, _ := json.Marshal(obj)
	if len(want) == 0 {
		want = nil
	}
	return raw, want
}

// RowIDsOf reads, for each tool of the allowlist, the ids at every path the
// table above lists, arrays included, and nothing else: a field that looks
// like an id but is not declared (foo_id, a document_id inside a metadata
// map) is not taken.
func TestRowIDsOfReadsEveryDeclaredPath(t *testing.T) {
	for _, tool := range ConfinedTools() {
		paths, ok := loggedPaths[tool]
		if !ok {
			t.Errorf("%s is on the allowlist and not in the table of logged paths", tool)
			continue
		}
		args, want := argsAt(paths, map[string]any{"foo_id": "x", "metadata": map[string]any{"document_id": "y"}})
		if got := RowIDsOf(tool, args); !reflect.DeepEqual(got, want) {
			t.Errorf("RowIDsOf(%s, %s) = %v, want %v", tool, args, got, want)
		}
	}
	for tool := range loggedPaths {
		if _, ok := confinedTools[tool]; !ok {
			t.Errorf("the table lists %s, which the allowlist does not", tool)
		}
	}
}

// A tool off the allowlist logs no ids: no guess from field names. Nor do
// arguments that are not a JSON object, ids that are empty, or a row-naming
// path on a tool that does not take it.
func TestRowIDsOfTakesNothingOffTheAllowlist(t *testing.T) {
	for _, c := range []struct{ tool, args string }{
		{"graph_cypher_query", `{"query":"MATCH (n) RETURN n","document_id":"d1"}`},
		{"knowledge_memory_consolidate", `{"promotion":{"memory_ids":["m1"]}}`},
		{"knowledge_get", `not json`},
		{"knowledge_get", `{"knowledge_id":""}`},
		{"memory_get", `{"memory_ids":[]}`},
		// A row-naming data path is read only on a tool that takes it:
		// knowledge_get does not read memory_ids, memory_save no plan.
		{"knowledge_get", `{"memory_ids":["m1"]}`},
		{"memory_save", `{"plan":{"filters":{"document_ids":["d1"]}}}`},
	} {
		if got := RowIDsOf(c.tool, []byte(c.args)); got != nil {
			t.Errorf("RowIDsOf(%s, %s) = %v, want nil", c.tool, c.args, got)
		}
	}
	if got := RowIDsOf("knowledge_get", []byte(`{"Knowledge_ID":"k1"}`)); !reflect.DeepEqual(got, map[string][]string{"knowledge_id": {"k1"}}) {
		t.Errorf("another spelling, which encoding/json reads into the same field: %v, want knowledge_id k1", got)
	}
}

// rowNamingExceptions are the arguments whose name ends in _id or _ids and
// that name no row: a memory bucket, or a scope field.
var rowNamingExceptions = map[string]string{
	"session_id":              "names a session bucket, not a row",
	"plan.filters.session_id": "names a session bucket, not a row",
	"plan.filters.user_id":    "is a scope field",
}

// Every argument of the allowlist whose name says it names rows (it ends in
// _id or _ids, or it is supersedes) is logged, or is listed above with why.
// An argument added to a tool later, and classified as data, fails here
// until it is logged or excused.
func TestEveryArgumentThatNamesARowIsLogged(t *testing.T) {
	used := map[string]bool{}
	for _, tool := range ConfinedTools() {
		r := confinedTools[tool]
		candidates := append([]string(nil), r.args...)
		for _, id := range r.ids {
			candidates = append(candidates, id.path)
		}
		for _, p := range candidates {
			last := p[strings.LastIndex(p, ".")+1:]
			if !strings.HasSuffix(last, "_id") && !strings.HasSuffix(last, "_ids") && last != "supersedes" {
				continue
			}
			if _, ok := rowNamingExceptions[p]; ok {
				used[p] = true
				continue
			}
			args, want := argsAt([]string{p}, nil)
			if got := RowIDsOf(tool, args); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: %s names rows and is not logged: %v, want %v", tool, p, got, want)
			}
		}
	}
	for p := range rowNamingExceptions {
		if !used[p] {
			t.Errorf("the exception %s matches no argument of the allowlist", p)
		}
	}
}
