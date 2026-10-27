package authz

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// The toolbox a confined key may use, one rule per tool, written out by hand.
//
// An earlier version derived the rule from each tool's input schema: a tool
// that declared `collection` was taken to honour it. That held for the tools
// that read one store and name rows under one field, and failed for the rest.
// knowledge_memory_consolidate declares user_id and also carries, under
// `promotion`, the ids of memories to promote and of a document to overwrite;
// knowledge_memory_recall declares both user_id and collection and reads both
// stores whichever one the key is confined on; memory_save declares user_id
// and retires the memories named under `supersedes`. None of that is visible
// in a top-level field list. So the rule is explicit now: for each tool, which
// scope fields it honours, which stores it reads or writes, which arguments
// carry plain data, which carry row ids and of what kind, and which arguments
// a confined key may not send at all. An argument that is in none of those
// lists is refused, so a field added to a tool later is refused until someone
// classifies it. A tool that is on neither list is refused as well. The test
// TestEveryToolAndEveryArgumentIsClassifiedForConfinedKeys holds the lists to
// the schemas, both ways.
//
// Paths are spelled as the arguments are nested: `plan.filters.document_ids`,
// `[]` for the elements of an array (`memory_ids[].x` is a field of every
// element), and `metadata.*` for an object the schema leaves open, whose
// contents are data.

// store is what a tool touches: documents and their chunks, confined by
// collection, or memories, confined by user_id, scope and namespace.
type store uint8

const (
	storeKnowledge store = 1 << iota
	storeMemory
)

func (s store) String() string {
	var names []string
	if s&storeKnowledge != 0 {
		names = append(names, "knowledge")
	}
	if s&storeMemory != 0 {
		names = append(names, "memory")
	}
	return strings.Join(names, " and ")
}

// confinedStore is the store a scope field confines: a collection confines
// knowledge, the three memory fields confine memory.
func confinedStore(field string) store {
	if field == FieldCollection {
		return storeKnowledge
	}
	return storeMemory
}

// idKind says what row a path names, so the guard knows which row to load and
// which rule to hold it to.
type idKind uint8

const (
	idKnowledge idKind = iota // a document: its collection must be permitted
	idMemory                  // a memory: its user_id, scope and namespace must be
)

func (k idKind) String() string {
	if k == idMemory {
		return "memory"
	}
	return "knowledge"
}

type idField struct {
	path string
	kind idKind
}

// toolRule is one line of the allowlist.
type toolRule struct {
	// scope lists the scope fields the tool honours at its top level. A key
	// confined on a field the tool does not honour is refused the tool; a tool
	// with no scope field is reachable only through the rows it names by id.
	scope []string
	// stores is what the tool reads or writes. A key confined on one store may
	// call a tool that touches the other only through a gate.
	stores store
	// args are the arguments that carry data, by path.
	args []string
	// ids are the arguments that name rows, by path, with the row's kind.
	ids []idField
	// gates names, per scope field, the boolean argument that switches off
	// the store the field does not confine. A port forces it true before
	// the call (ConfineToolArgs); the check refuses a call where it is not.
	gates map[string]string
	// denied are arguments a confined key may never send, with why.
	denied map[string]string
	// oneOf are top-level arguments of which a confined key may send one at
	// most: the tool reads one and ignores the other, and the guard must
	// read the one the tool reads.
	oneOf []string
	// facade marks a tool whose stores the facade holds to the caller scope
	// itself (cortexdb.CallerScope, set by Key.CallerContext): it has no
	// scope field to force and needs no gate, whatever field the key is
	// confined on.
	facade bool
	// rewritten lists, for a tool whose facade writes its own keys over the
	// caller's (documentKeys on a document, cortexdb.EngineMemoryKeys on a
	// memory), those keys: a row read back and sent again carries them, and
	// they are not refused under their exact spelling.
	rewritten func() []string
}

