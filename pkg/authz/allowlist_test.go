package authz

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// schemaPaths lists every argument path a tool's input schema declares, the
// way the allowlist spells them: dotted, "[]" for the elements of an array of
// objects, and ".*" for an object the schema leaves open.
func schemaPaths(schema map[string]any) []string {
	var out []string
	var walk func(prefix string, schema map[string]any)
	walk = func(prefix string, schema map[string]any) {
		props, _ := schema["properties"].(map[string]any)
		for name, raw := range props {
			sub, _ := raw.(map[string]any)
			path := prefix + name
			switch sub["type"] {
			case "object":
				if inner, _ := sub["properties"].(map[string]any); len(inner) == 0 {
					out = append(out, path+".*")
					continue
				}
				out = append(out, path)
				walk(path+".", sub)
			case "array":
				out = append(out, path)
				if items, _ := sub["items"].(map[string]any); items["type"] == "object" {
					walk(path+"[].", items)
				}
			default:
				out = append(out, path)
			}
		}
	}
	walk("", schema)
	sort.Strings(out)
	return out
}

// Every tool the toolbox defines is either on the allowlist, with every one
// of its arguments classified, or refused to confined keys. A tool added
// later is refused with defaultRefusal until it is reviewed; an argument
// added later to an allowed tool lands in no list and fails here, so the
// policy cannot drift open.
func TestEveryToolAndEveryArgumentIsClassifiedForConfinedKeys(t *testing.T) {
	refused := RefusedTools()
	// The tools refused for a reason of their own, not the default one: each
	// is a tool that looks like a search or a read of one store.
	own := []string{"build_context", "changes_since", "cortex_query", "get_chunks",
		"graph_health", "knowledge_memory_consolidate",
		"knowledge_memory_promote_to_knowledge", "knowledge_memory_reflect", "memory_list_all",
		"sweep_expired", "vector_dimension_repair",
		"graph_list_all", "graph_cypher_query", "rank_graph_nodes", "predict_graph_edges", "disambiguate_mentions", "graph_statistics",
		"graph_history", "knowledge_graph_history", "ontology_history"}
	for _, name := range own {
		if why, ok := refused[name]; !ok || why == defaultRefusal || why == "" {
			t.Errorf("%s lost its own reason: %q", name, why)
		}
	}
	for name, why := range refused {
		if why != defaultRefusal && !slices.Contains(own, name) {
			t.Errorf("%s has a reason of its own the test does not name: %q", name, why)
		}
	}
	for _, def := range cortexdb.ToolDefinitions() {
		rule, allowed := confinedTools[def.Name]
		if _, isRefused := refused[def.Name]; allowed == isRefused {
			t.Errorf("%s: allowed %v, refused %v", def.Name, allowed, isRefused)
		}
		if !allowed {
			continue
		}
		classified := map[string]string{}
		note := func(path, as string) {
			if prev, dup := classified[path]; dup {
				t.Errorf("%s: %s is both %s and %s", def.Name, path, prev, as)
			}
			classified[path] = as
		}
		for _, f := range rule.scope {
			note(f, "scope")
		}
		for _, a := range rule.args {
			note(a, "data")
		}
		for _, id := range rule.ids {
			note(id.path, "id")
		}
		gates := map[string]bool{}
		for _, g := range rule.gates {
			if !gates[g] {
				note(g, "gate")
			}
			gates[g] = true
		}
		for p := range rule.denied {
			note(p, "denied")
		}
		// A denied argument covers everything under it: refusing
		// "entities" refuses each of its fields.
		var want []string
		seen := map[string]bool{}
		for _, w := range schemaPaths(def.InputSchema) {
			for p := range rule.denied {
				if w == p+".*" || strings.HasPrefix(w, p+".") || strings.HasPrefix(w, p+"[].") {
					w = p
				}
			}
			if !seen[w] {
				want = append(want, w)
			}
			seen[w] = true
		}
		sort.Strings(want)
		got := make([]string, 0, len(classified))
		for p := range classified {
			got = append(got, p)
		}
		sort.Strings(got)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: the allowlist classifies\n%s\nbut the schema declares\n%s", def.Name, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		for _, f := range rule.scope {
			if f != FieldUserID && f != FieldScope && f != FieldNamespace && f != FieldCollection {
				t.Errorf("%s: %s is not a scope field", def.Name, f)
			}
		}
		if rule.stores == 0 {
			t.Errorf("%s: names no store", def.Name)
		}
		// A tool with gates has one for every field it honours, and only a
		// tool of both stores has any: ConfineToolArgs forces one per
		// constraint without looking.
		if len(rule.gates) > 0 && rule.stores != storeKnowledge|storeMemory {
			t.Errorf("%s: has gates but touches one store", def.Name)
		}
		for _, f := range rule.scope {
			if len(rule.gates) > 0 && rule.gates[f] == "" {
				t.Errorf("%s: honours %s but has no gate for it", def.Name, f)
			}
		}
	}
	for name := range refused {
		if _, ok := LookupTool(name); !ok {
			t.Errorf("%s is refused but is not a tool", name)
		}
	}
	for name, reason := range refused {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s is refused without a reason", name)
		}
	}
}

