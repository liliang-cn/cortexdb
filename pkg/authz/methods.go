package authz

import "fmt"

// Access is what an RPC does to the brain.
type Access int

const (
	// Unclassified is the zero value, and it is denied. An RPC that nobody
	// classified is an RPC nobody thought about, and the failure this whole
	// package exists to prevent is a write quietly passing as a read.
	Unclassified Access = iota
	Read
	Write
)

func (a Access) String() string {
	switch a {
	case Read:
		return "read"
	case Write:
		return "write"
	default:
		return "unclassified"
	}
}

// Method is one RPC's classification.
type Method struct {
	Access Access
	// Rowless marks an RPC that touches no scoped row at all — liveness,
	// server identity, the static tool catalogue. A confined key may still
	// call these: they expose nothing a scope could protect, and refusing
	// them would break the health probe of every scoped deployment.
	Rowless bool
}

// methods maps every gRPC full method name to what it does.
//
// This is an explicit table and not a name heuristic on purpose. A rule like
// strings.HasPrefix(method, "Delete") classifies today's RPCs correctly and
// then silently misclassifies the next one somebody adds — UpsertNamespace and
// RefreshInference are both writes that no prefix rule would catch. The table
// is paired with a test in pkg/rpcserver that walks the registered service
// descriptors and fails when a method is missing from it, so adding an RPC
// without classifying it is a build failure rather than a hole.
var methods = map[string]Method{
	// AdminService — Health and Info read no row. Backup reads all of them:
	// it writes a complete copy of the brain to the server's disk, so it is a
	// write by clearance and, more to the point, no confined key should be
	// able to obtain every row it is confined away from by asking for a copy.
	// Rowless is false so a confined key is refused it outright — the request
	// carries a filename, not a user_id, and there is nothing to narrow.
	"/cortexdb.v1.AdminService/Health": {Access: Read, Rowless: true},
	"/cortexdb.v1.AdminService/Info":   {Access: Read, Rowless: true},
	"/cortexdb.v1.AdminService/Backup": {Access: Write},

	// KnowledgeService.
	"/cortexdb.v1.KnowledgeService/SaveKnowledge":   {Access: Write},
	"/cortexdb.v1.KnowledgeService/UpdateKnowledge": {Access: Write},
	"/cortexdb.v1.KnowledgeService/GetKnowledge":    {Access: Read},
	"/cortexdb.v1.KnowledgeService/SearchKnowledge": {Access: Read},
	"/cortexdb.v1.KnowledgeService/DeleteKnowledge": {Access: Write},

	// MemoryService.
	"/cortexdb.v1.MemoryService/SaveMemory":   {Access: Write},
	"/cortexdb.v1.MemoryService/UpdateMemory": {Access: Write},
	"/cortexdb.v1.MemoryService/GetMemory":    {Access: Read},
	"/cortexdb.v1.MemoryService/SearchMemory": {Access: Read},
	"/cortexdb.v1.MemoryService/DeleteMemory": {Access: Write},

	// KnowledgeGraphService. Upsert/Delete/Import are the obvious writes;
	// RefreshInference materialises inferred triples and SaveOntologySchema
	// can activate a schema, so both are writes despite reading like queries.
	// ValidateShacl and the Explain* calls only compute over what is stored.
	//
	// QuerySparql is a write, which its name denies. CortexDB's SPARQL subset
	// executes updates, so this is the only safe static answer. rpcserver
	// narrows it to a read per call when the executor's own parser says the
	// query does not mutate — see sparql_access.go. The narrowing lives there
	// rather than here because it needs the graph store; this table stays the
	// answer for anything that cannot ask.
	//
	// executes INSERT DATA, DELETE DATA, DELETE WHERE and DELETE/INSERT/WHERE
	// alongside SELECT and ASK, and nothing between this table and
	// GraphStore.ExecuteSPARQL looks at which one arrived — so classifying it
	// as a read hands every read-only key a way to rewrite the graph in a
	// string. Narrowing it back to a read for SELECT-only queries would mean
	// the policy parsing SPARQL and agreeing with the executor's parser
	// forever; the cost of getting that disagreement wrong is silent.
	"/cortexdb.v1.KnowledgeGraphService/UpsertNamespace":       {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/ListNamespaces":        {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/UpsertKnowledgeGraph":  {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/FindKnowledgeGraph":    {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/DeleteKnowledgeGraph":  {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/ImportKnowledgeGraph":  {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/ExportKnowledgeGraph":  {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/QuerySparql":           {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/ValidateShacl":         {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/RefreshInference":      {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/SummarizeInference":    {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/ExplainInference":      {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/ExplainInferenceMatch": {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/SaveOntologySchema":    {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/GetOntologySchema":     {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/ListOntologySchemas":   {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/DeleteOntologySchema":  {Access: Write},

	// GraphRagService.
	"/cortexdb.v1.GraphRagService/InsertGraphDocument": {Access: Write},
	"/cortexdb.v1.GraphRagService/SearchGraphRag":      {Access: Read},
	"/cortexdb.v1.GraphRagService/InsertText":          {Access: Write},
	"/cortexdb.v1.GraphRagService/InsertTextBatch":     {Access: Write},
	"/cortexdb.v1.GraphRagService/SearchText":          {Access: Read},
	"/cortexdb.v1.GraphRagService/HybridSearchText":    {Access: Read},

	// ToolsService. ListTools hands back a static catalogue. CallTool stays in
	// the table as a write, but that value is only the answer for a caller who
	// cannot say which tool: it is what AuthorizeMethod returns, and the whole
	// toolbox is reachable through this one method, so the unrefined answer has
	// to be the strict one. Callers that can read the request use AuthorizeCall
	// instead, which decides from the named tool's ToolDefinition.Mutates —
	// see tools.go. The refinement is what lets a read-only key run
	// search_knowledge and knowledge_memory_recall, which it could not before.
	"/cortexdb.v1.ToolsService/ListTools": {Access: Read, Rowless: true},
	"/cortexdb.v1.ToolsService/CallTool":  {Access: Write},

	// ContractService reads the knowledge contract off the shelf. Both are
	// reads — nothing here stamps a grade — but neither is Rowless, for
	// Backup's reason rather than Health's: the tally counts every node and
	// edge in the store and NeedsAttention hands back their content, so both
	// see rows a confined key is confined away from, and the request carries
	// no scope field to narrow them by. Rowless false is therefore an outright
	// denial for a confined key, which is the honest answer — a tally silently
	// narrowed to one user's rows would report a shelf nobody has.
	"/cortexdb.v1.ContractService/ContractTally":  {Access: Read},
	"/cortexdb.v1.ContractService/NeedsAttention": {Access: Read},
}

// LookupMethod returns the classification of a gRPC full method name.
func LookupMethod(fullMethod string) (Method, bool) {
	m, ok := methods[fullMethod]
	return m, ok
}

// ClassifiedMethods lists every classified full method name. Tests use it to
// compare the table against what the server actually serves.
func ClassifiedMethods() []string {
	out := make([]string, 0, len(methods))
	for name := range methods {
		out = append(out, name)
	}
	return out
}

// AuthorizeMethod reports whether the key's clearance permits fullMethod.
//
// An unclassified method is denied. Failing open here would mean a newly added
// RPC — the case most likely to be a write — is reachable by every read-only
// key until somebody notices.
func (k Key) AuthorizeMethod(fullMethod string) error {
	m, ok := LookupMethod(fullMethod)
	if !ok {
		return fmt.Errorf("%w: %s is not classified as a read or a write", ErrDenied, fullMethod)
	}
	if m.Access == Write && k.Clearance != ReadWrite {
		return fmt.Errorf("%w: key %q is %s and %s is a write", ErrDenied, k.ID, k.Clearance, fullMethod)
	}
	return nil
}

// FieldLookup reports what a request message carries under a scope field name.
//
// declared is whether the request type has such a field at the top level at
// all; top is its top-level value (empty when unset); nested is every non-empty
// value of that name found deeper in the populated message, such as inside a
// RetrievalPlan's filters. The split matters: only the top-level field is
// reliably the one the handler acts on, while a nested copy may override it
// somewhere downstream, so a nested value is checked for conflict rather than
// accepted as satisfying the confinement.
type FieldLookup func(field string) (top string, nested []string, declared bool)

// AuthorizeRows reports whether the key's scope permits the rows this request
// asks for. It is a no-op for an unconfined key.
//
// The rule, for each confined field:
//
//   - the request type must declare the field at its top level, otherwise there
//     is nothing to confine and the call is denied;
//   - the top-level value must equal the key's value — an unset field means
//     "every user" or "every collection", which a confined key may not ask for;
//   - no nested copy of the field may disagree.
//
// Denial rather than narrowing is deliberate. Rewriting an over-broad request
// into a narrower one would hand the caller a quietly different answer than the
// one it asked for, and a search that silently returns a subset is impossible
// to debug from the client side. The one place narrowing would be defensible is
// a search whose field is simply unset, where "" plausibly means "unspecified"
// rather than "all" — but the same code path also serves deletes, where guessing
// is not acceptable, so the strict rule applies uniformly.
func (k Key) AuthorizeRows(fullMethod string, lookup FieldLookup) error {
	if k.Scope.IsZero() {
		return nil
	}
	m, ok := LookupMethod(fullMethod)
	if !ok {
		return fmt.Errorf("%w: %s is not classified as a read or a write", ErrDenied, fullMethod)
	}
	if m.Rowless {
		return nil
	}
	// Said explicitly rather than left to fall out of the generic rule below.
	// It would: a CallTool request declares no user_id, so "carries no user_id
	// field" already refuses it. But that is the request shape happening to
	// save us, and the day somebody adds a user_id to CallToolRequest for
	// convenience, confinement would start passing on the one RPC that reaches
	// every tool while still being unable to see the arguments. See
	// confinedCallToolDenial for why the arguments cannot be checked.
	if fullMethod == CallToolMethod {
		return confinedCallToolDenial(k)
	}
	if lookup == nil {
		return fmt.Errorf("%w: key %q is confined and the request for %s could not be inspected",
			ErrDenied, k.ID, fullMethod)
	}
	for _, c := range k.Scope.constraints() {
		top, nested, declared := lookup(c.field)
		if !declared {
			return fmt.Errorf("%w: key %q is confined to %s=%q and %s carries no %s field",
				ErrDenied, k.ID, c.field, c.value, fullMethod, c.field)
		}
		if top == "" {
			return fmt.Errorf("%w: key %q is confined to %s=%q and the request leaves %s unset",
				ErrDenied, k.ID, c.field, c.value, c.field)
		}
		if top != c.value {
			return fmt.Errorf("%w: key %q is confined to %s=%q and the request asks for %q",
				ErrDenied, k.ID, c.field, c.value, top)
		}
		for _, v := range nested {
			if v != c.value {
				return fmt.Errorf("%w: key %q is confined to %s=%q and the request carries a nested %s=%q",
					ErrDenied, k.ID, c.field, c.value, c.field, v)
			}
		}
	}
	return nil
}