// Argument fragments shared by several tools.
var (
	// plan.filters.document_ids is data, not a row id: it pins a search to
	// documents, and the collection filter, forced by the scope check,
	// decides what the pin finds. A document of another collection matches
	// nothing, exactly as a document that does not exist. Loading it here and
	// refusing it would tell the two apart.
	planArgs = []string{"plan", "plan.query", "plan.keywords", "plan.alternate_queries", "plan.entity_names", "plan.retrieval_mode",
		"plan.filters", "plan.filters.collection", "plan.filters.user_id", "plan.filters.scope", "plan.filters.namespace", "plan.filters.session_id", "plan.filters.document_ids",
		"plan.filters.review_status"}
	pprArgs    = []string{"ppr", "ppr.damping", "ppr.fusion", "ppr.passage_seed_weight"}
	queryArgs  = []string{"query", "keywords", "alternate_queries", "retrieval_mode", "disable_graph", "graph_light", "max_entities_per_chunk"}
	walkArgs   = []string{"entity_names", "chunk_window", "max_context_chars", "max_context_chunks", "per_document_limit", "max_related_chunks", "max_expansion_seeds", "max_hops", "max_traversal_nodes"}
	recallArgs = []string{"top_k_knowledge", "top_k_memories", "max_memory_chars", "max_memory_items", "session_id"}
	// The review fields are data: the facade takes the calling key as the
	// reviewer and refuses another name, so a decision names who made it.
	reviewArgs = []string{"review_status", "reviewed_by", "reviewed_at"}
	memoryArgs = []string{"content", "role", "importance", "ttl_seconds", "session_id", "metadata.*"}
	docArgs    = paths([]string{"content", "title", "source_url", "author", "metadata.*", "chunk_size", "chunk_overlap", "expires_at"}, reviewArgs)
	// An entity or a relation the caller declares is a node or an edge of
	// the shared graph, keyed by name across every collection, so declaring
	// one rewrites what every collection sees: its description, its
	// properties, the collections it is known in (which the walk gate
	// reads), the edges from it. The extraction the engine runs on the saved
	// text stays: it adds the key's collection to an entity and keeps the
	// rest (cortexdb.mergeEntityCollections).
	graphDenied = map[string]string{
		"entities":  "declares entities of the shared graph, which every collection reads and no scope field confines",
		"relations": "declares relations of the shared graph, which every collection reads and no scope field confines",
	}
	// engineChunkKeys are the metadata keys the engine writes on every chunk
	// of a document. A document's metadata is copied over them, so a
	// confined key naming one would claim its chunks for another document or
	// change their kind and order; aggregates group by them.
	engineChunkKeys = map[string]string{
		"document_id": "is the document the engine records on each chunk",
		"chunk_index": "is the position the engine records on each chunk",
		"graph_kind":  "is the kind the engine records on each chunk",
	}
	// pathDenied are the arguments that run path retrieval over the shared graph with no collection
	// (cortexdb.attachGraphRAGPaths calls SearchPaths), the walk
	// search_paths does.
	// writerKeys record who wrote a row (cortexdb.WithWriter). The facade
	// replaces them under their exact spelling only, so a confined key naming
	// one in another spelling could sign a row as another key. The document
	// tools take the exact spelling back (documentKeys).
	writerKeys = map[string]string{
		"written_by": "records who wrote the row, which the engine sets from the key",
		"updated_by": "records who updated the row, which the engine sets from the key",
	}
	pathDenied = map[string]string{
		"return_paths":      "runs path retrieval over the shared graph with no collection, as search_paths does",
		"max_paths":         "bounds path retrieval, which a confined key may not run",
		"path_max_depth":    "bounds path retrieval, which a confined key may not run",
		"relation_policies": "weights path retrieval, which a confined key may not run",
	}
	recallGates = map[string]string{FieldCollection: "disable_memory", FieldUserID: "disable_knowledge", FieldScope: "disable_knowledge", FieldNamespace: "disable_knowledge"}

	collectionScope = []string{FieldCollection}
	memoryScope     = []string{FieldUserID, FieldScope, FieldNamespace}
	bothScopes      = []string{FieldUserID, FieldScope, FieldNamespace, FieldCollection}
)

// documentKeys are the metadata keys the facade rewrites on every write of a
// document: the engine's marks, expiry and review decision, and the
// signature of the key that writes.
func documentKeys() []string {
	return append(cortexdb.EngineDocumentKeys(), cortexdb.WriterKeys()...)
}

