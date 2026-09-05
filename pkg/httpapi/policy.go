package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"io"
	"net/http"
	"strings"

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
	if !keys.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyContext{}, key)))
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
				// is refused every tool: arguments are opaque JSON no scope can
				// be checked against, which is also gRPC's rule.
				tool, _ := strings.CutPrefix(r.URL.Path, toolCallPrefix)
				name = "POST " + toolCallPrefix + tool
				access, known := authz.LookupTool(tool)
				if !known {
					writeError(w, http.StatusForbidden, "denied: "+tool+" is not a tool this server defines")
					return
				}
				if !key.Scope.IsZero() {
					writeError(w, http.StatusForbidden, "denied: key \""+key.ID+"\" is confined and a tool's arguments are opaque JSON no scope can be checked against")
					return
				}
				m = authz.Method{Access: access, Rowless: true}
				if tool == sparqlToolName && access == authz.Write && db != nil {
					if q, ok := peekJSONString(w, r, "query"); ok && !db.Graph().SPARQLMutates(r.Context(), q) {
						m.Access = authz.Read
					}
				}
			}
			if err := key.AuthorizeOperation(name, m); err != nil {
				writeError(w, http.StatusForbidden, err.Error())
				return
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
			next(w, r)
		}
	}
}

// sparqlToolName is the one tool whose classification is decided per call.
const sparqlToolName = "knowledge_graph_query"

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
		return top, nestedValues(fields, field, nestedScanDepth), true
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