func TestAConfinedKeyIsRefusedAToolOffTheAllowlist(t *testing.T) {
	hermes := Key{ID: "hermes", Secret: "h", Clearance: ReadWrite, Scope: Scope{UserID: "hermes"}}
	notes := Key{ID: "notes", Secret: "n", Clearance: ReadWrite, Scope: Scope{Collection: "notes"}}
	for _, c := range []struct {
		key        Key
		tool, args string
		want       string
	}{
		// Reads memories and writes documents: no single confinement covers it.
		{hermes, "knowledge_memory_consolidate", `{"user_id":"hermes","promotion":{"memory_ids":["zeus-1"]}}`, "not a tool a confined key may call"},
		// Its graph prefetch searches chunks of every collection.
		{notes, "cortex_query", `{"query":"q","collection":"notes","entity_names":["Orpheus"]}`, "not a tool a confined key may call"},
		{notes, "graph_statistics", `{}`, "not a tool a confined key may call"},
		{notes, "get_chunks", `{"chunk_ids":["chunk:x:000"]}`, "not a tool a confined key may call"},
		{notes, "no_such_tool", `{"collection":"notes"}`, "not a tool this server defines"},
	} {
		err := c.key.AuthorizeToolArgs(c.tool, []byte(c.args))
		if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s for %s: %v, want a denial saying %q", c.tool, c.key.ID, err, c.want)
		}
	}
	// The reason travels with the refusal, so the operator reading the log
	// knows why the tool is off the list.
	err := hermes.AuthorizeToolArgs("knowledge_memory_consolidate", []byte(`{"user_id":"hermes"}`))
	if err == nil || !strings.Contains(err.Error(), RefusedTools()["knowledge_memory_consolidate"]) {
		t.Fatalf("the refusal does not carry its reason: %v", err)
	}
	// Unconfined keys never enter the check.
	root := Key{ID: "root", Secret: "s", Clearance: ReadWrite}
	if err := root.AuthorizeToolArgs("graph_statistics", nil); err != nil {
		t.Fatalf("an unconfined key was refused a tool: %v", err)
	}
}

