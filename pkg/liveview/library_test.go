package liveview

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// libraryFixture is a small brain: two families of projects joined by their
// own relations, memories naming them, and memories naming no project at all.
func libraryFixture() ([]Node, []Edge) {
	nodes := []Node{
		{ID: "entity:cortexdb", Label: "CortexDB", Type: "project"},
		{ID: "entity:alchemy", Label: "alchemy", Type: "project"},
		{ID: "entity:sds", Label: "SDS", Type: "project"},
		{ID: "entity:drbd", Label: "DRBD", Type: "project"},
		{ID: "entity:qilang", Label: "qilang", Type: "project"},
		{ID: "entity:openclaw", Label: "openclaw", Type: "service"},
		{ID: "entity:node-b", Label: "node-b", Type: "host"},
	}
	var edges []Edge
	mem := func(id string, names ...string) {
		nodes = append(nodes, Node{ID: "memory:" + id, Label: "about " + id, Type: "memory"})
		for _, n := range names {
			edges = append(edges, Edge{Source: "memory:" + id, Target: "entity:" + n, Label: "mentions"})
		}
	}
	// Seven memories name CortexDB alone; one names CortexDB and alchemy, so it
	// is shelved under alchemy, the more specific of the two.
	for i := range 7 {
		mem(fmt.Sprintf("cdb-%d", i), "cortexdb")
	}
	for i := range 4 {
		mem(fmt.Sprintf("alc-%d", i), "alchemy")
	}
	mem("both", "cortexdb", "alchemy")
	// The storage family, and openclaw, which they all mention.
	for i := range 4 {
		mem(fmt.Sprintf("sds-%d", i), "sds", "openclaw")
	}
	for i := range 3 {
		mem(fmt.Sprintf("drbd-%d", i), "drbd", "openclaw", "node-b")
	}
	// A project with two books: too small to be a wing, so it goes to the annex.
	mem("qi-0", "qilang")
	mem("qi-1", "qilang")
	// Memories that name no project are the general stacks.
	mem("loose-0", "openclaw")
	mem("loose-1")
	edges = append(edges,
		Edge{Source: "entity:alchemy", Target: "entity:cortexdb", Label: "depends_on"},
		Edge{Source: "entity:sds", Target: "entity:drbd", Label: "uses"},
		Edge{Source: "entity:openclaw", Target: "entity:node-b", Label: "runs_on"},
		Edge{Source: "entity:openclaw", Target: "entity:sds", Label: "co_occurs"},
		// a duplicate mention must not count twice
		Edge{Source: "memory:drbd-0", Target: "entity:node-b", Label: "mentions"},
	)
	return nodes, edges
}

func shelfOf(lib Library, memory string) string {
	for _, w := range lib.Wings {
		for _, s := range w.Shelves {
			for _, b := range s.Books {
				if b.ID == memory {
					return s.Project
				}
			}
		}
	}
	for _, s := range lib.Annex {
		for _, b := range s.Books {
			if b.ID == memory {
				return "annex:" + s.Project
			}
		}
	}
	for _, b := range lib.Stacks {
		if b.ID == memory {
			return "stacks"
		}
	}
	return ""
}

func TestLibraryShelvesAMemoryUnderTheMostSpecificProjectItNames(t *testing.T) {
	lib := BuildLibrary(libraryFixture())
	for memory, want := range map[string]string{
		"memory:cdb-0":  "CortexDB",
		"memory:both":   "alchemy",
		"memory:sds-2":  "SDS",
		"memory:drbd-1": "DRBD",
		"memory:qi-0":   "annex:qilang",
		// a memory that mentions only a service or nothing at all names no
		// project, and the general stacks hold it rather than a guess
		"memory:loose-0": "stacks",
		"memory:loose-1": "stacks",
	} {
		if got := shelfOf(lib, memory); got != want {
			t.Errorf("%s is shelved under %q, want %q", memory, got, want)
		}
	}
	if lib.Cards != 7 {
		t.Errorf("cards = %d, want every node that is not a memory (7)", lib.Cards)
	}
}

