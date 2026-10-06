package liveview

// The library: the same brain, drawn as a reading room.
//
// The graph page answers "what is connected to what". A person who comes to a
// memory store with a question about their own work usually means something
// plainer — where did I write that down, what else is on that shelf — and a
// force layout cannot answer it, because a node's position there means nothing
// beyond "something pulled it here". The library gives position a meaning:
//
//   - A memory is a book. It is shelved under the project it names, and when it
//     names several, under the most specific one: the project the fewest other
//     memories name. "CortexDB" is named by everything; the one other project a
//     memory names is what it is actually about.
//   - Shelves that belong together stand in one wing. Wings are communities of
//     the project graph — the projects' own relations (depends_on, uses,
//     part_of) plus every memory that names two of them — partitioned with the
//     same Leiden the rest of the module uses, so the library and the graph
//     page cannot disagree about what belongs with what.
//   - A memory that names no project goes to the general stacks. That is most
//     memories on a real brain, and the stacks are drawn as big as they are:
//     shrinking them would hide the finding that most of what is remembered is
//     filed under nothing.
//   - Everything else a memory mentions is an index card. A card is opened from
//     the catalogue and shows every book that mentions it, which is how an
//     entity that cuts across half the library looks like one.
//   - A book's thickness is how many things it mentions: its index, not its
//     word count, because the index is what the graph actually holds.
//
// Everything here is computed from the snapshot the graph page already polls,
// so the library costs no read of its own and changes when the graph does.
// Documents are the exception: the snapshot leaves them out on purpose (they
// are bookkeeping, see graph.BookkeepingNodeTypes), so the reference room asks
// for them through the page's read-only tools, and says it could not when the
// source has none.

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// LibraryBook is one memory on a shelf.
type LibraryBook struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Terms is how many distinct things the memory mentions: the size of its
	// index, and the book's thickness.
	Terms int `json:"terms"`
}

// LibraryShelf is one project's books.
type LibraryShelf struct {
	ID      string        `json:"id"`
	Project string        `json:"project"`
	Books   []LibraryBook `json:"books"`
}

// LibraryWing is a community of shelves, named after its largest.
type LibraryWing struct {
	Name    string         `json:"name"`
	Books   int            `json:"books"`
	Shelves []LibraryShelf `json:"shelves"`
}

// LibraryDoc is one document in the reference room.
type LibraryDoc struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Chunks int    `json:"chunks"`
}

// Library is the whole reading room, largest wing first.
type Library struct {
	Version int64         `json:"version"`
	Source  string        `json:"source"`
	Wings   []LibraryWing `json:"wings"`
	// Annex holds the shelves of wings too small to stand on their own — one
	// project with a couple of books does not need a wing with a plaque.
	Annex []LibraryShelf `json:"annex"`
	// Stacks are the memories that name no project.
	Stacks []LibraryBook `json:"stacks"`
	// Cards counts the index cards: every node that is not a memory.
	Cards int `json:"cards"`
	// Docs is the reference room. DocsKnown separates "this source cannot be
	// asked for documents" from "it has none", which are different findings.
	Docs      []LibraryDoc `json:"docs"`
	DocsKnown bool         `json:"docs_known"`
	// Records says a book can be opened: its text read through the source.
	Records bool `json:"records"`
}

// LibraryCard is an opened index card: what mentions it, and what it is
// related to.
type LibraryCard struct {
	ID        string            `json:"id"`
	Label     string            `json:"label"`
	Type      string            `json:"type"`
	Found     bool              `json:"found"`
	Books     []string          `json:"books"`
	Relations []LibraryRelation `json:"relations"`
	// More is how many relations were left out past the cap.
	More int `json:"more,omitempty"`
}

// LibraryRelation is one relation on a card, from the card's side.
type LibraryRelation struct {
	Rel   string `json:"rel"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"`
	// Out is true when the card is the relation's subject.
	Out bool `json:"out"`
}

// LibraryPage is the back of an opened book: its index and what to read next.
type LibraryPage struct {
	ID      string           `json:"id"`
	Label   string           `json:"label"`
	Found   bool             `json:"found"`
	Index   []LibraryTerm    `json:"index"`
	SeeAlso []LibrarySeeAlso `json:"see_also"`
	// Record is the book's text and provenance, through the source's Record
	// hook — the same answer the graph page's inspector gets.
	Record RecordDetail `json:"record"`
}

