package httpapi

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// toolCallPrefix is the one dynamic segment in the whole API: everything after
// it is a tool name handed straight to the toolbox.
const toolCallPrefix = "/v1/tools/"

// router dispatches on an exact path plus a method.
//
// http.ServeMux can do this, and would answer a wrong method with 405 on its
// own — but in plain text, and the moment a catch-all is registered to make
// unknown routes answer in JSON, every wrong-method request matches the
// catch-all and the 405 turns back into a 404. Forty lines of explicit table is
// worth being able to say exactly which of the two a caller gets.
type router struct {
	routes map[string]map[string]http.HandlerFunc
	tool   http.HandlerFunc
}

func newRouter() *router {
	return &router{routes: make(map[string]map[string]http.HandlerFunc)}
}

func (rt *router) handle(method, path string, h http.HandlerFunc) {
	byMethod, ok := rt.routes[path]
	if !ok {
		byMethod = make(map[string]http.HandlerFunc)
		rt.routes[path] = byMethod
	}
	byMethod[method] = h
}

// handleTool registers the handler for POST /v1/tools/{name}.
func (rt *router) handleTool(h http.HandlerFunc) { rt.tool = h }

// registered lists every route the router will dispatch to, the tool endpoint
// included under its template path.
//
// It reads the dispatch tables themselves rather than a list maintained beside
// them, which is the whole value: the coverage test walks this, so a route that
// registerRoutes adds and the access table does not classify cannot hide from
// it. A list written by hand would only ever prove that somebody remembered to
// update the list.
func (rt *router) registered() []route {
	var out []route
	for path, byMethod := range rt.routes {
		for method := range byMethod {
			out = append(out, route{method: method, path: path})
		}
	}
	if rt.tool != nil {
		out = append(out, route{method: http.MethodPost, path: toolRoutePath})
	}
	slices.SortFunc(out, func(a, b route) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// wrapEach replaces every registered handler with wrap's result for that route,
// so a wrapper can depend on which route it is guarding. Registration happens
// first and wrapping second precisely so nothing can be registered afterwards
// and miss the wrapper.
func (rt *router) wrapEach(wrap func(rr route, h http.HandlerFunc) http.HandlerFunc) {
	for path, byMethod := range rt.routes {
		for method, h := range byMethod {
			byMethod[method] = wrap(route{method: method, path: path}, h)
		}
	}
	if rt.tool != nil {
		rt.tool = wrap(route{method: http.MethodPost, path: toolRoutePath}, rt.tool)
	}
}

func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if name, ok := strings.CutPrefix(r.URL.Path, toolCallPrefix); ok {
		rt.serveTool(w, r, name)
		return
	}
	byMethod, ok := rt.routes[r.URL.Path]
	if !ok {
		notFound(w, r)
		return
	}
	h, ok := byMethod[r.Method]
	if !ok {
		// The verbs come from the table rather than from a list maintained
		// beside it, so Allow cannot drift from what the route really has.
		methodNotAllowed(w, slices.Collect(maps.Keys(byMethod)))
		return
	}
	h(w, r)
}

func (rt *router) serveTool(w http.ResponseWriter, r *http.Request, name string) {
	// A tool name is one path segment. A slash in it is not a tool nobody
	// registered, it is a different URL shape, and 404 says so more honestly
	// than an "unknown tool" naming half a path.
	if rt.tool == nil || name == "" || strings.Contains(name, "/") {
		notFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, []string{http.MethodPost})
		return
	}
	r.SetPathValue("tool", name)
	rt.tool(w, r)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, fmt.Sprintf("no route for %s %s", r.Method, r.URL.Path))
}

func methodNotAllowed(w http.ResponseWriter, allowed []string) {
	slices.Sort(allowed)
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeError(w, http.StatusMethodNotAllowed,
		"method not allowed; this route accepts "+strings.Join(allowed, ", "))
}
