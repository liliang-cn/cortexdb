package liveview

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
)

// Pages a host can leave out.
//
// A process that mounts the view as one page of its own product (New and
// Handler under a prefix) does not always want every page the view has: a
// back office with no recall log has nothing to show on Recalls, and an
// Ontology tab that says it cannot be asked is noise to people who will never
// ask it. WithoutPages names the pages to leave out.
//
// A page left out is absent, not present and empty. It is gone from the
// navigation and the command palette, its path answers 404, the pages that
// stay draw no link to it, and the API routes that only it reads answer 404
// too, so what the host left out is not one fetch away. A route two pages
// read — /api/ontology serves the Ontology page and the Versions page's list
// of schemas — goes only when both do.
//
// The graph and the entity page cannot be left out. The graph is the view,
// and the entity page is where the graph's inspector sends a node for its
// whole history.

// Page names one of the view's pages a host can leave out. Its value is the
// page's path under the view's root.
type Page string

const (
	// PageChanges is the change feed, every write to the store. Its API stays:
	// the graph's inspector and the entity page read a node's changes from it.
	PageChanges Page = "changes"
	// PageActivity is the tool calls the view observed.
	PageActivity Page = "activity"
	// PageRecalls is the recall log. Leaving it out also takes the graph's
	// colour-by-recalls mode and replaying a recall on the graph, which read
	// the same log.
	PageRecalls Page = "recalls"
	// PageLibrary is the brain as a reading room.
	PageLibrary Page = "library"
	// PageOntology is the declared model, drawn.
	PageOntology Page = "ontology"
	// PageVersions is every saved version of a schema and what changed
	// between them; "Versions" in the navigation.
	PageVersions Page = "ontology-history"
)

// optionalPages is every page WithoutPages accepts.
var optionalPages = map[Page]bool{
	PageChanges: true, PageActivity: true, PageRecalls: true,
	PageLibrary: true, PageOntology: true, PageVersions: true,
}

// Option configures a view built by New or Start.
type Option func(*options)

type options struct {
	hidden []Page
}

// WithoutPages leaves the named pages out of the view. New refuses a name
// that is not one of the Page constants, rather than serving every page while
// the host believes one is hidden.
func WithoutPages(pages ...Page) Option {
	return func(o *options) { o.hidden = append(o.hidden, pages...) }
}

// hiddenPages checks the pages an option list leaves out.
func hiddenPages(opts []Option) (map[Page]bool, error) {
	var o options
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	hidden := make(map[Page]bool, len(o.hidden))
	for _, p := range o.hidden {
		if !optionalPages[p] {
			return nil, fmt.Errorf("live view: %q is not a page that can be left out", string(p))
		}
		hidden[p] = true
	}
	return hidden, nil
}

// HiddenPages reports the pages this view leaves out, sorted.
func (s *Server) HiddenPages() []Page {
	out := make([]Page, 0, len(s.hidden))
	for p := range s.hidden {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// pageShown reports whether the app route at path is part of this view.
func (s *Server) pageShown(path string) bool {
	return !s.hidden[Page(strings.TrimPrefix(path, "/"))]
}

// serves wraps an API route that only the given pages read: it answers 404
// once every one of them is left out.
func (s *Server) serves(h http.HandlerFunc, pages ...Page) http.HandlerFunc {
	for _, p := range pages {
		if !s.hidden[p] {
			return h
		}
	}
	return handleLeftOut
}

func handleLeftOut(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "this view leaves out the page " + r.URL.Path + " serves"})
}

// hiddenPagesMeta is the tag that tells the app which pages are left out. It
// is written into the index beside the base tag, so the navigation is right on
// the first paint rather than after the stream's first message.
func hiddenPagesMeta(hidden []Page) string {
	if len(hidden) == 0 {
		return ""
	}
	names := make([]string, len(hidden))
	for i, p := range hidden {
		names[i] = string(p)
	}
	return `<meta name="cortexdb-hidden-pages" content="` + html.EscapeString(strings.Join(names, " ")) + `">`
}