func TestLibraryGroupsShelvesIntoWingsByTheirProjectsRelations(t *testing.T) {
	lib := BuildLibrary(libraryFixture())
	wings := map[string][]string{}
	for _, w := range lib.Wings {
		for _, s := range w.Shelves {
			wings[w.Name] = append(wings[w.Name], s.Project)
		}
	}
	if len(lib.Wings) != 2 {
		t.Fatalf("wings = %v, want two: CortexDB's family and the storage family", wings)
	}
	if got := strings.Join(wings["CortexDB"], ","); got != "CortexDB,alchemy" {
		t.Errorf("CortexDB wing holds %q, want CortexDB and alchemy, which depends on it", got)
	}
	if got := strings.Join(wings["SDS"], ","); got != "SDS,DRBD" {
		t.Errorf("SDS wing holds %q, want SDS and DRBD, which it uses", got)
	}
	if lib.Wings[0].Name != "CortexDB" || lib.Wings[0].Books != 12 {
		t.Errorf("first wing = %s with %d books, want the largest (CortexDB, 12)", lib.Wings[0].Name, lib.Wings[0].Books)
	}
	if len(lib.Annex) != 1 || lib.Annex[0].Project != "qilang" {
		t.Errorf("annex = %+v, want qilang's shelf: two books are not a wing", lib.Annex)
	}
}

func TestLibraryBookThicknessIsItsIndexNotItsDuplicates(t *testing.T) {
	lib := BuildLibrary(libraryFixture())
	for _, w := range lib.Wings {
		for _, s := range w.Shelves {
			for _, b := range s.Books {
				if b.ID == "memory:drbd-0" && b.Terms != 3 {
					t.Errorf("drbd-0 has %d terms, want 3: a mention repeated is still one entry", b.Terms)
				}
			}
		}
	}
}

func TestLibraryOnAnEmptyBrainIsAnEmptyRoomNotAnError(t *testing.T) {
	lib := BuildLibrary(nil, nil)
	if lib.Wings == nil || len(lib.Wings) != 0 || len(lib.Stacks) != 0 || lib.Cards != 0 {
		t.Errorf("empty library = %+v", lib)
	}
}

func TestOpeningACardShowsEveryBookThatMentionsItAndItsRelations(t *testing.T) {
	nodes, edges := libraryFixture()
	card := BuildCard(nodes, edges, "entity:openclaw")
	if !card.Found || card.Label != "openclaw" || card.Type != "service" {
		t.Fatalf("card = %+v", card)
	}
	if len(card.Books) != 8 {
		t.Errorf("card names %d books, want the 8 memories that mention openclaw: %v", len(card.Books), card.Books)
	}
	if len(card.Relations) != 2 {
		t.Fatalf("relations = %+v, want runs_on node-b and co_occurs SDS", card.Relations)
	}
	if r := card.Relations[0]; r.Rel != "runs_on" || r.Label != "node-b" || !r.Out {
		t.Errorf("first relation = %+v, want runs_on node-b, told before noticed", r)
	}
	if r := card.Relations[1]; r.Rel != "co_occurs" {
		t.Errorf("last relation = %+v, want the co-occurrence last", r)
	}

	from := BuildCard(nodes, edges, "entity:node-b")
	if len(from.Relations) != 1 || from.Relations[0].Out || from.Relations[0].Label != "openclaw" {
		t.Errorf("node-b's card = %+v, want the incoming runs_on from openclaw", from.Relations)
	}
	if missing := BuildCard(nodes, edges, "entity:nowhere"); missing.Found || missing.Books == nil {
		t.Errorf("a card the snapshot lacks = %+v, want not found with empty lists", missing)
	}
}

func TestACardWithTooManyRelationsSaysHowManyItLeftOut(t *testing.T) {
	nodes := []Node{{ID: "hub", Label: "hub", Type: "entity"}}
	var edges []Edge
	for i := range cardRelations + 5 {
		id := fmt.Sprintf("n%d", i)
		nodes = append(nodes, Node{ID: id, Label: id, Type: "entity"})
		edges = append(edges, Edge{Source: "hub", Target: id, Label: "links"})
	}
	card := BuildCard(nodes, edges, "hub")
	if len(card.Relations) != cardRelations || card.More != 5 {
		t.Errorf("relations = %d, more = %d, want %d and 5", len(card.Relations), card.More, cardRelations)
	}
}

