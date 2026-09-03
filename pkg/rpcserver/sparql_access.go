package rpcserver

// Classifying SPARQL by asking the thing that runs it.
//
// CortexDB's SPARQL subset executes updates — INSERT DATA, DELETE DATA,
// DELETE WHERE and DELETE/INSERT/WHERE — so the RPC named QuerySparql and the
// tool named knowledge_graph_query both write, and the only safe *static*
// classification for them is Write. That is where they sit in the tables, and
// it is the right default.
//
// The cost of stopping there is that a read-only key loses the query language
// entirely, SELECT included, which is most of what a read-only key would want
// a graph for. The obvious fix — have the policy recognise INSERT and DELETE
// itself — is the bad kind of fix: a second parser that has to agree with the
// executor's forever, whose disagreement is silent and lands in the dangerous
// direction, an update the policy read as a query.
//
// So the policy asks the executor's own parser (graph.SPARQLMutates) instead.
// One parser, one answer, and a query it cannot parse is reported as a write.
// The refinement can only ever narrow Write to Read, never widen: if anything
// here fails, the static Write classification stands.

import (
	"context"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

const (
	querySparqlMethod = "/cortexdb.v1.KnowledgeGraphService/QuerySparql"
	sparqlToolName    = "knowledge_graph_query"
)

// sparqlAccess returns a refined classification when this call carries SPARQL
// that does not mutate. The second result is false for everything else,
// including SPARQL that does mutate — those keep the table's Write.
func sparqlAccess(ctx context.Context, db *cortexdb.DB, fullMethod string, req any) (name string, m authz.Method, ok bool) {
	if db == nil {
		return "", authz.Method{}, false
	}
	var query string
	switch fullMethod {
	case querySparqlMethod:
		query, ok = stringField(req, "query")
		name = "QuerySparql"
	case authz.CallToolMethod:
		tool, got := stringField(req, "name")
		if !got || tool != sparqlToolName {
			return "", authz.Method{}, false
		}
		// The tool takes its arguments as opaque JSON, so the query is one
		// level down. Reaching into it is the same act the handler performs a
		// moment later; the policy just does it first.
		args, got := stringField(req, "args_json")
		if !got {
			return "", authz.Method{}, false
		}
		query, ok = jsonStringField(args, "query")
		name = sparqlToolName
	default:
		return "", authz.Method{}, false
	}
	if !ok || query == "" {
		return "", authz.Method{}, false
	}
	graph := db.Graph()
	if graph == nil || graph.SPARQLMutates(ctx, query) {
		return "", authz.Method{}, false
	}
	// Rowless: the SPARQL surface carries none of the scope fields, so a
	// confined key is still refused it by the row check. This only lifts the
	// clearance half for a read-only, unconfined key.
	return name, authz.Method{Access: authz.Read}, true
}

func stringField(req any, field string) (string, bool) {
	msg, ok := req.(proto.Message)
	if !ok || msg == nil {
		return "", false
	}
	m := msg.ProtoReflect()
	if !m.IsValid() {
		return "", false
	}
	fd := m.Descriptor().Fields().ByName(protoreflect.Name(field))
	if fd == nil || fd.Kind() != protoreflect.StringKind || fd.IsList() || fd.IsMap() {
		return "", false
	}
	return m.Get(fd).String(), true
}
