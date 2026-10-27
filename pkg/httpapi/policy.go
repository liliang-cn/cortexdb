package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
)

// The REST surface enforces the same key policy the gRPC surface does, split
// the same way its interceptor is: authentication decides which key is
// speaking, clearance decides whether that key may invoke this route, and scope
// decides whether it may touch the rows the request names.
//
// The split is visible in the status codes on purpose. 401 means "I do not know
// who you are"; 403 means "I know exactly who you are and this is not yours".
// Collapsing them into one code is the difference between an operator fixing a
// typo in a secret and an operator widening a scope, and from the outside those
// two failures look identical.

// keyContext carries the authenticated key from the authentication layer to the
// per-route authorization wrapper. It is unexported and typed so nothing else
// can put a key into a request context.
type keyContext struct{}

func keyFrom(ctx context.Context) (authz.Key, bool) {
	key, ok := ctx.Value(keyContext{}).(authz.Key)
	return key, ok
}

// authenticate resolves the bearer secret to a key and refuses with 401 when it
// cannot. A disabled policy — no key file and no token — is passed straight
// through, which is what an unset token has always meant here and on the gRPC
// side; locking an open loopback deployment on upgrade would be the worse
// surprise.
func authenticate(keys *authz.KeySet, next http.Handler) http.Handler {
	return authenticateFrom(authz.StaticKeySource(keys), next)
}

// authenticateFrom is authenticate over the key set src holds at each request,
// so a key file is applied as it stands now.
func authenticateFrom(src *authz.KeySource, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys := src.Current()
		if !keys.Enabled() {
			next.ServeHTTP(w, r)
			return
		}
		secret, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		key, ok := authz.Key{}, false
		if found {
			// Lookup compares every key in constant time and does not stop at
			// the match, so neither the secret nor its position in the file
			// can be recovered by timing the refusals.
			key, ok = keys.Lookup(secret)
		}
		if !ok {
			// Deliberately the same message whether the secret was never valid
			// or has since been revoked out of the key file: telling them apart
			// tells a prober which guesses were once right.
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid or missing bearer token")
			return
		}
		observability.CallFrom(r.Context()).SetKey(key.ID)
		// The facade learns the caller the way it does over gRPC: it signs
		// what this call writes and confines the memory buckets it may reach.
		next.ServeHTTP(w, r.WithContext(key.CallerContext(context.WithValue(r.Context(), keyContext{}, key))))
	})
}

// authorize wraps one route's handler with the clearance and scope checks for
// whichever key authenticated.
//
// It wraps per route rather than sitting in front of the router because the
// route is the unit being classified. In front, an unclassified path and a path
// nobody registered are the same request, and every 404 would come back as a
// 403 the moment a key file was configured.
func authorize(rr route, next http.HandlerFunc) http.HandlerFunc {
	return authorizeWith(nil)(rr, next)
}