// LibraryTerm is one entry in a book's index.
type LibraryTerm struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Type    string `json:"type"`
	Project bool   `json:"project,omitempty"`
}

// LibrarySeeAlso is another book sharing index terms with this one.
type LibrarySeeAlso struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Shared int    `json:"shared"`
}

const (
	// mentionsRel is the edge a memory names things by.
	mentionsRel = "mentions"
	// annexBooks is the size under which a wing is folded into the annex.
	annexBooks = 6
	// cardRelations caps the relations an opened card carries. A hub entity
	// has hundreds; a table holds a dozen and a reader follows one.
	cardRelations = 40
	// seeAlsoBooks is how many related books the back page lists.
	seeAlsoBooks = 4
	// wingResolution is the Leiden resolution the wings are cut at. At the
	// standard 1.0 one project everything names (CortexDB, on the brain this
	// was tuned on) pulls the projects that merely use it into its wing, and a
	// third of the library ends up behind one plaque; at 1.6 families that
	// belong together (a project and the services built on it) split apart.
	wingResolution = 1.3
)

// projectLinks are the relations that tie two projects into one wing, and
// how strongly. A declared dependency is a stronger statement than two
// projects being named in the same memory.
var projectLinks = map[string]float64{"depends_on": 3, "uses": 3, "part_of": 3}

func isMemory(n Node) bool  { return n.Type == "memory" }
func isProject(n Node) bool { return strings.EqualFold(n.Type, "project") }

// BuildLibrary shelves a snapshot.
func BuildLibrary(nodes []Node, edges []Edge) Library {
	byID := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}

	// What each memory mentions, and which projects among those.
	terms := map[string]map[string]bool{}
	named := map[string][]string{}
	for _, e := range edges {
		if e.Label != mentionsRel {
			continue
		}
		from, ok := byID[e.Source]
		if !ok || !isMemory(from) {
			continue
		}
		to, ok := byID[e.Target]
		if !ok || to.ID == from.ID {
			continue
		}
		if terms[from.ID] == nil {
			terms[from.ID] = map[string]bool{}
		}
		if terms[from.ID][to.ID] {
			continue
		}
		terms[from.ID][to.ID] = true
		if isProject(to) {
			named[from.ID] = append(named[from.ID], to.ID)
		}
	}

	// Most specific first: the project the fewest memories name.
	reach := map[string]int{}
	for _, ps := range named {
		for _, p := range ps {
			reach[p]++
		}
	}
	home := map[string]string{}
	for m, ps := range named {
		best := ps[0]
		for _, p := range ps[1:] {
			if reach[p] < reach[best] || (reach[p] == reach[best] && byID[p].Label < byID[best].Label) {
				best = p
			}
		}
		home[m] = best
	}

	lib := Library{}
	shelves := map[string]*LibraryShelf{}
	for _, n := range nodes {
		if !isMemory(n) {
			lib.Cards++
			continue
		}
		b := LibraryBook{ID: n.ID, Label: n.Label, Terms: len(terms[n.ID])}
		p, ok := home[n.ID]
		if !ok {
			lib.Stacks = append(lib.Stacks, b)
			continue
		}
		sh := shelves[p]
		if sh == nil {
			sh = &LibraryShelf{ID: p, Project: byID[p].Label}
			shelves[p] = sh
		}
		sh.Books = append(sh.Books, b)
	}
	byLabel := func(bs []LibraryBook) {
		sort.Slice(bs, func(i, j int) bool {
			if bs[i].Label != bs[j].Label {
				return bs[i].Label < bs[j].Label
			}
			return bs[i].ID < bs[j].ID
		})
	}
	byLabel(lib.Stacks)
	for _, sh := range shelves {
		byLabel(sh.Books)
	}

	// The project graph over the shelves, partitioned into wings.
	ids := make([]string, 0, len(shelves))
	for id := range shelves {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	index := make(map[string]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}
	var wedges []graph.WeightedEdge
	link := func(a, b string, w float64) {
		ia, oka := index[a]
		ib, okb := index[b]
		if oka && okb && ia != ib {
			wedges = append(wedges, graph.WeightedEdge{From: ia, To: ib, Weight: w})
		}
	}
	for _, e := range edges {
		if w, ok := projectLinks[e.Label]; ok {
			link(e.Source, e.Target, w)
		}
	}
	for _, ps := range named {
		for i := range ps {
			for j := i + 1; j < len(ps); j++ {
				link(ps[i], ps[j], 1)
			}
		}
	}
	groups := [][]string{}
	if h := graph.LeidenHierarchy(ids, wedges, graph.HierarchyOptions{MaxLevels: 1, Resolution: wingResolution}); h != nil && len(h.Levels) > 0 {
		for _, c := range h.Levels[0].Communities {
			groups = append(groups, c.Nodes)
		}
	} else {
		for _, id := range ids {
			groups = append(groups, []string{id})
		}
	}

	for _, g := range groups {
		w := LibraryWing{}
		for _, id := range g {
			sh := shelves[id]
			w.Shelves = append(w.Shelves, *sh)
			w.Books += len(sh.Books)
		}
		sort.Slice(w.Shelves, func(i, j int) bool {
			if len(w.Shelves[i].Books) != len(w.Shelves[j].Books) {
				return len(w.Shelves[i].Books) > len(w.Shelves[j].Books)
			}
			return w.Shelves[i].Project < w.Shelves[j].Project
		})
		if w.Books < annexBooks {
			lib.Annex = append(lib.Annex, w.Shelves...)
			continue
		}
		w.Name = w.Shelves[0].Project
		lib.Wings = append(lib.Wings, w)
	}
	sort.Slice(lib.Wings, func(i, j int) bool {
		if lib.Wings[i].Books != lib.Wings[j].Books {
			return lib.Wings[i].Books > lib.Wings[j].Books
		}
		return lib.Wings[i].Name < lib.Wings[j].Name
	})
	sort.Slice(lib.Annex, func(i, j int) bool {
		if len(lib.Annex[i].Books) != len(lib.Annex[j].Books) {
			return len(lib.Annex[i].Books) > len(lib.Annex[j].Books)
		}
		return lib.Annex[i].Project < lib.Annex[j].Project
	})
	if lib.Wings == nil {
		lib.Wings = []LibraryWing{}
	}
	return lib
}