// An argument the allowlist does not cover is refused, whatever it holds:
// the fields that let a confined key reach outside its scope were all
// arguments the policy did not know about.
func TestAConfinedKeyIsRefusedAnArgumentTheAllowlistDoesNotCover(t *testing.T) {
	hermes := Key{ID: "hermes", Secret: "h", Clearance: ReadWrite, Scope: Scope{UserID: "hermes"}}
	notes := Key{ID: "notes", Secret: "n", Clearance: ReadWrite, Scope: Scope{Collection: "notes"}}
	for _, c := range []struct {
		key        Key
		tool, args string
		want       string
	}{
		// A session named by its raw bucket id reads another user's memories.
		// A denied argument's refusal carries its reason.
		{notes, "extract_conversation", `{"collection":"notes","session_id":"memory:user:zeus:default"}`, "session_id from a confined key: it names a memory bucket"},
		// Persisting derives a document id the guard cannot see.
		{notes, "extract_conversation", `{"collection":"notes","text":"Orpheus met Eurydice.","persist":true}`, "persist from a confined key: it writes"},
		// Declared entities and relations write the shared graph.
		{notes, "knowledge_save", `{"knowledge_id":"n1","collection":"notes","content":"x","entities":[{"name":"Orpheus","chunk_ids":["chunk:hr-salaries-2026:000"]}]}`, "entities from a confined key: it declares entities"},
		{notes, "knowledge_update", `{"knowledge_id":"n1","collection":"notes","relations":[{"from":"a","to":"b","support_edge_ids":["e1"]}]}`, "relations from a confined key: it declares relations"},
		{hermes, "memory_save", `{"memory_id":"h1","user_id":"hermes","content":"x","relations":[{"from":"a","to":"b","chunk_ids":["chunk:secret:000"]}]}`, "relations from a confined key: it declares relations"},
		// A field the schema does not declare today is refused too, so a
		// field added tomorrow is refused until the allowlist names it.
		{hermes, "memory_search", `{"query":"q","user_id":"hermes","owner_override":"zeus"}`, "owner_override"},
		{hermes, "memory_search", `{"query":"q","user_id":"hermes","plan":{"filters":{"user_id":"hermes","collection_override":"secrets"}}}`, "plan.filters.collection_override"},
		// An object inside a list of ids is a path the allowlist does not
		// name, whatever its keys.
		{hermes, "memory_get", `{"memory_ids":["hermes-1",{"memory_id":"zeus-1"}]}`, "memory_ids[].memory_id"},
		// Spelling does not matter to encoding/json, so it does not here.
		{notes, "extract_conversation", `{"collection":"notes","SESSION_ID":"memory:user:zeus:default"}`, "session_id from a confined key"},
	} {
		err := c.key.AuthorizeToolArgs(c.tool, []byte(c.args))
		if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: %v, want a denial naming %s", c.key.ID, c.tool, err, c.want)
		}
	}
	// The same calls without the offending argument pass.
	for _, c := range []struct {
		key        Key
		tool, args string
	}{
		{notes, "extract_conversation", `{"collection":"notes","text":"Orpheus met Eurydice.","max_entities":5}`},
		{notes, "knowledge_save", `{"knowledge_id":"n1","collection":"notes","content":"x","metadata":{"any":"thing","nested":{"deep":1}}}`},
		{hermes, "memory_save", `{"memory_id":"h1","user_id":"hermes","content":"x","supersedes":["h0"],"metadata":{"k":"v"}}`},
		{hermes, "memory_search", `{"query":"q","user_id":"hermes","plan":{"filters":{"user_id":"hermes","document_ids":["d1"]},"keywords":["q"]},"ppr":{"damping":0.8}}`},
	} {
		if err := c.key.AuthorizeToolArgs(c.tool, []byte(c.args)); err != nil {
			t.Errorf("%s %s was refused: %v", c.key.ID, c.tool, err)
		}
	}
}

