package liveview

// History: the same questions as git log, asked of the graph and the ontology.
//
// Three read-only routes, each answered by one of the brain's own tools through
// [Source.Call], so a local file and a shared brain answer alike and the page
// asks what an agent asks:
//
//   - /api/history — graph_history: one entity's versions, grouped into one
//     line per (predicate, direction), with why the current one is in effect.
//   - /api/ontology/history — ontology_history: every saved version of a schema
//     and the intervals each was the active validator.
//   - /api/ontology/diff — ontology_diff between two stored versions.
//
// And the as_of parameter that /api/find and /api/expand take, which puts the
// whole question in the past: the page pinned to an instant must not fetch a
// neighbourhood as it is now and merge it into a scene drawn as it was.
//
// A malformed instant is a 400 here. /api/graph answers a bad as_of with a 200
// whose payload says the pin was refused, because that payload is what a page
// that is already drawing redraws from; these routes are fetched on a click,
// their answer is shown in a panel, and an error is exactly what the panel
// should print. Either way a bad instant is never read as now.
//
// None of it writes. The tools are on [ExploreTools], which a test holds
// against the catalogue's Mutates flag, and every route refuses any method but
// GET or HEAD.

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

const (
	historyLinesDefault = 20
	historyLinesMax     = 100
)

// asOfParam reads the optional as_of of a request: the zero time for none,
// an error for one that is not an instant. See [ParseAsOf] for the spellings.
func asOfParam(r *http.Request, name string) (time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := ParseAsOf(strings.ReplaceAll(raw, " ", "+"))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", name, err)
	}
	return t, nil
}

// instantText is an instant the way the tools take it.
func instantText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// readOnly refuses any method that could be read as a write.
func readOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusMethodNotAllowed)
	_, _ = w.Write([]byte(`{"error":"this route only reads"}` + "\n"))
	return false
}

// intParam reads an optional non-negative integer.
func intParam(r *http.Request, name string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s %q is not a non-negative integer", name, raw)
	}
	return n, nil
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	q := r.URL.Query()
	entity := strings.TrimSpace(q.Get("entity"))
	if entity == "" {
		writeBadRequest(w, errors.New("entity is required: a node id or a name"))
		return
	}
	req := cortexdb.GraphHistoryToolRequest{Entity: entity, Relation: strings.TrimSpace(q.Get("relation")), Cursor: q.Get("cursor")}
	for _, p := range []struct {
		name string
		to   *string
	}{{"as_of", &req.AsOf}, {"from", &req.From}, {"to", &req.To}} {
		t, err := asOfParam(r, p.name)
		if err != nil {
			writeBadRequest(w, err)
			return
		}
		*p.to = instantText(t)
	}
	var err error
	if req.Limit, err = intParam(r, "limit"); err != nil {
		writeBadRequest(w, err)
		return
	}
	if req.Limit == 0 {
		req.Limit = historyLinesDefault
	}
	req.Limit = min(req.Limit, historyLinesMax)
	if req.MaxVersions, err = intParam(r, "max_versions"); err != nil {
		writeBadRequest(w, err)
		return
	}

	ctx, cancel := s.exploreContext(r)
	defer cancel()
	var res cortexdb.GraphHistoryResult
	err = callInto(ctx, s.src.Call, "graph_history", req, &res)
	writeExplore(w, res, err)
}

func (s *Server) handleOntologyHistory(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	q := r.URL.Query()
	schema := strings.TrimSpace(q.Get("schema"))
	if schema == "" {
		writeBadRequest(w, errors.New("schema is required"))
		return
	}
	req := cortexdb.OntologyHistoryRequest{SchemaID: schema}
	var err error
	if req.Limit, err = intParam(r, "limit"); err != nil {
		writeBadRequest(w, err)
		return
	}
	if req.Offset, err = intParam(r, "offset"); err != nil {
		writeBadRequest(w, err)
		return
	}
	req.OldestFirst = q.Get("oldest_first") == "1" || q.Get("oldest_first") == "true"

	ctx, cancel := s.exploreContext(r)
	defer cancel()
	var res cortexdb.OntologyHistoryResponse
	err = callInto(ctx, s.src.Call, "ontology_history", req, &res)
	writeExplore(w, res, err)
}

func (s *Server) handleOntologyDiff(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	q := r.URL.Query()
	schema := strings.TrimSpace(q.Get("schema"))
	if schema == "" {
		writeBadRequest(w, errors.New("schema is required"))
		return
	}
	from, err := intParam(r, "from")
	if err != nil || from == 0 {
		writeBadRequest(w, errors.New("from must be a stored version number, 1 or more"))
		return
	}
	to, err := intParam(r, "to")
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	// Spelled out rather than OntologyDiffRequest: that type carries a
	// candidate schema, and a route that forwarded one would compare a
	// definition the caller made up. Versions only.
	args := struct {
		SchemaID    string `json:"schema_id"`
		FromVersion int    `json:"from_version"`
		ToVersion   int    `json:"to_version,omitempty"`
	}{schema, from, to}

	ctx, cancel := s.exploreContext(r)
	defer cancel()
	var res cortexdb.OntologyDiffResponse
	err = callInto(ctx, s.src.Call, "ontology_diff", args, &res)
	if err == nil && res.Diff.Changes == nil {
		res.Diff.Changes = []cortexdb.OntologyChange{}
	}
	writeExplore(w, res, err)
}