// BuildCard opens one index card.
func BuildCard(nodes []Node, edges []Edge, id string) LibraryCard {
	byID := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	card := LibraryCard{ID: id, Books: []string{}, Relations: []LibraryRelation{}}
	n, ok := byID[id]
	if !ok {
		return card
	}
	card.Found, card.Label, card.Type = true, n.Label, n.Type
	seenBook := map[string]bool{}
	seenRel := map[string]bool{}
	for _, e := range edges {
		if e.Source != id && e.Target != id {
			continue
		}
		other := e.Target
		if e.Target == id {
			other = e.Source
		}
		o, ok := byID[other]
		if !ok || other == id {
			continue
		}
		if e.Label == mentionsRel && isMemory(o) {
			if !seenBook[other] {
				seenBook[other] = true
				card.Books = append(card.Books, other)
			}
			continue
		}
		key := e.Label + "\x00" + other + "\x00" + boolKey(e.Source == id)
		if seenRel[key] {
			continue
		}
		seenRel[key] = true
		card.Relations = append(card.Relations, LibraryRelation{Rel: e.Label, ID: other, Label: o.Label, Type: o.Type, Out: e.Source == id})
	}
	sort.Strings(card.Books)
	sort.Slice(card.Relations, func(i, j int) bool {
		a, b := card.Relations[i], card.Relations[j]
		// Relations the graph was told about come before the ones it only
		// noticed (co_occurs), and among those the order is for reading.
		if (a.Rel == "co_occurs") != (b.Rel == "co_occurs") {
			return b.Rel == "co_occurs"
		}
		if a.Rel != b.Rel {
			return a.Rel < b.Rel
		}
		return a.Label < b.Label
	})
	if len(card.Relations) > cardRelations {
		card.More = len(card.Relations) - cardRelations
		card.Relations = card.Relations[:cardRelations]
	}
	return card
}