// Recall reads both stores. A key confined to a collection reaches no memory
// and a key confined to a user reaches no document, as on the typed API, so
// the call passes only with the other store switched off. ConfineToolArgs is
// what a port uses to switch it off before the call.
func TestAToolThatReadsBothStoresIsGatedForAConfinedKey(t *testing.T) {
	hermes := Key{ID: "hermes", Secret: "h", Clearance: ReadWrite, Scope: Scope{UserID: "hermes"}}
	notes := Key{ID: "notes", Secret: "n", Clearance: ReadWrite, Scope: Scope{Collection: "notes"}}
	for _, tool := range []string{"knowledge_memory_recall", "knowledge_memory_build_context_pack"} {
		err := notes.AuthorizeToolArgs(tool, []byte(`{"query":"q","collection":"notes","user_id":"zeus"}`))
		if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "disable_memory") {
			t.Fatalf("%s: a collection key reached memory: %v", tool, err)
		}
		err = notes.AuthorizeToolArgs(tool, []byte(`{"query":"q","collection":"notes","disable_memory":false}`))
		if !errors.Is(err, ErrDenied) {
			t.Fatalf("%s: disable_memory=false passed: %v", tool, err)
		}
		if err := notes.AuthorizeToolArgs(tool, []byte(`{"query":"q","collection":"notes","disable_memory":true}`)); err != nil {
			t.Fatalf("%s: a collection key refused knowledge alone: %v", tool, err)
		}
		// The gate counts under any spelling, as encoding/json reads it, and
		// every spelling sent must switch the store off: which of two
		// spellings the decoder keeps is not the one the check would pick.
		if err := notes.AuthorizeToolArgs(tool, []byte(`{"query":"q","collection":"notes","DISABLE_MEMORY":true}`)); err != nil {
			t.Fatalf("%s: a differently spelled gate was not seen: %v", tool, err)
		}
		for _, args := range []string{
			`{"query":"q","collection":"notes","disable_memory":true,"Disable_Memory":false}`,
			`{"query":"q","collection":"notes","Disable_Memory":false,"disable_memory":true}`,
		} {
			if err := notes.AuthorizeToolArgs(tool, []byte(args)); !errors.Is(err, ErrDenied) {
				t.Fatalf("%s: a gate switched off under another spelling passed: %v", tool, err)
			}
		}
		err = hermes.AuthorizeToolArgs(tool, []byte(`{"query":"q","user_id":"hermes"}`))
		if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "disable_knowledge") {
			t.Fatalf("%s: a user key reached every collection: %v", tool, err)
		}
		if err := hermes.AuthorizeToolArgs(tool, []byte(`{"query":"q","user_id":"hermes","disable_knowledge":true}`)); err != nil {
			t.Fatalf("%s: a user key refused memory alone: %v", tool, err)
		}
	}

	confined := notes.ConfineToolArgs("knowledge_memory_recall", []byte(`{"query":"q","collection":"notes","DISABLE_MEMORY":false}`))
	var got map[string]any
	if err := json.Unmarshal(confined, &got); err != nil {
		t.Fatal(err)
	}
	if got["disable_memory"] != true || got["DISABLE_MEMORY"] != nil || got["query"] != "q" {
		t.Fatalf("confined arguments: %s", confined)
	}
	if err := notes.AuthorizeToolArgs("knowledge_memory_recall", confined); err != nil {
		t.Fatalf("confined arguments were refused: %v", err)
	}
	// Nothing to force, or nothing that can be: the arguments come back as
	// they were, and AuthorizeToolArgs refuses the ones that are not an object.
	for _, c := range []struct {
		key        Key
		tool, args string
	}{
		{Key{ID: "root"}, "knowledge_memory_recall", `{"query":"q"}`},
		{notes, "knowledge_search", `{"query":"q","collection":"notes"}`},
		{notes, "no_such_tool", `{"query":"q"}`},
		{notes, "knowledge_memory_recall", `[1]`},
		{notes, "knowledge_memory_recall", `null`},
	} {
		if out := c.key.ConfineToolArgs(c.tool, []byte(c.args)); string(out) != c.args {
			t.Errorf("%s %s: %s, want the arguments unchanged", c.key.ID, c.tool, out)
		}
	}
	for _, args := range []string{`[1]`, `null`, ` null `} {
		if err := notes.AuthorizeToolArgs("knowledge_memory_recall", notes.ConfineToolArgs("knowledge_memory_recall", []byte(args))); !errors.Is(err, ErrDenied) {
			t.Fatalf("%s passed through confinement: %v", args, err)
		}
	}
}

