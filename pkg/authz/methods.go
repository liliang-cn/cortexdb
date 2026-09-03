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
	// AdminService — neither call reads a row.
	"/cortexdb.v1.AdminService/Health": {Access: Read, Rowless: true},
	"/cortexdb.v1.AdminService/Info":   {Access: Read, Rowless: true},

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
	"/cortexdb.v1.KnowledgeGraphService/UpsertNamespace":       {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/ListNamespaces":        {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/UpsertKnowledgeGraph":  {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/FindKnowledgeGraph":    {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/DeleteKnowledgeGraph":  {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/ImportKnowledgeGraph":  {Access: Write},
	"/cortexdb.v1.KnowledgeGraphService/ExportKnowledgeGraph":  {Access: Read},
	"/cortexdb.v1.KnowledgeGraphService/QuerySparql":           {Access: Read},
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

	// ToolsService. ListTools hands back a static catalogue. CallTool is the
	// one genuinely ambiguous entry in this table: it dispatches on a name
	// with JSON arguments, so a single classification has to cover the whole
	// toolbox, and the toolbox contains writes. It is a write, which means a
	// read-only key cannot call any tool, including the read-only ones.
	// Classifying per tool name would put a second, differently-shaped policy
	// table next to this one and would have to be kept in step with the tool
	// definitions by hand; being unable to use CallTool from a read-only key
	// is the cheaper mistake.
	"/cortexdb.v1.ToolsService/ListTools": {Access: Read, Rowless: true},
	"/cortexdb.v1.ToolsService/CallTool":  {Access: Write},
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
