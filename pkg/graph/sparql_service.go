package graph

// SPARQL 1.1 Federated Query: SERVICE.
//
// SERVICE <endpoint> { P } sends P to another SPARQL endpoint and joins what
// comes back. The store has no business opening network connections on a
// query's say-so — the endpoint is whatever IRI the query names — so SERVICE
// answers only through a SPARQLServiceFunc the embedding application sets,
// which decides which endpoints exist and how to reach them. Without one,
// SERVICE fails, and SERVICE SILENT contributes the empty solution, as the
// spec says a silent failure does. HTTPSPARQLService is a SPARQL 1.1
// Protocol client to use as, or inside, that function.
//
// The endpoint receives P as written in the query, inside SELECT * WHERE,
// with the query's prefixes and base, so it is evaluated there exactly as the
// author wrote it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// SPARQLServiceFunc evaluates query, a SELECT, at endpoint and returns its
// solutions.
type SPARQLServiceFunc func(ctx context.Context, endpoint, query string) (*SPARQLResult, error)

// SetSPARQLServiceHandler sets how SERVICE clauses are answered; nil turns
// SERVICE off again.
func (g *GraphStore) SetSPARQLServiceHandler(fn SPARQLServiceFunc) {
	if fn == nil {
		g.service.Store(nil)
		return
	}
	g.service.Store(&fn)
}

type sparqlServiceStep struct {
	Endpoint sparqlTermPattern
	Silent   bool
	// Query is the SELECT sent to the endpoint; Group is the same pattern
	// parsed, for the variables it can bind.
	Query string
	Group sparqlGroup
}

func (sparqlServiceStep) sparqlStep() {}

func (p *sparqlParser) parseService(prefixes map[string]string) (sparqlStep, error) {
	step := sparqlServiceStep{Silent: p.matchWord("SILENT")}
	endpoint, err := p.parseTermPattern(prefixes, false)
	if err != nil {
		return nil, err
	}
	if endpoint.Variable == "" && (endpoint.Term == nil || endpoint.Term.Kind != RDFTermIRI) {
		return nil, fmt.Errorf("SERVICE takes an IRI or a variable")
	}
	step.Endpoint = endpoint
	open := p.peek()
	if open.Type != sparqlTokenPunct || open.Value != "{" {
		return nil, fmt.Errorf("expected { after SERVICE")
	}
	// The group is parsed to check it, then sent as text.
	group, err := p.parseEnclosedGroup(nil, prefixes)
	if err != nil {
		return nil, err
	}
	step.Group = group
	closing := p.tokens[p.position-1]
	var q strings.Builder
	if p.base != "" {
		fmt.Fprintf(&q, "BASE <%s>\n", p.base)
	}
	names := make([]string, 0, len(prefixes))
	for name := range prefixes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&q, "PREFIX %s: <%s>\n", name, prefixes[name])
	}
	q.WriteString("SELECT * WHERE ")
	q.WriteString(p.src[open.Start : closing.Start+1])
	step.Query = q.String()
	return step, nil
}

func (g *GraphStore) executeSPARQLService(ctx context.Context, step sparqlServiceStep, current []map[string]RDFTerm) ([]map[string]RDFTerm, error) {
	handler := g.service.Load()
	byEndpoint := map[string][]map[string]RDFTerm{}
	var order []string
	for _, row := range current {
		endpoint := ""
		switch {
		case step.Endpoint.Term != nil:
			endpoint = step.Endpoint.Term.Value
		default:
			if v, ok := row[step.Endpoint.Variable]; ok && v.Kind == RDFTermIRI {
				endpoint = v.Value
			}
		}
		if _, seen := byEndpoint[endpoint]; !seen {
			order = append(order, endpoint)
		}
		byEndpoint[endpoint] = append(byEndpoint[endpoint], row)
	}
	var out []map[string]RDFTerm
	for _, endpoint := range order {
		rows := byEndpoint[endpoint]
		var (
			result *SPARQLResult
			err    error
		)
		switch {
		case endpoint == "":
			err = fmt.Errorf("SERVICE ?%s is not bound to an IRI", step.Endpoint.Variable)
		case handler == nil:
			err = fmt.Errorf("SERVICE <%s>: no SPARQL service handler is set (GraphStore.SetSPARQLServiceHandler)", endpoint)
		default:
			result, err = (*handler)(ctx, endpoint, step.Query)
		}
		if err != nil {
			if !step.Silent {
				return nil, err
			}
			out = append(out, rows...) // joined with the empty solution
			continue
		}
		for _, o := range rows {
			for _, i := range result.Bindings {
				if merged, ok := mergeValueRow(o, i); ok {
					out = append(out, merged)
				}
			}
		}
	}
	return out, nil
}