func TestTheBackOfABookListsItsIndexAndTheBooksNearestIt(t *testing.T) {
	nodes, edges := libraryFixture()
	page := BuildPage(nodes, edges, "memory:drbd-0")
	if !page.Found {
		t.Fatal("drbd-0 not found")
	}
	var index []string
	for _, t := range page.Index {
		index = append(index, t.Label)
	}
	if got := strings.Join(index, ","); got != "DRBD,node-b,openclaw" {
		t.Errorf("index = %q, want the project first, then the rest alphabetically", got)
	}
	if len(page.SeeAlso) != seeAlsoBooks {
		t.Fatalf("see also = %+v, want %d books", page.SeeAlso, seeAlsoBooks)
	}
	if s := page.SeeAlso[0]; s.Shared != 3 || !strings.HasPrefix(s.ID, "memory:drbd-") {
		t.Errorf("nearest book = %+v, want another DRBD memory sharing all three terms", s)
	}
	for _, s := range page.SeeAlso {
		if s.ID == "memory:drbd-0" {
			t.Error("a book is listed as related to itself")
		}
	}
}

func TestTheLibraryIsServedFromTheSameSnapshotAsTheGraph(t *testing.T) {
	nodes, edges := libraryFixture()
	src := &Source{
		Describe: "test brain",
		Read:     func(context.Context) ([]Node, []Edge, error) { return nodes, edges, nil },
		Record: func(_ context.Context, id string) (RecordDetail, error) {
			return RecordDetail{Available: true, Found: true, ID: id, Content: "the text of " + id}, nil
		},
		Close: func() error { return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sv, err := Start(ctx, src, 0, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sv.Close() })

	page := httpGet(t, sv.URL()+"/library")
	for _, want := range []string{`getJSON("api/library")`, `"api/library/card?id="`, `"api/library/book?id="`, `new EventSource("api/stream")`} {
		if !strings.Contains(page, want) {
			t.Errorf("library page is missing %s", want)
		}
	}

	var lib Library
	decodeGet(t, sv.URL()+"/api/library", &lib)
	if len(lib.Wings) != 2 || lib.Source != "test brain" || !lib.Records {
		t.Errorf("library = %d wings, source %q, records %v", len(lib.Wings), lib.Source, lib.Records)
	}
	if lib.DocsKnown {
		t.Error("a source with no tools cannot have been asked for documents, and the library must say so")
	}

	var card LibraryCard
	decodeGet(t, sv.URL()+"/api/library/card?id=entity:openclaw", &card)
	if len(card.Books) != 8 {
		t.Errorf("served card has %d books, want 8", len(card.Books))
	}

	var book LibraryPage
	decodeGet(t, sv.URL()+"/api/library/book?id=memory:drbd-0", &book)
	if book.Record.Content != "the text of memory:drbd-0" || len(book.Index) != 3 {
		t.Errorf("served book = %+v", book)
	}
}

// The library is a page of the view like the others: reached from the graph
// and the ontology, and every request it makes relative to where it is mounted.
func TestTheLibraryIsLinkedAndMountable(t *testing.T) {
	if !strings.Contains(pageHTML, `href="library"`) {
		t.Error("the graph page does not link to the library")
	}
	if !strings.Contains(ontologyHTML, `href="library"`) {
		t.Error("the ontology page does not link to the library")
	}
	for _, absolute := range []string{`fetch("/`, `getJSON("/`, `EventSource("/`, `href="/`} {
		if strings.Contains(libraryHTML, absolute) {
			t.Errorf("the library requests %s…, an absolute path that leaves an embedder's mount point", absolute)
		}
	}
	// no violet in the leathers: the palette is walnut, oak, parchment, brass
	for _, hex := range []string{"#8b5cf6", "#a855f7", "#7c3aed", "#c084fc"} {
		if strings.Contains(libraryHTML, hex) {
			t.Errorf("the library uses %s", hex)
		}
	}
}