func boolKey(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// BuildPage turns to the back of a book: its index and its nearest books.
func BuildPage(nodes []Node, edges []Edge, id string) LibraryPage {
	byID := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	page := LibraryPage{ID: id, Index: []LibraryTerm{}, SeeAlso: []LibrarySeeAlso{}}
	n, ok := byID[id]
	if !ok {
		return page
	}
	page.Found, page.Label = true, n.Label

	mine := map[string]bool{}
	for _, e := range edges {
		if e.Label != mentionsRel || e.Source != id || mine[e.Target] {
			continue
		}
		t, ok := byID[e.Target]
		if !ok {
			continue
		}
		mine[e.Target] = true
		page.Index = append(page.Index, LibraryTerm{ID: t.ID, Label: t.Label, Type: t.Type, Project: isProject(t)})
	}
	sort.Slice(page.Index, func(i, j int) bool {
		if page.Index[i].Project != page.Index[j].Project {
			return page.Index[i].Project
		}
		return strings.ToLower(page.Index[i].Label) < strings.ToLower(page.Index[j].Label)
	})

	// Other books, by how many index terms they share with this one.
	shared := map[string]int{}
	counted := map[string]bool{}
	for _, e := range edges {
		if e.Label != mentionsRel || !mine[e.Target] || e.Source == id {
			continue
		}
		key := e.Source + "\x00" + e.Target
		if counted[key] {
			continue
		}
		counted[key] = true
		if o, ok := byID[e.Source]; ok && isMemory(o) {
			shared[e.Source]++
		}
	}
	for other, k := range shared {
		page.SeeAlso = append(page.SeeAlso, LibrarySeeAlso{ID: other, Label: byID[other].Label, Shared: k})
	}
	sort.Slice(page.SeeAlso, func(i, j int) bool {
		if page.SeeAlso[i].Shared != page.SeeAlso[j].Shared {
			return page.SeeAlso[i].Shared > page.SeeAlso[j].Shared
		}
		return page.SeeAlso[i].Label < page.SeeAlso[j].Label
	})
	if len(page.SeeAlso) > seeAlsoBooks {
		page.SeeAlso = page.SeeAlso[:seeAlsoBooks]
	}
	return page
}

// libraryDocs asks the source for the reference room's documents.
func libraryDocs(ctx context.Context, call Caller) ([]LibraryDoc, error) {
	args, _ := json.Marshal(map[string]any{
		"query":    "MATCH (d:document) OPTIONAL MATCH (d)-[:has_chunk]->(c) RETURN id(d), coalesce(d.title, d.name, id(d)), count(c)",
		"max_rows": 2000,
	})
	raw, err := call(ctx, "graph_cypher_query", args)
	if err != nil {
		return nil, err
	}
	var out struct {
		Rows [][]any `json:"rows"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	docs := make([]LibraryDoc, 0, len(out.Rows))
	for _, r := range out.Rows {
		if len(r) < 3 {
			continue
		}
		id, _ := r[0].(string)
		title, _ := r[1].(string)
		n, _ := r[2].(float64)
		docs = append(docs, LibraryDoc{ID: id, Title: title, Chunks: int(n)})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Title < docs[j].Title })
	return docs, nil
}

func (s *Server) handleLibraryPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(libraryHTML))
}

// handleLibrary answers the library page's opening frame.
func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	snap := s.hub.snapshot()
	lib := BuildLibrary(snap.Nodes, snap.Edges)
	lib.Version = snap.Version
	lib.Source = s.src.Describe
	lib.Records = s.src.Record != nil
	lib.Docs = []LibraryDoc{}
	if s.src.Call != nil {
		ctx, cancel := context.WithTimeout(r.Context(), exploreTimeout)
		defer cancel()
		if docs, err := libraryDocs(ctx, s.src.Call); err == nil {
			lib.Docs, lib.DocsKnown = docs, true
		}
	}
	_ = json.NewEncoder(w).Encode(lib)
}

// handleLibraryCard opens an index card.
func (s *Server) handleLibraryCard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	snap := s.hub.snapshot()
	_ = json.NewEncoder(w).Encode(BuildCard(snap.Nodes, snap.Edges, strings.TrimSpace(r.URL.Query().Get("id"))))
}

// handleLibraryBook opens a book: its back page from the snapshot, its text
// from the source.
func (s *Server) handleLibraryBook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	snap := s.hub.snapshot()
	page := BuildPage(snap.Nodes, snap.Edges, id)
	switch {
	case s.src.Record == nil:
		page.Record = unavailableRecord("this view's source cannot look a record up")
	case id == "":
		page.Record = unavailableRecord("no record id given")
	default:
		got, err := s.src.Record(r.Context(), id)
		if err != nil {
			page.Record = unavailableRecord(err.Error())
		} else {
			page.Record = got
		}
		page.Record.ID = id
	}
	_ = json.NewEncoder(w).Encode(page)
}