// The scope rule is unchanged by the allowlist: the field must be set, equal
// the key's value, and so must every nested copy under any spelling. A tool
// addressed only by id has no scope field, and the row guard decides.
func TestTheScopeRuleStillHoldsOnTheAllowlist(t *testing.T) {
	hermes := Key{ID: "hermes", Secret: "h", Clearance: ReadWrite, Scope: Scope{UserID: "hermes"}}
	notes := Key{ID: "notes", Secret: "n", Clearance: ReadWrite, Scope: Scope{Collection: "notes"}}
	for _, c := range []struct {
		key        Key
		tool, args string
		want       string
	}{
		{hermes, "memory_search", `{"query":"q","user_id":"zeus"}`, `asks for "zeus"`},
		{hermes, "memory_search", `{"query":"q"}`, "leaves user_id unset"},
		{hermes, "memory_search", `{"query":"q","user_id":"hermes","USER_ID":"zeus"}`, `"zeus"`},
		{hermes, "knowledge_memory_recall", `{"query":"q","user_id":"hermes","disable_knowledge":true,"plan":{"filters":{"USER_ID":"zeus"}}}`, `nested user_id="zeus"`},
		{notes, "knowledge_search", `{"query":"q","collection":"secrets"}`, `asks for "secrets"`},
		{notes, "memory_search", `{"query":"q","user_id":"hermes"}`, "takes no collection argument"},
		{hermes, "ingest_document", `{"document_id":"d","content":"x","collection":"notes"}`, "takes no user_id argument"},
	} {
		err := c.key.AuthorizeToolArgs(c.tool, []byte(c.args))
		if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: %v, want %q", c.key.ID, c.tool, err, c.want)
		}
	}
	for _, c := range []struct {
		key        Key
		tool, args string
	}{
		{hermes, "memory_search", `{"query":"q","user_id":"hermes","User_Id":"hermes"}`},
		{hermes, "memory_get", `{"memory_ids":["h-1"]}`},
		{hermes, "memory_update", `{"memory_id":"h-1","content":"y"}`},
		{notes, "knowledge_get", `{"knowledge_id":"n1"}`},
		{notes, "knowledge_delete", `{"knowledge_id":"n1"}`},
	} {
		if err := c.key.AuthorizeToolArgs(c.tool, []byte(c.args)); err != nil {
			t.Errorf("%s %s was refused: %v", c.key.ID, c.tool, err)
		}
	}
	for _, bad := range []string{`[1,2]`, `"text"`, `{"a":`} {
		if err := hermes.AuthorizeToolArgs("memory_search", []byte(bad)); !errors.Is(err, ErrDenied) {
			t.Errorf("%s was accepted as tool arguments: %v", bad, err)
		}
	}
	// No arguments is an empty object, and a confined key then fails on the
	// unset field rather than on the parse.
	err := hermes.AuthorizeToolArgs("memory_search", nil)
	if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "leaves user_id unset") {
		t.Fatalf("no arguments: %v", err)
	}
	// A nested copy is looked for jsonScanDepth object levels down, inside
	// an open object such as metadata, and no deeper: the bound is what keeps
	// an attacker's arguments from turning the check into an unbounded walk.
	err = hermes.AuthorizeToolArgs("memory_save", []byte(`{"memory_id":"h1","user_id":"hermes","content":"x","metadata":{"a":{"b":{"user_id":"zeus"}}}}`))
	if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), `nested user_id="zeus"`) {
		t.Fatalf("a copy %d levels down was not seen: %v", jsonScanDepth-1, err)
	}
	if err := hermes.AuthorizeToolArgs("memory_save", []byte(`{"memory_id":"h1","user_id":"hermes","content":"x","metadata":{"a":{"b":{"c":{"d":{"user_id":"zeus"}}}}}}`)); err != nil {
		t.Fatalf("the walk went past its bound of %d levels: %v", jsonScanDepth, err)
	}
}

// The table a reviewer reads is generated from the allowlist, so the two
// cannot disagree.
func TestTheAllowlistTableNamesEveryToolAndEveryIDField(t *testing.T) {
	table := ConfinedToolsTable()
	for name, rule := range confinedTools {
		if !strings.Contains(table, "`"+name+"`") {
			t.Errorf("the table does not name %s", name)
		}
		for _, id := range rule.ids {
			if !strings.Contains(table, id.path) {
				t.Errorf("the table does not name %s of %s", id.path, name)
			}
		}
	}
	for name := range RefusedTools() {
		if !strings.Contains(table, "`"+name+"`") {
			t.Errorf("the table does not name the refused %s", name)
		}
	}
	// Three rows, read as a reviewer reads them: the stores, the kind of
	// each id, the gates and a tool reached by id only.
	for _, row := range []string{
		"| `knowledge_get` | (by id only) | knowledge | `knowledge_id` (knowledge) |  | `knowledge_id` with `knowledge_ids` |",
		"| `knowledge_memory_recall` | user_id, scope, namespace, collection | knowledge and memory |  | collection → `disable_memory=true`, user_id → `disable_knowledge=true`, scope → `disable_knowledge=true`, namespace → `disable_knowledge=true` |  |",
		"| `memory_save` | user_id, scope, namespace | memory | `memory_id` (memory), `supersedes` (memory) |  | `entities`, `relations` |",
		"| `memory_get` | (by id only) | memory | `memory_id` (memory) |  | `memory_id` with `memory_ids` |",
	} {
		if !strings.Contains(table, row) {
			t.Errorf("the table lacks the row %s", row)
		}
	}
	t.Log("\n" + table)
}