func paths(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// confinedTools is the allowlist.
var confinedTools = map[string]toolRule{
	// Knowledge: one collection, named at the top level.
	"ingest_document": {scope: collectionScope, stores: storeKnowledge, rewritten: documentKeys,
		args: paths(docArgs, []string{"dedupe"}), ids: []idField{{"document_id", idKnowledge}}},
	"knowledge_save": {scope: collectionScope, stores: storeKnowledge, rewritten: documentKeys,
		args: docArgs, ids: []idField{{"knowledge_id", idKnowledge}}, denied: graphDenied},
	"knowledge_update": {scope: collectionScope, stores: storeKnowledge, rewritten: documentKeys,
		args: docArgs, ids: []idField{{"knowledge_id", idKnowledge}}, denied: graphDenied},
	"knowledge_search": {scope: collectionScope, stores: storeKnowledge,
		args: paths(queryArgs, walkArgs, planArgs, pprArgs, []string{"top_k", "diversity_lambda"})},
	"search_text": {scope: collectionScope, stores: storeKnowledge,
		args: paths(queryArgs, planArgs, []string{"top_k", "threshold"})},
	"search_graphrag_lexical": {scope: collectionScope, stores: storeKnowledge,
		args: paths(queryArgs, walkArgs, planArgs, []string{"top_k", "diversity_lambda", "disable_rerank"}), denied: pathDenied},
	"search_vector_range": {scope: collectionScope, stores: storeKnowledge,
		args: []string{"query", "radius", "max_results", "filter.*"}},
	"aggregate_metadata": {scope: collectionScope, stores: storeKnowledge,
		args: []string{"field", "type", "group_by", "order_by", "limit", "filters.*"}},
	"representative_records": {scope: collectionScope, stores: storeKnowledge,
		args: []string{"group_by", "max_per_group", "filter.*"}},
	"extract_conversation": {scope: collectionScope, stores: storeKnowledge,
		args: []string{"text", "max_entities"},
		denied: map[string]string{
			"session_id": "names a memory bucket by its raw id, which reads another user's session",
			"persist":    "writes entities to the shared graph and a document under an id the guard cannot see",
		}},
	// The sources a check cites are data, as plan.filters.document_ids is:
	// the facade reads only the collection the scope check forces, so a
	// source of another collection is missing, as one that does not exist.
	"check_against_sources": {scope: collectionScope, stores: storeKnowledge,
		args: []string{"sentences", "sentences[].text", "sentences[].sources", "equivalents.*"}},
	// Knowledge, addressed by id alone: the row guard decides. The list form
	// of knowledge_get is data, as memory_get's is: the facade lists a
	// document outside the caller's collection under missing
	// (cortexdb.DB.GetKnowledge), and a call sends one form.
	"knowledge_get": {stores: storeKnowledge, args: []string{"knowledge_ids"}, ids: []idField{{"knowledge_id", idKnowledge}},
		oneOf: []string{"knowledge_id", "knowledge_ids"}},
	"knowledge_delete": {stores: storeKnowledge, ids: []idField{{"knowledge_id", idKnowledge}}},

	// Memory: user_id, scope and namespace at the top level. The bucket a
	// session_id picks is held to the key's scope by the facade
	// (cortexdb.CallerScope, set by Key.CallerContext), so session_id is data
	// here.
	"memory_save": {scope: memoryScope, stores: storeMemory, rewritten: cortexdb.EngineMemoryKeys,
		args: paths(memoryArgs, reviewArgs), ids: []idField{{"memory_id", idMemory}, {"supersedes", idMemory}}, denied: graphDenied},
	"knowledge_memory_remember": {scope: memoryScope, stores: storeMemory, rewritten: cortexdb.EngineMemoryKeys,
		args: memoryArgs, ids: []idField{{"memory_id", idMemory}}},
	"memory_search": {scope: memoryScope, stores: storeMemory,
		args: paths(queryArgs[:4], planArgs, pprArgs, []string{"entity_names", "top_k", "session_id"})},
	// Memory, addressed by id alone. The list form of memory_get is data: the
	// facade lists a row outside the caller's scope under missing, in the
	// order asked, exactly as a row that does not exist (cortexdb.DB.GetMemory).
	// The facade reads the list and ignores memory_id when both are set, so a
	// call sends one form.
	"memory_get":    {stores: storeMemory, args: []string{"memory_ids"}, ids: []idField{{"memory_id", idMemory}}, oneOf: []string{"memory_id", "memory_ids"}},
	"memory_update": {stores: storeMemory, rewritten: cortexdb.EngineMemoryKeys, args: paths([]string{"content", "importance", "ttl_seconds", "metadata.*"}, reviewArgs), ids: []idField{{"memory_id", idMemory}}},
	"memory_delete": {stores: storeMemory, ids: []idField{{"memory_id", idMemory}}},

	// The graph, walked from a seed or read by node id. Under a caller scope
	// the facade leaves out the chunks and documents of a collection the key
	// may not read, the memories of a bucket it may not read, and the
	// entities no permitted collection lists, and serves an entity with its
	// id and name alone (cortexdb's withoutExpiredDocuments, servedSeeds).
	// The walk itself takes only those nodes and the edges the key's
	// collections state (cortexdb's walkAdmit), so what it reaches, a limit,
	// a score and a path are of the key's part of the graph alone.
	// Node ids and entity names are data, as plan.filters.document_ids is:
	// a node the scope hides answers as a node that does not exist, a name
	// included whose best match it hides, so loading it here and refusing
	// it would tell the two apart.
	"search_chunks_by_entities": {stores: storeKnowledge | storeMemory, facade: true,
		args: []string{"entity_names", "top_k", "max_hops"}},
	"search_paths": {stores: storeKnowledge | storeMemory, facade: true,
		args: []string{"entity_names", "node_ids", "max_depth", "direction", "edge_types", "relation_policies.*", "decay", "use_edge_weights", "max_paths", "max_expansions", "include_bookkeeping", "with_text"}},
	"knowledge_memory_expand_entity_context": {stores: storeKnowledge | storeMemory, facade: true,
		args: []string{"entity_ids", "entity_names", "max_hops", "edge_types", "node_types", "limit", "top_k_chunks", "max_context_chunks", "max_context_chars", "per_document_limit", "chunk_window", "retrieval_mode", "disable_graph", "graph_light", "max_entities_per_chunk"}},
	"expand_graph": {stores: storeKnowledge | storeMemory, facade: true,
		args: []string{"node_ids", "max_hops", "edge_types", "node_types", "limit", "relation_policies.*"}},
	"get_nodes": {stores: storeKnowledge | storeMemory, facade: true, args: []string{"node_ids"}},
	"knowledge_memory_neighbors": {stores: storeKnowledge | storeMemory, facade: true,
		args: []string{"node_id", "entity_name", "max_depth", "edge_types", "node_types", "direction", "limit"}},
	"knowledge_memory_shortest_path": {stores: storeKnowledge | storeMemory, facade: true,
		args: []string{"from_node_id", "to_node_id", "from_entity_name", "to_entity_name"}},

	// Both stores, each behind its gate. Recall adds no graph facts for a
	// caller with a scope (cortexdb.KnowledgeMemory.Recall).
	"knowledge_memory_recall": {scope: bothScopes, stores: storeKnowledge | storeMemory,
		args: paths(queryArgs, walkArgs, planArgs, pprArgs, recallArgs, []string{"detail", "explain", "planner"}), gates: recallGates},
	"knowledge_memory_build_context_pack": {scope: bothScopes, stores: storeKnowledge | storeMemory,
		args: paths(queryArgs, walkArgs, planArgs, pprArgs, recallArgs, []string{"explain", "planner"}), gates: recallGates},

	// The recall log, which the facade holds to the calling key: a confined
	// key reads and marks the recalls it made and no other, whatever key it
	// names, and its hits were held to its scope when the recall ran. The
	// memories no recall returned are left out of its stats.
	"recall_log_list": {stores: storeKnowledge | storeMemory, facade: true,
		args: []string{"limit", "before", "record_id", "contains", "key", "project", "empty"}},
	"recall_log_get":   {stores: storeKnowledge | storeMemory, facade: true, args: []string{"recall_id"}},
	"recall_log_stats": {stores: storeKnowledge | storeMemory, facade: true, args: []string{"limit", "record_id", "key"}},
	"recall_feedback": {stores: storeKnowledge | storeMemory, facade: true,
		args: []string{"recall_id", "record_id", "verdict", "note"}},
	"recall_feedback_export": {stores: storeKnowledge | storeMemory, facade: true, args: []string{"since_ms"}},
}

// refusedTools are the tools a confined key may not call, with why. Every
// tool the toolbox defines is here or in confinedTools.
var refusedTools = map[string]string{
	"cortex_query":                          "its graph prefetch searches chunks of every collection",
	"knowledge_memory_consolidate":          "reads memories and writes a document, under promotion.*, in one call",
	"knowledge_memory_promote_to_knowledge": "reads memories and writes a document in one call",
	"knowledge_memory_reflect":              "reads both stores through recall.* with no gate of its own",
	"get_chunks":                            "names chunks, whose document the guard does not load",
	"build_context":                         "names chunks, whose document the guard does not load",
	"find_nodes":                            "matches the content of every node, chunks of every collection included; it opens in a change of its own, once its matches pass the hidden-node filter",
	"graph_list_all":                        "lists the whole stored graph, which the facade does not hold to a caller scope",
	"graph_cypher_query":                    "queries the whole stored graph, which the facade does not hold to a caller scope",
	"rank_graph_nodes":                      "ranks every node of the stored graph, which the facade does not hold to a caller scope",
	"predict_graph_edges":                   "predicts edges from the whole stored graph, which the facade does not hold to a caller scope",
	"disambiguate_mentions":                 "matches mentions against every entity of the stored graph, which the facade does not hold to a caller scope",
	"graph_statistics":                      "counts the whole stored graph, which the facade does not hold to a caller scope",
	"memory_list_all":                       "lists every bucket",
	"changes_since":                         "feeds changes of every store and every collection",
	"graph_health":                          "reports on every store",
	"vector_dimension_repair":               "rewrites every store",
	"sweep_expired":                         "removes expired rows of every store and every collection",
}

// defaultRefusal is why a tool with no reason of its own is refused: the
// shared graph, ontologies, rules, runs and the other stores these tools
// reach carry no field a scope confines. A tool added to the toolbox later
// lands here too, so the policy fails closed until it is reviewed.
const defaultRefusal = "reaches state that no scope field confines"

func init() {
	for name := range toolAccess {
		if _, ok := confinedTools[name]; ok {
			continue
		}
		if _, ok := refusedTools[name]; ok {
			continue
		}
		refusedTools[name] = defaultRefusal
	}
}

// RefusedTools lists the tools a confined key may not call, each with why.
func RefusedTools() map[string]string {
	out := make(map[string]string, len(refusedTools))
	for k, v := range refusedTools {
		out[k] = v
	}
	return out
}

// ConfinedTools lists the tools a confined key may call, sorted.
func ConfinedTools() []string {
	out := make([]string, 0, len(confinedTools))
	for name := range confinedTools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// AuthorizeToolArgs is AuthorizeRows for a CallTool: the key's scope checked
// against the named tool's arguments rather than against a request message.
// It is a no-op for an unconfined key. For a confined key, in this order:
//
//   - the tool must be on the allowlist;
//   - every argument must be one the allowlist classifies, under any spelling,
//     and none may be one it denies;
//   - for each confined field: the tool must honour it, the argument must be
//     set and equal the key's value, and so must every nested copy;
//   - a tool that touches the store the field does not confine must have that
//     store switched off by its gate.
//
// A tool with no scope field skips the third rule: it names its rows by id,
// and the row guard decides.
func (k Key) AuthorizeToolArgs(tool string, args []byte) error {
	if k.Scope.IsZero() {
		return nil
	}
	rule, err := confinedRule(k, tool)
	if err != nil {
		return err
	}
	obj, err := argsObject(args)
	if err != nil {
		return fmt.Errorf("%w: key %q is confined and the arguments of tool %s could not be inspected: %v", ErrDenied, k.ID, tool, err)
	}
	if err := rule.checkPaths(obj); err != nil {
		return fmt.Errorf("%w: key %q is confined and %s %v", ErrDenied, k.ID, tool, err)
	}
	for _, c := range k.Scope.constraints() {
		if len(rule.scope) > 0 {
			if !contains(rule.scope, c.field) {
				return fmt.Errorf("%w: key %q is confined to %s=%s and tool %s takes no %s argument",
					ErrDenied, k.ID, c.field, c, tool, c.field)
			}
			top, nested := scopeValues(obj, c.field)
			if top == "" {
				return fmt.Errorf("%w: key %q is confined to %s=%s and the call to %s leaves %s unset",
					ErrDenied, k.ID, c.field, c, tool, c.field)
			}
			if !c.allows(top) {
				return fmt.Errorf("%w: key %q is confined to %s=%s and the call to %s asks for %q",
					ErrDenied, k.ID, c.field, c, tool, top)
			}
			for _, v := range nested {
				if !c.allows(v) {
					return fmt.Errorf("%w: key %q is confined to %s=%s and the call to %s carries a nested %s=%q",
						ErrDenied, k.ID, c.field, c, tool, c.field, v)
				}
			}
		}
		if other := rule.stores &^ confinedStore(c.field); other != 0 && !rule.facade {
			gate := rule.gates[c.field]
			if !gateOn(obj, gate) {
				return fmt.Errorf("%w: key %q is confined to %s=%s and tool %s reads %s, which the call must switch off with %s=true",
					ErrDenied, k.ID, c.field, c, tool, other, gate)
			}
		}
	}
	return nil
}

// ConfineToolArgs sets, for a confined key, the gates the named tool needs:
// a key confined to a collection gets disable_memory=true on a recall, a key
// confined to a user gets disable_knowledge=true. A port calls it before the
// tool and hands the tool what comes back. Arguments with nothing to force,
// every call by an unconfined key, and arguments that are not an object come
// back unchanged: the last are for AuthorizeToolArgs to refuse.
func (k Key) ConfineToolArgs(tool string, args []byte) []byte {
	if k.Scope.IsZero() {
		return args
	}
	rule, ok := confinedTools[tool]
	if !ok || len(rule.gates) == 0 {
		return args
	}
	obj, err := argsObject(args)
	if err != nil {
		return args
	}
	// A tool with gates has one for every field it honours (held by the
	// allowlist's completeness test), so each constraint forces one.
	for _, c := range k.Scope.constraints() {
		gate := rule.gates[c.field]
		// Every spelling goes: encoding/json would read DISABLE_MEMORY as
		// disable_memory, and which of two spellings wins is not defined.
		for key := range obj {
			if strings.EqualFold(key, gate) {
				delete(obj, key)
			}
		}
		obj[gate] = true
	}
	// Marshalling a map decoded from JSON cannot fail.
	out, _ := json.Marshal(obj)
	return out
}

// confinedRule is the allowlist line for a tool, or the denial a confined key
// gets for a tool that has none.
func confinedRule(k Key, tool string) (toolRule, error) {
	rule, ok := confinedTools[tool]
	if ok {
		return rule, nil
	}
	if reason, refused := refusedTools[tool]; refused {
		return toolRule{}, fmt.Errorf("%w: key %q is confined and %s is not a tool a confined key may call: it %s",
			ErrDenied, k.ID, tool, reason)
	}
	return toolRule{}, fmt.Errorf("%w: %s is not a tool this server defines", ErrDenied, tool)
}

// checkPaths walks the arguments and refuses the first path the rule does not
// classify as scope, data, id or gate, and the first path it denies. Keys are
// matched without regard to case, because encoding/json matches them so.
func (r toolRule) checkPaths(obj map[string]any) error {
	allowed := map[string]bool{}
	open := map[string]bool{}
	for _, p := range paths(r.scope, r.args) {
		if strings.HasSuffix(p, ".*") {
			open[strings.ToLower(strings.TrimSuffix(p, ".*"))] = true
			continue
		}
		allowed[strings.ToLower(p)] = true
	}
	for _, id := range r.ids {
		allowed[strings.ToLower(id.path)] = true
	}
	for _, g := range r.gates {
		allowed[strings.ToLower(g)] = true
	}
	denied := map[string]string{}
	for p, why := range r.denied {
		denied[strings.ToLower(p)] = why
	}
	var sent []string
	for _, name := range r.oneOf {
		if lookupKey(obj, name) != nil {
			sent = append(sent, name)
		}
	}
	if len(sent) > 1 {
		return fmt.Errorf("takes %s, not both: the tool reads one of them only", strings.Join(sent, " or "))
	}
	var walk func(path string, v any) error
	walk = func(path string, v any) error {
		switch t := v.(type) {
		case map[string]any:
			for key, inner := range t {
				child := strings.ToLower(key)
				if path != "" {
					child = path + "." + child
				}
				if why, no := denied[child]; no {
					return fmt.Errorf("takes no %s from a confined key: it %s", child, why)
				}
				if open[child] {
					if err := r.checkOpenKeys(child, inner); err != nil {
						return err
					}
					continue
				}
				if !allowed[child] {
					return fmt.Errorf("takes no %s argument a confined key may send", child)
				}
				if err := walk(child, inner); err != nil {
					return err
				}
			}
		case []any:
			for _, elem := range t {
				if err := walk(path+"[]", elem); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk("", obj)
}

// checkOpenKeys holds the keys of an open object to what a confined key may
// put there. A metadata map (written to the row) takes no key the engine
// reserves by a leading "_", and a document's metadata takes none of the
// keys the engine writes on every chunk. A filter map (filters.*, filter.*)
// reads, so it may name an engine key: that is how a curator lists what
// waits. A tool whose facade rewrites engine keys takes them back under
// their exact spelling.
func (r toolRule) checkOpenKeys(path string, v any) error {
	obj, _ := v.(map[string]any)
	meta := path == "metadata"
	for key := range obj {
		if meta && r.rewritten != nil && slices.Contains(r.rewritten(), key) {
			continue
		}
		lower := strings.ToLower(key)
		if meta && strings.HasPrefix(lower, "_") {
			return fmt.Errorf("takes no %s.%s from a confined key: a key that starts with _ is the engine's", path, lower)
		}
		if why, no := engineChunkKeys[lower]; meta && no && r.stores&storeKnowledge != 0 {
			return fmt.Errorf("takes no %s.%s from a confined key: it %s", path, lower, why)
		}
		if why, no := writerKeys[lower]; meta && no {
			return fmt.Errorf("takes no %s.%s from a confined key: it %s", path, lower, why)
		}
	}
	return nil
}

// argsObject reads tool arguments, a JSON object. No arguments is an empty
// object. Anything else is an error, which a confined key's caller turns into
// a denial.
func argsObject(args []byte) (map[string]any, error) {
	obj := map[string]any{}
	if len(bytes.TrimSpace(args)) == 0 {
		return obj, nil
	}
	if err := json.Unmarshal(args, &obj); err != nil {
		return nil, fmt.Errorf("tool arguments must be a JSON object: %w", err)
	}
	// null decodes into a nil map without an error.
	if obj == nil {
		return nil, fmt.Errorf("tool arguments must be a JSON object, not null")
	}
	return obj, nil
}

// gateOn reports whether the arguments switch a store off: the gate is sent,
// and true under every spelling sent. encoding/json may keep any one of two
// spellings, so one false copy is enough to refuse.
func gateOn(obj map[string]any, gate string) bool {
	seen := false
	for k, v := range obj {
		if !strings.EqualFold(k, gate) {
			continue
		}
		if on, _ := v.(bool); !on {
			return false
		}
		seen = true
	}
	return seen
}

// lookupKey is obj[key] under any spelling of key, the exact one first.
func lookupKey(obj map[string]any, key string) any {
	if v, ok := obj[key]; ok {
		return v
	}
	for k, v := range obj {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return nil
}

// scopeValues gives the top-level value of a scope field and every nested
// copy of it, under any spelling, the same view the typed RPCs give through
// protoreflect. Another spelling at the top level (USER_ID beside user_id) is
// a value encoding/json will also read into the same argument, so it counts
// as a nested copy.
func scopeValues(obj map[string]any, field string) (top string, nested []string) {
	top, _ = obj[field].(string)
	for k, v := range obj {
		if k != field && strings.EqualFold(k, field) {
			if s, _ := v.(string); s != "" {
				nested = append(nested, s)
			}
		}
		nested = append(nested, jsonValuesIn(v, field, jsonScanDepth)...)
	}
	return top, nested
}

// jsonScanDepth bounds the walk for nested copies of a scope field, matching
// the bound the gRPC interceptor uses on request messages. Nothing the
// toolbox accepts nests a scope field deeper than a retrieval plan's filters;
// the bound is here so arguments shaped by an attacker cannot turn an
// authorisation check into an unbounded walk.
const jsonScanDepth = 4

// jsonValuesIn reads the named field off one decoded JSON value and keeps
// descending. Arrays do not spend depth: they are containers of the same
// nesting level, and a list of plans is no deeper than one plan.
func jsonValuesIn(v any, field string, depth int) []string {
	if depth <= 0 {
		return nil
	}
	switch t := v.(type) {
	case map[string]any:
		var out []string
		for k, inner := range t {
			if strings.EqualFold(k, field) {
				if s, _ := inner.(string); s != "" {
					out = append(out, s)
				}
			}
			out = append(out, jsonValuesIn(inner, field, depth-1)...)
		}
		return out
	case []any:
		var out []string
		for _, elem := range t {
			out = append(out, jsonValuesIn(elem, field, depth)...)
		}
		return out
	default:
		return nil
	}
}

// JSONPathValues returns the strings the arguments carry at a path: a string
// or an array of strings at the end of the path, under any spelling of each
// key, through every element where the path says "[]". Empty strings are
// dropped; the result is sorted and without duplicates.
func JSONPathValues(args []byte, path string) ([]string, error) {
	obj, err := argsObject(args)
	if err != nil {
		return nil, err
	}
	return valuesAt(obj, path), nil
}

// valuesAt is JSONPathValues on decoded arguments.
func valuesAt(obj map[string]any, path string) []string {
	seen := map[string]bool{}
	for _, v := range pathValues(obj, strings.Split(path, ".")) {
		if v != "" {
			seen[v] = true
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// rowData are the data arguments that name rows without the guard loading
// them: the list forms of memory_get and knowledge_get, which the facade
// answers with missing; plan.filters.document_ids, a pin the collection
// filter decides; the nodes a graph tool starts from or walks to, which the
// facade answers as nodes that do not exist when the scope hides them; and
// the sources a check cites, which it answers as missing. The request log
// records them beside the ids the guard loads.
var rowData = []string{
	"memory_ids", "knowledge_ids", "plan.filters.document_ids",
	"node_id", "node_ids", "entity_ids", "from_node_id", "to_node_id",
	"sentences[].sources",
	// The recall log's: a logged recall, and the memory or document a
	// recall returned.
	"recall_id", "record_id",
}

// rowPaths are the argument paths of the tool that name rows: its ids, and
// the rowData it takes.
func (r toolRule) rowPaths() []string {
	var out []string
	for _, id := range r.ids {
		out = append(out, id.path)
	}
	for _, p := range rowData {
		if slices.Contains(r.args, p) {
			out = append(out, p)
		}
	}
	return out
}

// RowIDsOf returns the ids a call to the tool names in its arguments, by
// path, for the request log to say what a call reached for: the values at
// the paths the allowlist declares for the tool (rowPaths), read as
// JSONPathValues reads them: the distinct ids, sorted. A tool off the
// allowlist, arguments that are not a JSON object, and a call that names no
// row give nil. It reads the arguments only, for any key; it decides
// nothing.
func RowIDsOf(tool string, args []byte) map[string][]string {
	rule, ok := confinedTools[tool]
	if !ok {
		return nil
	}
	// Arguments that are not an object decode to none, and name no row.
	obj, _ := argsObject(args)
	var out map[string][]string
	for _, p := range rule.rowPaths() {
		if ids := valuesAt(obj, p); len(ids) > 0 {
			if out == nil {
				out = map[string][]string{}
			}
			out[p] = ids
		}
	}
	return out
}

func pathValues(v any, segments []string) []string {
	if len(segments) == 0 {
		switch t := v.(type) {
		case string:
			return []string{t}
		case []any:
			var out []string
			for _, elem := range t {
				if s, _ := elem.(string); s != "" {
					out = append(out, s)
				}
			}
			return out
		}
		return nil
	}
	obj, _ := v.(map[string]any)
	name, each := strings.CutSuffix(segments[0], "[]")
	var out []string
	for k, inner := range obj {
		if !strings.EqualFold(k, name) {
			continue
		}
		if !each {
			out = append(out, pathValues(inner, segments[1:])...)
			continue
		}
		elems, _ := inner.([]any)
		for _, elem := range elems {
			out = append(out, pathValues(elem, segments[1:])...)
		}
	}
	return out
}

// ConfinedToolsTable renders the allowlist as Markdown, for a review to check
// tool by tool: what each tool honours, touches, names and refuses.
func ConfinedToolsTable() string {
	var b strings.Builder
	b.WriteString("| Tool | Honours | Touches | Row ids it names | Gate | Refused arguments |\n|---|---|---|---|---|---|\n")
	for _, name := range ConfinedTools() {
		r := confinedTools[name]
		var ids []string
		for _, id := range r.ids {
			ids = append(ids, fmt.Sprintf("`%s` (%s)", id.path, id.kind))
		}
		var gates []string
		for _, f := range []string{FieldCollection, FieldUserID, FieldScope, FieldNamespace} {
			if g := r.gates[f]; g != "" {
				gates = append(gates, fmt.Sprintf("%s → `%s=true`", f, g))
			}
		}
		if r.facade {
			gates = append(gates, "by the facade (CallerScope)")
		}
		var denied []string
		for p := range r.denied {
			denied = append(denied, "`"+p+"`")
		}
		sort.Strings(denied)
		if len(r.oneOf) > 0 {
			denied = append(denied, "`"+strings.Join(r.oneOf, "` with `")+"`")
		}
		scope := strings.Join(r.scope, ", ")
		if scope == "" {
			scope = "(by id only)"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s |\n", name, scope, r.stores,
			strings.Join(ids, ", "), strings.Join(gates, ", "), strings.Join(denied, ", "))
	}
	b.WriteString("\nRefused to a confined key:\n\n| Tool | Why |\n|---|---|\n")
	names := make([]string, 0, len(refusedTools))
	for name := range refusedTools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "| `%s` | %s |\n", name, refusedTools[name])
	}
	return b.String()
}