// authorizeWith is authorize with a brain to ask, which the tool route needs
// and nothing else does: the one question a table cannot answer is whether a
// SPARQL query writes, and only the executor's own parser can.
func authorizeWith(db *cortexdb.DB) func(rr route, next http.HandlerFunc) http.HandlerFunc {
	return func(rr route, next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			key, enforcing := keyFrom(r.Context())
			if !enforcing {
				// No policy in force. The classification table still exists and
				// the coverage test still holds it to every route; there is just
				// no key whose clearance or scope could be consulted.
				next(w, r)
				return
			}
			// Every policy step below that reads the body reads it into a map,
			// and the handler decodes it into a struct. For a key those steps
			// apply to, both must read one value per field, so a body that
			// names a field twice is refused first (authz.CheckUnambiguous).
			if key.ReadsBody() {
				body, ok := bufferBody(w, r)
				if !ok {
					return
				}
				if err := authz.CheckUnambiguous(body); err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
			}
			// A missing entry is the zero authz.Method, which is Unclassified, and
			// AuthorizeOperation denies it by name. Nothing branches on ok: a route
			// nobody classified is a route nobody thought about.
			m, _ := lookupRoute(rr)
			name := rr.String()
			if rr.path == toolRoutePath {
				// The table says the toolbox is a write, which is the only safe
				// answer for a route that reaches every tool. Refine it to the
				// tool actually named, the same way the gRPC side does — from
				// the tool's own declaration, never a second list — and deny a
				// name the toolbox does not define, fail closed. A confined key
				// has its arguments checked against the allowlist line of the
				// tool (authz.AuthorizeToolArgs), below, once clearance has
				// passed; the route's own row check is skipped because the
				// body is the tool's arguments, not a request of this route.
				tool, _ := strings.CutPrefix(r.URL.Path, toolCallPrefix)
				name = "POST " + toolCallPrefix + tool
				access, known := authz.LookupTool(tool)
				if !known {
					writeError(w, http.StatusForbidden, "denied: "+tool+" is not a tool this server defines")
					return
				}
				m = authz.Method{Access: access, Rowless: true}
				if tool == sparqlToolName && access == authz.Write && db != nil {
					if q, ok := peekJSONString(w, r, "query"); ok && !db.Graph().SPARQLMutates(r.Context(), q) {
						m.Access = authz.Read
					}
				}
			}
			// The SPARQL route is a write in the table; a query the executor's
			// parser says does not mutate is a read. It can only narrow: a
			// body that will not parse, or no database to parse it with,
			// leaves the write.
			if rr == sparqlRoute && m.Access == authz.Write && db != nil {
				if q, ok := peekJSONString(w, r, "query"); ok && !db.Graph().SPARQLMutates(r.Context(), q) {
					m.Access = authz.Read
				}
			}
			if err := key.AuthorizeOperation(name, m); err != nil {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
			if rr.path == toolRoutePath && !key.Scope.IsZero() {
				tool, _ := strings.CutPrefix(r.URL.Path, toolCallPrefix)
				body, ok := bufferBody(w, r)
				if !ok {
					return
				}
				// Arguments that are not an object are the caller's mistake,
				// not a denial.
				if len(bytes.TrimSpace(body)) > 0 && !(isJSONObject(body) && json.Valid(body)) {
					writeError(w, http.StatusBadRequest, "tool arguments must be a JSON object")
					return
				}
				// The gates a confined key needs are forced into the body
				// first, so the tool is called with exactly the arguments the
				// policy checked. See authz.Key.ConfineToolArgs.
				body = key.ConfineToolArgs(tool, body)
				if err := key.AuthorizeToolArgs(tool, body); err != nil {
					writeError(w, http.StatusForbidden, err.Error())
					return
				}
				// A tool that names a row by id may only touch a row within
				// the scope; a row the key may not have is 404, like one that
				// does not exist. See authz.Scope.GuardToolRows. With no
				// database to read the rows from, the guard cannot decide,
				// and a guard that cannot decide refuses.
				if db == nil {
					writeError(w, http.StatusInternalServerError, "denied: key \""+key.ID+"\" is confined and the tool route has no database to guard rows against")
					return
				}
				if err := key.Scope.GuardToolRows(r.Context(), db, tool, body); err != nil {
					writeError(w, toolGuardStatus(err), err.Error())
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				r.ContentLength = int64(len(body))
			}
			if key.Scope.IsZero() || m.Rowless {
				next(w, r)
				return
			}
			lookup, ok := bodyFieldLookup(w, r)
			if !ok {
				return
			}
			if err := key.AuthorizeOperationRows(rr.String(), m, lookup); err != nil {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
			if tw, typed := typedWriteTools[rr.String()]; typed {
				body, _ := bufferBody(w, r)
				if args, decoded := tw.args(body); decoded {
					if err := key.AuthorizeToolArgs(tw.tool, args); err != nil {
						writeError(w, http.StatusForbidden, err.Error())
						return
					}
				}
			}
			if u, upserts := upsertRoutes[rr.String()]; upserts && !guardUpsert(w, r, db, key.Scope, u) {
				return
			}
			next(w, r)
		}
	}
}

// typedWriteTools are the typed writes a confined key may call, each with
// the tool it stands for. A typed write takes what the allowlist line of its
// tool takes, so the route and POST /v1/tools/<name> cannot disagree on what
// a confined key may send: declared entities and relations write the shared
// graph, and some metadata keys are the engine's. The typed writes of the
// shared graph name no scope field, so a confined key never reaches them.
var typedWriteTools = map[string]typedWrite{
	"POST /v1/knowledge": {"knowledge_save", knowledgeToolArgs},
	"POST /v1/memory":    {"memory_save", memoryToolArgs},
}

type typedWrite struct {
	tool string
	args func(body []byte) ([]byte, bool)
}

// decodeTyped decodes a typed write's body the way the handler will
// (decodeBody), so the check reads what the handler reads: the decoder
// matches a field name by Unicode case folding, so "Entities" and
// "entitieſ" are its Entities, which a check by name would miss. A body the
// decoder refuses is left to the handler, which answers it 400 with the same
// decoder before anything is written, as on main. That is also the answer to
// a key that reaches no row of the route's store: the scope field it must
// send (collection on the memory route, user_id, scope or namespace on the
// knowledge route) is not a field of the route, and a body without it is
// the row checks' 403.
func decodeTyped(body []byte, req any) bool {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	return dec.Decode(req) == nil
}

// knowledgeToolArgs and memoryToolArgs hand the allowlist, of a decoded
// typed write, the arguments it shares with its tool: the scope fields,
// metadata, and entities and relations when the body declares any. The
// scope fields and the row were checked as for any typed route. The facade
// signs both typed writes (cortexdb.WithWriter replaces written_by and drops
// updated_by), so those two metadata keys are not handed over: a row read
// back and sent again is not refused for the signature it carries. Another
// spelling is not replaced, and keeps the refusal.
func knowledgeToolArgs(body []byte) ([]byte, bool) {
	var req cortexdb.KnowledgeSaveRequest
	if !decodeTyped(body, &req) {
		return nil, false
	}
	args := map[string]any{authz.FieldCollection: req.Collection}
	meta := make(map[string]any, len(req.Metadata))
	for k, v := range req.Metadata {
		meta[k] = v
	}
	return typedToolArgs(args, meta, len(req.Entities), len(req.Relations)), true
}

func memoryToolArgs(body []byte) ([]byte, bool) {
	var req cortexdb.MemorySaveRequest
	if !decodeTyped(body, &req) {
		return nil, false
	}
	args := map[string]any{}
	for field, v := range map[string]string{authz.FieldUserID: req.UserID, authz.FieldScope: req.Scope, authz.FieldNamespace: req.Namespace} {
		if v != "" {
			args[field] = v
		}
	}
	meta := make(map[string]any, len(req.Metadata))
	for k, v := range req.Metadata {
		meta[k] = v
	}
	return typedToolArgs(args, meta, len(req.Entities), len(req.Relations)), true
}

func typedToolArgs(args, meta map[string]any, entities, relations int) []byte {
	delete(meta, "written_by")
	delete(meta, "updated_by")
	if len(meta) > 0 {
		args["metadata"] = meta
	}
	if entities > 0 {
		args["entities"] = []any{}
	}
	if relations > 0 {
		args["relations"] = []any{}
	}
	raw, _ := json.Marshal(args)
	return raw
}

// sparqlToolName is the one tool whose classification is decided per call.
const sparqlToolName = "knowledge_graph_query"

// sparqlRoute is the one route whose classification is decided per call.
var sparqlRoute = route{http.MethodPost, "/v1/graph/sparql"}

// peekJSONString reads one top-level string out of the body and puts the body
// back, so the handler still sees it. A body that will not parse yields no
// value, which leaves the static classification in place — the call is about
// to fail on the same JSON anyway.
func peekJSONString(w http.ResponseWriter, r *http.Request, field string) (string, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		return "", false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return "", false
	}
	v, ok := m[field].(string)
	return v, ok
}

// nestedScanDepth bounds the walk for nested copies of a scope field, matching
// the bound the gRPC interceptor uses. Nothing the facade accepts nests a scope
// field deeper than a RetrievalPlan's filters; the bound is here so a body
// shaped by an attacker cannot turn an authorisation check into an unbounded
// walk.
const nestedScanDepth = 4

// bodyFieldLookup reads the request body once, hands the policy a view of the
// scope fields in it, and puts the body back for the handler to decode.
//
// It returns false when it has already written a response.
func bodyFieldLookup(w http.ResponseWriter, r *http.Request) (authz.FieldLookup, bool) {
	body, ok := bufferBody(w, r)
	if !ok {
		return nil, false
	}
	fields := map[string]any{}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &fields); err != nil {
			// Not an object, or not JSON at all. Reported as the caller's
			// mistake rather than as a scope denial, because a 403 saying
			// user_id was unset would send an operator looking at their key
			// file for a problem that is in their curl. The handler's decoder
			// would have said the same thing a moment later.
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("request body must be a JSON object to be checked against this key's scope: %v", err))
			return nil, false
		}
	}
	return func(field string) (string, []string, bool) {
		top, _ := fields[field].(string)
		nested := nestedValues(fields, field, nestedScanDepth)
		// encoding/json matches keys to struct fields case-insensitively, so
		// {"user_id":"a","USER_ID":"b"} is accepted by the handler and may
		// resolve to either copy. Any other spelling of the field is held to
		// the confinement exactly as a nested copy is.
		return top, append(nested, otherSpellings(fields, field)...), true
	}, true
}