// maxServiceResponseBytes bounds what HTTPSPARQLService reads from an
// endpoint: the answer is held in memory to be joined, and an endpoint is
// whatever the query names.
const maxServiceResponseBytes = 64 << 20

// HTTPSPARQLService is a SPARQL 1.1 Protocol client: it POSTs the query to
// the endpoint and reads SPARQL JSON results, at most 64MB of them. client
// nil is http.DefaultClient. Wrap it to restrict which endpoints may be
// reached.
func HTTPSPARQLService(client *http.Client) SPARQLServiceFunc {
	if client == nil {
		client = http.DefaultClient
	}
	return func(ctx context.Context, endpoint, query string) (*SPARQLResult, error) {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("SERVICE <%s>: not an http(s) endpoint", endpoint)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(query))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/sparql-query")
		req.Header.Set("Accept", SPARQLResultsJSON.MediaType())
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("SERVICE <%s>: %w", endpoint, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode/100 != 2 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("SERVICE <%s>: %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxServiceResponseBytes+1))
		if err != nil {
			return nil, fmt.Errorf("SERVICE <%s>: %w", endpoint, err)
		}
		if len(body) > maxServiceResponseBytes {
			return nil, fmt.Errorf("SERVICE <%s>: the answer is larger than %d bytes", endpoint, maxServiceResponseBytes)
		}
		return ReadSPARQLResultsJSON(bytes.NewReader(body))
	}
}

// ReadSPARQLResultsJSON reads a SPARQL JSON results document.
func ReadSPARQLResultsJSON(r io.Reader) (*SPARQLResult, error) {
	var doc struct {
		Head struct {
			Vars []string `json:"vars"`
		} `json:"head"`
		Boolean *bool `json:"boolean"`
		Results struct {
			Bindings []map[string]json.RawMessage `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("read SPARQL JSON results: %w", err)
	}
	if doc.Boolean != nil {
		return &SPARQLResult{QueryType: SPARQLQueryAsk, Boolean: *doc.Boolean, Count: boolCount(*doc.Boolean)}, nil
	}
	out := &SPARQLResult{QueryType: SPARQLQuerySelect, Vars: doc.Head.Vars}
	for _, binding := range doc.Results.Bindings {
		row := make(map[string]RDFTerm, len(binding))
		for name, raw := range binding {
			term, err := decodeJSONResultTerm(raw)
			if err != nil {
				return nil, fmt.Errorf("read SPARQL JSON results: ?%s: %w", name, err)
			}
			row[name] = term
		}
		out.Bindings = append(out.Bindings, row)
	}
	out.Count = len(out.Bindings)
	return out, nil
}

func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

func decodeJSONResultTerm(raw json.RawMessage) (RDFTerm, error) {
	var t struct {
		Type     string          `json:"type"`
		Value    json.RawMessage `json:"value"`
		Lang     string          `json:"xml:lang"`
		Dir      string          `json:"its:dir"`
		Datatype string          `json:"datatype"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return RDFTerm{}, err
	}
	if t.Type == "triple" {
		var parts struct {
			Subject, Predicate, Object json.RawMessage
		}
		if err := json.Unmarshal(t.Value, &parts); err != nil {
			return RDFTerm{}, err
		}
		var terms [3]RDFTerm
		for i, part := range []json.RawMessage{parts.Subject, parts.Predicate, parts.Object} {
			term, err := decodeJSONResultTerm(part)
			if err != nil {
				return RDFTerm{}, err
			}
			terms[i] = term
		}
		return NewTripleTerm(terms[0], terms[1], terms[2])
	}
	var value string
	if err := json.Unmarshal(t.Value, &value); err != nil {
		return RDFTerm{}, err
	}
	switch t.Type {
	case "uri":
		return NewIRI(value), nil
	case "bnode":
		return NewBlankNode(value), nil
	case "literal", "typed-literal":
		switch {
		case t.Lang != "":
			return NewDirLangLiteral(value, t.Lang, t.Dir), nil
		case t.Datatype != "" && t.Datatype != XSDNamespace+"string":
			return NewTypedLiteral(value, t.Datatype), nil
		}
		return NewLiteral(value), nil
	}
	return RDFTerm{}, fmt.Errorf("unknown term type %q", t.Type)
}