// An entity or a relation a caller declares lands on a node or an edge of
// the shared graph, keyed by name across every collection: a confined key
// could rewrite an entity of another collection, add edges to it, or change
// the collections it is known in, which the walk gate reads. So entities and
// relations are refused on every tool that takes them. The extraction the
// engine runs on the saved text stays: it adds the key's collection to an
// entity and keeps the rest. A document's metadata takes none of the keys
// the engine writes on every chunk, and none that starts with "_".
func TestAConfinedKeyDeclaresNoEntityOrRelationAndNoEngineKey(t *testing.T) {
	notes := Key{ID: "notes", Scope: Scope{Collection: "notes"}}
	hermes := Key{ID: "hermes", Scope: Scope{UserID: "hermes"}}
	for _, c := range []struct {
		key  Key
		tool string
		args string
		want string
	}{
		{notes, "knowledge_save", `{"knowledge_id":"n1","collection":"notes","content":"x","entities":[{"name":"Project Nightshade","description":"defaced"}]}`, "entities"},
		{notes, "knowledge_save", `{"knowledge_id":"n1","collection":"notes","content":"x","relations":[{"from":"Project Nightshade","to":"Orpheus","type":"funds"}]}`, "relations"},
		{notes, "knowledge_update", `{"knowledge_id":"n1","collection":"notes","Entities":[]}`, "entities"},
		{notes, "knowledge_update", `{"knowledge_id":"n1","collection":"notes","relations":[]}`, "relations"},
		{hermes, "memory_save", `{"memory_id":"m1","user_id":"hermes","content":"x","entities":[{"name":"Acme","metadata":{"collections":""}}]}`, "entities"},
		{hermes, "memory_save", `{"memory_id":"m1","user_id":"hermes","content":"x","relations":[{"from":"a","to":"b"}]}`, "relations"},
		{notes, "ingest_document", `{"document_id":"n1","collection":"notes","content":"x","metadata":{"document_id":"hr"}}`, "metadata.document_id"},
		{notes, "knowledge_save", `{"knowledge_id":"n1","collection":"notes","content":"x","metadata":{"Chunk_Index":"0"}}`, "metadata.chunk_index"},
		{notes, "knowledge_update", `{"knowledge_id":"n1","collection":"notes","metadata":{"graph_kind":"entity"}}`, "metadata.graph_kind"},
		{hermes, "memory_save", `{"memory_id":"m1","user_id":"hermes","content":"x","metadata":{"_anything":"x"}}`, "metadata._anything"},
		// Who wrote a row is the engine's to record (cortexdb.WithWriter):
		// the facade replaces the exact spelling only, so another spelling
		// would sign a row as another key.
		{notes, "ingest_document", `{"document_id":"n1","collection":"notes","content":"x","metadata":{"Written_By":"zeus-key"}}`, "metadata.written_by"},
		{notes, "knowledge_update", `{"knowledge_id":"n1","collection":"notes","metadata":{"Updated_By":"zeus-key"}}`, "metadata.updated_by"},
		{hermes, "memory_update", `{"memory_id":"m1","metadata":{"written_by":"zeus-key"}}`, "metadata.written_by"},
	} {
		err := c.key.AuthorizeToolArgs(c.tool, []byte(c.args))
		if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: %v, want a denial naming %s", c.key.ID, c.tool, err, c.want)
		}
	}
	for _, c := range []struct {
		key        Key
		tool, args string
	}{
		{notes, "knowledge_save", `{"knowledge_id":"n1","collection":"notes","content":"Project Nightshade is a name I heard.","metadata":{"color":"blue"}}`},
		{hermes, "memory_save", `{"memory_id":"m1","user_id":"hermes","content":"x","metadata":{"document_id":"a reference of mine"}}`},
		{notes, "aggregate_metadata", `{"collection":"notes","type":"group_by","group_by":["document_id"],"filters":{"document_id":"n1"}}`},
	} {
		if err := c.key.AuthorizeToolArgs(c.tool, []byte(c.args)); err != nil {
			t.Errorf("%s %s: the caller's own data was refused: %v", c.key.ID, c.tool, err)
		}
	}
}