// bufferBody reads the whole body so it can be inspected and then read again.
//
// The cap is the same one decodeBody enforces: this read happens first, so
// without it an unbounded body would be spent on the policy check before the
// handler ever got the chance to refuse it.
func bufferBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds the %d byte limit", maxRequestBytes))
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, true
}

// otherSpellings returns the non-empty string values an object carries under
// a case variant of field, excluding the exact spelling itself.
func otherSpellings(obj map[string]any, field string) []string {
	var out []string
	for k, v := range obj {
		if k == field || !strings.EqualFold(k, field) {
			continue
		}
		if s, _ := v.(string); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// nestedValues collects non-empty values of a scope field from everything
// nested inside an object, skipping that object's own copy of it.
//
// Nested copies are collected for the reason the gRPC side collects them: a
// user_id carried inside a retrieval plan's filters may be the one the facade
// ends up using, and rather than reason about which wins for each endpoint the
// policy refuses any nested value that disagrees with the confinement. That is
// correct whichever way the precedence goes.
func nestedValues(obj map[string]any, field string, depth int) []string {
	if depth <= 0 {
		return nil
	}
	var out []string
	for _, v := range obj {
		out = append(out, valuesIn(v, field, depth)...)
	}
	return out
}

// valuesIn reads the named field off one decoded JSON value and keeps
// descending. Arrays do not spend depth: they are containers of the same
// nesting level, and a list of plans is no deeper than one plan.
func valuesIn(v any, field string, depth int) []string {
	switch t := v.(type) {
	case map[string]any:
		var out []string
		if s, _ := t[field].(string); s != "" {
			out = append(out, s)
		}
		out = append(out, otherSpellings(t, field)...)
		return append(out, nestedValues(t, field, depth-1)...)
	case []any:
		var out []string
		for _, elem := range t {
			out = append(out, valuesIn(elem, field, depth)...)
		}
		return out
	default:
		return nil
	}
}

// oneNotFound spells every 404 a tool returns to a confined key the way the
// row guard spells a row the key may not have: "not found" and nothing else.
// The tool's own wording names the kind of row, or the store that missed it,
// and a confined key that could read it would tell a row that does not exist
// from a row it may not have: the oracle the guard exists to close. An
// unconfined key keeps the tool's wording, which is its diagnostic.
func oneNotFound(ctx context.Context, status int, message string) (int, string) {
	if key, ok := keyFrom(ctx); ok && !key.Scope.IsZero() && status == http.StatusNotFound {
		return status, authz.ErrWithheld.Error()
	}
	return status, message
}

// toolGuardStatus maps a row-guard failure to its HTTP status: a row the key
// may not have is 404, like one that does not exist; a call the guard refuses
// on its own is 403; anything else is the database failing, 500.
func toolGuardStatus(err error) int {
	switch {
	case errors.Is(err, authz.ErrWithheld):
		return http.StatusNotFound
	case errors.Is(err, authz.ErrDenied):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}
