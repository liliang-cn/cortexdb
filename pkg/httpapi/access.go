package httpapi

import (
	"net/http"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
)

// route identifies one endpoint: the verb and the path it is registered under.
// It is the HTTP analogue of a gRPC full method name, and it is what a denial
// names, so String is the wording an operator reads in a 403.
type route struct {
	method string
	path   string
}

func (r route) String() string { return r.method + " " + r.path }

// toolRoutePath is the path the tool endpoint is classified under. Every tool
// call goes through one handler, so it gets one entry rather than one per tool;
// the template shape is what the router reports for it too, so the table and
// the router agree on the name of the thing being classified.
const toolRoutePath = toolCallPrefix + "{tool}"

// routeAccess is what every REST route does to the brain.
//
// This is the same discipline as the method table in pkg/authz/methods.go and
// exists for the same reason: a rule like "POST is a write" classifies today's
// routes wrongly on its first line — POST /v1/knowledge/search and POST
// /v1/recall are reads — and any other heuristic would misclassify the next
// route somebody adds. It is paired with the test in route_coverage_test.go,
// which walks the routes the router actually registers and fails when one is
// missing here, so adding an endpoint without deciding what it does is a build
// failure rather than a hole.
//
// A route absent from this table is Unclassified, and authz denies that to
// every key. Adding an endpoint therefore fails closed twice: the test refuses
// to go green, and if the test were somehow bypassed the route would refuse to
// serve a keyed deployment at all.
var routeAccess = map[route]authz.Method{
	// Liveness and server identity read no row, so a confined key may call
	// them: they expose nothing a scope could protect, and refusing them
	// would break the health probe of every scoped deployment. This mirrors
	// AdminService/Health and AdminService/Info exactly.
	{http.MethodGet, "/v1/health"}: {Access: authz.Read, Rowless: true},
	{http.MethodGet, "/v1/info"}:   {Access: authz.Read, Rowless: true},

	// Knowledge. The GET and DELETE take an id in the query string and carry
	// no scope fields at all, which means a confined key is refused them
	// outright — the same answer GetKnowledge and DeleteKnowledge give over
	// gRPC, where those request messages have no user_id either.
	{http.MethodPost, "/v1/knowledge"}:        {Access: authz.Write},
	{http.MethodPost, "/v1/knowledge/search"}: {Access: authz.Read},
	{http.MethodGet, "/v1/knowledge"}:         {Access: authz.Read},
	{http.MethodDelete, "/v1/knowledge"}:      {Access: authz.Write},

	// Memory.
	{http.MethodPost, "/v1/memory"}:        {Access: authz.Write},
	{http.MethodPost, "/v1/memory/search"}: {Access: authz.Read},
	{http.MethodGet, "/v1/memory"}:         {Access: authz.Read},
	{http.MethodDelete, "/v1/memory"}:      {Access: authz.Write},

	// Retrieval. Both are POSTs because they carry a request body, not
	// because they change anything.
	{http.MethodPost, "/v1/query"}:  {Access: authz.Read},
	{http.MethodPost, "/v1/recall"}: {Access: authz.Read},

	// Graph. Upserts write; expansion and SPARQL only read what is stored,
	// matching UpsertKnowledgeGraph and QuerySparql on the gRPC side.
	{http.MethodPost, "/v1/graph/entities"}:  {Access: authz.Write},
	{http.MethodPost, "/v1/graph/relations"}: {Access: authz.Write},
	{http.MethodPost, "/v1/graph/expand"}:    {Access: authz.Read},
	{http.MethodPost, "/v1/graph/triples"}:   {Access: authz.Write},
	{http.MethodPost, "/v1/graph/sparql"}:    {Access: authz.Read},

	// Tools. The listing is a static catalogue and touches no row.
	//
	// The call is deliberately conservative: one route dispatches on a name to
	// the whole toolbox, the toolbox contains writes, and until each tool says
	// for itself whether it mutates there is no honest way to call any of them
	// a read. So every tool call is a write, which means a read-only key
	// cannot call even the read-only tools. That classification is being made
	// per tool right now — cortexdb.ToolDefinition grew a Mutates field for
	// exactly this — and when it lands, this entry becomes a lookup against
	// the tool's own declaration on both surfaces at once. Guessing per tool
	// name here in the meantime would put a third policy table in the tree and
	// would be wrong the first time a tool was renamed.
	{http.MethodGet, "/v1/tools"}:    {Access: authz.Read, Rowless: true},
	{http.MethodPost, toolRoutePath}: {Access: authz.Write},
}

// lookupRoute returns the classification of a route. A missing entry yields the
// zero authz.Method, which is Unclassified and therefore denied — the caller
// does not need to branch on ok to be safe, only to report why.
func lookupRoute(r route) (authz.Method, bool) {
	m, ok := routeAccess[r]
	return m, ok
}

// classifiedRoutes lists every route in the table. The coverage test uses it to
// catch the drift the other way: an entry left behind after a route was renamed
// or removed, sitting in the table looking like coverage it no longer provides.
func classifiedRoutes() []route {
	out := make([]route, 0, len(routeAccess))
	for r := range routeAccess {
		out = append(out, r)
	}
	return out
}