// The facade signs every write of a document (ingest_document,
// knowledge_save, knowledge_update): written_by and updated_by are replaced
// from the key. So the three document tools take them back under their exact
// spelling, as a document read back carries them. Another spelling is not
// replaced and stays refused, and so does a memory tool, which this rule
// does not reach.
func TestTheDocumentToolsTakeTheSignatureBack(t *testing.T) {
	notes := Key{ID: "notes", Scope: Scope{Collection: "notes"}}
	hermes := Key{ID: "hermes", Scope: Scope{UserID: "hermes"}}
	for _, tool := range []string{"ingest_document", "knowledge_save", "knowledge_update"} {
		id := "knowledge_id"
		if tool == "ingest_document" {
			id = "document_id"
		}
		args := `{"` + id + `":"n1","collection":"notes","content":"x","metadata":{"written_by":"zeus-key","updated_by":"zeus-key"}}`
		if err := notes.AuthorizeToolArgs(tool, []byte(args)); err != nil {
			t.Errorf("%s with the signature it read: %v, want it taken", tool, err)
		}
		for _, spelling := range []string{"Written_By", "UPDATED_BY"} {
			args := strings.Replace(args, `"written_by":"zeus-key","updated_by":"zeus-key"`, `"`+spelling+`":"zeus-key"`, 1)
			if err := notes.AuthorizeToolArgs(tool, []byte(args)); !errors.Is(err, ErrDenied) {
				t.Errorf("%s with %s: %v, want a denial", tool, spelling, err)
			}
		}
	}
	for _, c := range []struct{ tool, args string }{
		{"memory_save", `{"memory_id":"m1","user_id":"hermes","content":"x","metadata":{"written_by":"zeus-key"}}`},
		{"knowledge_memory_remember", `{"memory_id":"m1","user_id":"hermes","content":"x","metadata":{"written_by":"zeus-key"}}`},
		{"memory_update", `{"memory_id":"m1","metadata":{"updated_by":"zeus-key"}}`},
	} {
		if err := hermes.AuthorizeToolArgs(c.tool, []byte(c.args)); !errors.Is(err, ErrDenied) {
			t.Errorf("%s with a signature: %v, want a denial", c.tool, err)
		}
	}
}

// return_paths runs path retrieval over the shared graph with no collection
// (attachGraphRAGPaths calls SearchPaths), the walk search_paths does and a
// confined key is refused. So the path arguments of search_graphrag_lexical
// are refused, and the search itself, whose walk keeps to the collection,
// stays.
func TestPathRetrievalIsRefusedToAConfinedKey(t *testing.T) {
	notes := Key{ID: "notes", Scope: Scope{Collection: "notes"}}
	for _, arg := range []string{`"return_paths":true`, `"max_paths":3`, `"Path_Max_Depth":2`, `"relation_policies":{"*":{"weight":1}}`} {
		err := notes.AuthorizeToolArgs("search_graphrag_lexical", []byte(`{"collection":"notes","entity_names":["Project Nightshade"],`+arg+`}`))
		if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "path") && !strings.Contains(err.Error(), "relation_policies") {
			t.Errorf("%s: %v, want a denial", arg, err)
		}
	}
	if err := notes.AuthorizeToolArgs("search_graphrag_lexical", []byte(`{"collection":"notes","entity_names":["Project Nightshade"],"max_hops":2}`)); err != nil {
		t.Errorf("the walk confined to the collection was refused: %v", err)
	}
}

// memory_get reads memory_ids and ignores memory_id when both are set, while
// the row guard reads memory_id: a foreign memory_id beside a list of the
// caller's would answer "not found", a missing one would serve the list, and
// the pair would tell them apart. A confined key sends one form or the other.
func TestMemoryGetTakesOneFormFromAConfinedKey(t *testing.T) {
	hermes := Key{ID: "hermes", Scope: Scope{UserID: "hermes"}}
	err := hermes.AuthorizeToolArgs("memory_get", []byte(`{"memory_id":"zeus-1","Memory_IDs":["hermes-1"]}`))
	if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "memory_id") || !strings.Contains(err.Error(), "memory_ids") {
		t.Fatalf("both forms of memory_get: %v, want a denial naming both", err)
	}
	for _, args := range []string{`{"memory_id":"hermes-1"}`, `{"memory_ids":["hermes-1","hermes-2"]}`} {
		if err := hermes.AuthorizeToolArgs("memory_get", []byte(args)); err != nil {
			t.Errorf("%s: one form was refused: %v", args, err)
		}
	}
}

// A filter reads; it does not write. So a filter map (filters.*, filter.*)
// may name an engine key: a curator lists what waits with
// aggregate_metadata on _review_status, and the gate reads _expires_at. A
// metadata map still takes no engine key the facade does not rewrite, such
// as importflow's _table, from a confined key.
func TestAFilterMayNameAnEngineKey(t *testing.T) {
	notes := Key{ID: "notes", Scope: Scope{Collection: "notes"}}
	for _, c := range []struct{ tool, args string }{
		{"aggregate_metadata", `{"collection":"notes","type":"count","filters":{"_review_status":"proposed"}}`},
		{"representative_records", `{"collection":"notes","group_by":"source","filter":{"_review_status":"proposed"}}`},
		{"search_vector_range", `{"collection":"notes","query":"x","radius":0.5,"filter":{"_expires_at":"2031-01-01T00:00:00Z"}}`},
	} {
		if err := notes.AuthorizeToolArgs(c.tool, []byte(c.args)); err != nil {
			t.Errorf("%s: a filter on an engine key was refused: %v", c.tool, err)
		}
	}
	err := notes.AuthorizeToolArgs("knowledge_save", []byte(`{"knowledge_id":"n1","collection":"notes","content":"x","metadata":{"_table":"t"}}`))
	if !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "metadata._table") {
		t.Errorf("an engine key in metadata was not refused: %v", err)
	}
}

// A copy of the scope field inside a list is read like one inside an object,
// and the list does not spend the depth bound: a list of objects sits at the
// nesting level of one object.
func TestACopyOfTheScopeFieldInsideAListIsRead(t *testing.T) {
	notes := Key{ID: "notes", Secret: "n", Clearance: ReadWrite, Scope: Scope{Collection: "notes"}}
	for _, md := range []string{
		`{"a":[{"collection":"secrets"}]}`,
		`{"a":{"b":{"c":[{"collection":"secrets"}]}}}`,
	} {
		args := `{"knowledge_id":"n1","collection":"notes","content":"x","metadata":` + md + `}`
		if err := notes.AuthorizeToolArgs("knowledge_save", []byte(args)); err == nil {
			t.Errorf("a copy of collection in %s passed", md)
		}
		same := strings.ReplaceAll(args, "secrets", "notes")
		if err := notes.AuthorizeToolArgs("knowledge_save", []byte(same)); err != nil {
			t.Errorf("an equal copy in %s was refused: %v", md, err)
		}
	}
}

// The marks the facade rewrites on every document write pass in the
// metadata of the three tools that write a document, under their exact
// spelling; another spelling, another engine key and a memory write keep
// the refusal of a "_" key.
func TestTheRewrittenMarksPassOnTheDocumentWritesOnly(t *testing.T) {
	notes := Key{ID: "notes", Clearance: ReadWrite, Scope: Scope{Collection: "notes"}}
	for _, tool := range []string{"ingest_document", "knowledge_save", "knowledge_update"} {
		for _, mark := range cortexdb.EngineDocumentKeys() {
			args := `{"collection":"notes","content":"c","metadata":{"` + mark + `":"x"}}`
			if err := notes.AuthorizeToolArgs(tool, notes.ConfineToolArgs(tool, []byte(args))); err != nil {
				t.Errorf("%s refused the mark %s: %v", tool, mark, err)
			}
		}
		for _, key := range []string{strings.ToUpper(cortexdb.EngineDocumentKeys()[0]), "_table"} {
			args := `{"collection":"notes","content":"c","metadata":{"` + key + `":"x"}}`
			if err := notes.AuthorizeToolArgs(tool, notes.ConfineToolArgs(tool, []byte(args))); !errors.Is(err, ErrDenied) {
				t.Errorf("%s took %s: %v", tool, key, err)
			}
		}
	}
	hermes := Key{ID: "hermes", Clearance: ReadWrite, Scope: Scope{UserID: "hermes"}}
	if err := hermes.AuthorizeToolArgs("memory_save", hermes.ConfineToolArgs("memory_save", []byte(`{"user_id":"hermes","content":"c","metadata":{"team":"x"}}`))); err != nil {
		t.Fatalf("memory_save refused plain metadata, so its refusal below proves nothing: %v", err)
	}
	// A memory write takes back the review keys, which the facade rewrites
	// on a memory too, and none of a document's other marks.
	for _, tool := range []string{"memory_save", "knowledge_memory_remember"} {
		for _, mark := range cortexdb.EngineDocumentKeys() {
			args := `{"user_id":"hermes","content":"c","metadata":{"` + mark + `":"x"}}`
			err := hermes.AuthorizeToolArgs(tool, hermes.ConfineToolArgs(tool, []byte(args)))
			if slices.Contains(cortexdb.EngineMemoryKeys(), mark) {
				if err != nil {
					t.Errorf("%s refused the review key %s: %v", tool, mark, err)
				}
			} else if !errors.Is(err, ErrDenied) {
				t.Errorf("%s took the mark %s: %v", tool, mark, err)
			}
		}
	}
}
