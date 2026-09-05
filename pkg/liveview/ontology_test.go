package liveview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// aviation is the schema the ontology tests are about: two object types, one
// interface one of them implements, and a link whose sides differ — Airport
// reaches MANY flights, Flight reaches ONE airport and carries the foreign
// key. That asymmetry is the thing the page exists to make visible, so it is
// in the fixture rather than added by whichever test happens to need it.
func aviation() cortexdb.OntologySchema {
	return cortexdb.OntologySchema{
		SchemaID: "aviation",
		Name:     "Aviation",
		Active:   true,
		InterfaceTypes: []cortexdb.OntologyInterfaceType{
			{APIName: "Locatable", Properties: []cortexdb.OntologyProperty{
				{APIName: "latitude", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataDouble}},
			}},
		},
		ObjectTypes: []cortexdb.OntologyObjectType{
			{
				APIName: "Airport", PrimaryKey: "iataCode", Implements: []string{"Locatable"},
				Properties: []cortexdb.OntologyProperty{
					{APIName: "iataCode", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
					{APIName: "airportName", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}},
				},
			},
			{
				APIName: "Flight", PrimaryKey: "flightNumber",
				Properties: []cortexdb.OntologyProperty{
					{APIName: "flightNumber", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
					{APIName: "originIata", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}},
				},
			},
		},
		LinkTypes: []cortexdb.OntologyLinkType{{
			APIName: "flightDeparture",
			A: cortexdb.OntologyLinkSide{APIName: "departures", ObjectTypeAPIName: "Airport",
				Cardinality: cortexdb.OntologyCardinalityMany},
			B: cortexdb.OntologyLinkSide{APIName: "origin", ObjectTypeAPIName: "Flight",
				Cardinality: cortexdb.OntologyCardinalityOne, ForeignKeyProperty: "originIata"},
		}},
	}
}

// objectNamed finds one drawn object type, or fails.
func objectNamed(t *testing.T, rep OntologyReport, name string) OntologyObjectNode {
	t.Helper()
	for _, ot := range rep.ObjectTypes {
		if ot.APIName == name {
			return ot
		}
	}
	t.Fatalf("no object type %q in %v", name, rep.ObjectTypes)
	return OntologyObjectNode{}
}

// strayNames renders an undeclared list as "name=count", which is what the
// assertions are about: which types are outside the model, how many records
// are under each, and in what order they are offered to a reader.
func strayNames(strays []OntologyStrayType) []string {
	out := make([]string, 0, len(strays))
	for _, s := range strays {
		out = append(out, fmt.Sprintf("%s=%d", s.Name, s.Count))
	}
	return out
}

/* ---- the four states ---- */

// The state a real brain is most likely in, and the one this page is most
// likely to be opened on. "Nobody has modelled this store" has to survive as
// a finding rather than arriving as an empty diagram, and the store's own
// vocabulary is still worth drawing without a schema — it exists, it was just
// never written down.
func TestOntologyReportOfAStoreWithNoSchemaSaysSoAndStillReadsItsVocabulary(t *testing.T) {
	read := buildOntologyReading("whole store",
		map[string]int{"entity": 4000, "chunk": 700, "": 17},
		map[string]int{"mentions": 9000, "about": 142},
		cortexdb.OntologySchema{})
	rep := buildOntologyReport(nil, "", &read)

	if rep.State != OntologyAbsent {
		t.Errorf("state = %q, want %q", rep.State, OntologyAbsent)
	}
	if !rep.Available {
		t.Error("the store answered; it is Reason-less unavailability that means nobody asked")
	}
	if rep.Saved {
		t.Error("saved = true with no schema list")
	}
	// The opposite finding, and on this store the whole of it.
	if rep.Usage.UndeclaredNodeTypes != 3 || rep.Usage.UndeclaredEdgeTypes != 2 {
		t.Errorf("undeclared types = %d/%d, want every type in the store (3/2)",
			rep.Usage.UndeclaredNodeTypes, rep.Usage.UndeclaredEdgeTypes)
	}
	if rep.Usage.Nodes != 4717 || rep.Usage.Edges != 9142 {
		t.Errorf("usage totals = %d/%d, want the whole store", rep.Usage.Nodes, rep.Usage.Edges)
	}
	// Never claimed of a store with nothing to claim it about.
	if rep.DeclaredUnusedTypes != 0 || rep.DeclaredUnusedLinks != 0 {
		t.Errorf("unused = %d/%d with nothing declared", rep.DeclaredUnusedTypes, rep.DeclaredUnusedLinks)
	}
}

// A schema describing a store it has never touched reads very differently
// from one whose types are all in use — and identically to it on any page
// that only draws boxes.
func TestOntologyReportOfASavedSchemaNothingUsesSaysUnused(t *testing.T) {
	read := buildOntologyReading("whole store",
		map[string]int{"entity": 90}, map[string]int{"mentions": 40}, aviation())
	rep := buildOntologyReport([]cortexdb.OntologySchema{aviation()}, "", &read)

	if rep.State != OntologyUnused {
		t.Fatalf("state = %q, want %q", rep.State, OntologyUnused)
	}
	if rep.DeclaredUnusedTypes != 2 || rep.DeclaredUnusedLinks != 1 {
		t.Errorf("unused = %d types / %d links, want every declaration",
			rep.DeclaredUnusedTypes, rep.DeclaredUnusedLinks)
	}
}

// The mistake this whole file is arranged to prevent: a count nobody took
// looks exactly like a count that came back zero, and reporting the first as
// the second declares every type in the schema dead on a page that measured
// nothing.
func TestOntologyReportNeverCallsADeclarationUnusedWithoutCountingTheStore(t *testing.T) {
	rep := buildOntologyReport([]cortexdb.OntologySchema{aviation()}, "", nil)

	if rep.Usage.Available {
		t.Fatal("usage claims to have been read when it was not")
	}
	if rep.State == OntologyUnused {
		t.Error("state = unused without the store ever being counted")
	}
	if rep.DeclaredUnusedTypes != 0 || rep.DeclaredUnusedLinks != 0 {
		t.Errorf("unused = %d/%d, want nothing claimed about data nobody read",
			rep.DeclaredUnusedTypes, rep.DeclaredUnusedLinks)
	}
	if rep.Usage.Reason == "" {
		t.Error("no reason given, so the page has nothing to show but zeroes")
	}
	if objectNamed(t, rep, "Airport").Instances != 0 {
		t.Error("an uncounted type carries a count")
	}
}

func TestOntologyReportOfAUsedSchemaIsLive(t *testing.T) {
	read := buildOntologyReading("whole store",
		map[string]int{"Airport": 12}, map[string]int{"flightDeparture": 30}, aviation())
	rep := buildOntologyReport([]cortexdb.OntologySchema{aviation()}, "", &read)

	if rep.State != OntologyLive {
		t.Fatalf("state = %q, want %q", rep.State, OntologyLive)
	}
	// Flight is declared and empty. That it sits beside a type in use is what
	// makes it a finding rather than the shape of the whole store.
	if rep.DeclaredUnusedTypes != 1 || rep.DeclaredUnusedLinks != 0 {
		t.Errorf("unused = %d types / %d links, want just Flight",
			rep.DeclaredUnusedTypes, rep.DeclaredUnusedLinks)
	}
	if objectNamed(t, rep, "Airport").Instances != 12 {
		t.Errorf("Airport instances = %d, want 12", objectNamed(t, rep, "Airport").Instances)
	}
}

/* ---- holding the declarations against the data ---- */

// Node ids fold case, and so does the ontology's own matching. Getting this
// wrong does not fail — it reports "Airport" as declared-but-unused and
// "airport" as an undeclared stray, which is two confident findings from one
// type.
func TestOntologyReportFoldsCaseWhenMatchingDeclarationsToData(t *testing.T) {
	read := buildOntologyReading("whole store",
		map[string]int{"airport": 41}, map[string]int{"FLIGHTDEPARTURE": 7}, aviation())
	rep := buildOntologyReport([]cortexdb.OntologySchema{aviation()}, "", &read)

	if got := objectNamed(t, rep, "Airport").Instances; got != 41 {
		t.Errorf("Airport instances = %d, want the 41 stored as \"airport\"", got)
	}
	if len(rep.Usage.UndeclaredNodes) != 0 {
		t.Errorf("undeclared nodes = %v, want none — the type is declared, in another case",
			strayNames(rep.Usage.UndeclaredNodes))
	}
	if rep.LinkTypes[0].Instances != 7 {
		t.Errorf("link instances = %d, want the 7 stored under another spelling", rep.LinkTypes[0].Instances)
	}
}

// Largest first, because the biggest hole in the schema is the one a reader
// should land on, and stable because a list that reordered between refreshes
// would read as a change in the store.
func TestOntologyUsageOrdersUndeclaredTypesBySizeAndKeepsTheUntyped(t *testing.T) {
	read := buildOntologyReading("whole store",
		map[string]int{"Airport": 3, "memory": 20, "concept": 900, "": 5},
		map[string]int{}, aviation())

	want := []string{"concept=900", "memory=20", "=5"}
	if got := strayNames(read.Usage.UndeclaredNodes); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("undeclared = %v, want %v", got, want)
	}
	// Records carrying no type at all are a finding of their own, and not the
	// same one as a type the schema forgot.
	if read.Usage.UndeclaredNodes[2].Name != "" {
		t.Error("the untyped records were folded in with a named type")
	}
	if read.Usage.UndeclaredNodeCount != 925 {
		t.Errorf("undeclared records = %d, want 925 — the declared Airport rows are not among them",
			read.Usage.UndeclaredNodeCount)
	}
}

// The cap is on the answer, not on the work: a reader is told how many types
// the schema does not describe, rather than shown a list that quietly stops.
func TestOntologyUsageCapsTheUndeclaredListButNotItsTotals(t *testing.T) {
	counts := map[string]int{}
	for i := 0; i < ontologyStrayLimit+13; i++ {
		counts[fmt.Sprintf("type%03d", i)] = i + 1
	}
	read := buildOntologyReading("whole store", counts, map[string]int{}, cortexdb.OntologySchema{})

	if len(read.Usage.UndeclaredNodes) != ontologyStrayLimit {
		t.Errorf("list has %d entries, want it capped at %d", len(read.Usage.UndeclaredNodes), ontologyStrayLimit)
	}
	if !read.Usage.NodeListTruncated {
		t.Error("the list was cut and does not say so")
	}
	if read.Usage.UndeclaredNodeTypes != ontologyStrayLimit+13 {
		t.Errorf("undeclared type count = %d, want the true %d",
			read.Usage.UndeclaredNodeTypes, ontologyStrayLimit+13)
	}
}

/* ---- the three things only readable from raw JSON today ---- */

// Multiplicity is per side and the ONE side carries the foreign key. A reader
// told only "one-to-many" cannot tell which end holds the column, which is the
// half of the declaration that decides how the data is stored.
func TestOntologyReportKeepsCardinalityPerSideAndNamesTheForeignKeySide(t *testing.T) {
	rep := buildOntologyReport([]cortexdb.OntologySchema{aviation()}, "", nil)
	if len(rep.LinkTypes) != 1 {
		t.Fatalf("link types = %v", rep.LinkTypes)
	}
	lt := rep.LinkTypes[0]
	if lt.A.Cardinality != "MANY" || lt.B.Cardinality != "ONE" {
		t.Errorf("cardinality = %s/%s, want MANY/ONE kept per side", lt.A.Cardinality, lt.B.Cardinality)
	}
	if lt.Multiplicity != "many-to-one" {
		t.Errorf("multiplicity = %q, want many-to-one composed from the two sides", lt.Multiplicity)
	}
	if lt.B.ForeignKey != "originIata" || lt.A.ForeignKey != "" {
		t.Errorf("foreign key = %q on A / %q on B, want it only on the ONE side",
			lt.A.ForeignKey, lt.B.ForeignKey)
	}
	if lt.A.ObjectType != "Airport" || lt.B.ObjectType != "Flight" {
		t.Errorf("sides = %s/%s, want the ends they were declared against", lt.A.ObjectType, lt.B.ObjectType)
	}
}

// A cardinality the schema never stated is a schema that has not decided.
// "many" is the wrong default to invent for it, and a phrase composed from a
// blank would read as a decision somebody made.
func TestOntologyReportLeavesMultiplicityBlankWhenASideDoesNotDeclareIt(t *testing.T) {
	schema := aviation()
	schema.LinkTypes[0].A.Cardinality = ""
	rep := buildOntologyReport([]cortexdb.OntologySchema{schema}, "", nil)
	if rep.LinkTypes[0].Multiplicity != "" {
		t.Errorf("multiplicity = %q, want nothing invented for an undeclared side",
			rep.LinkTypes[0].Multiplicity)
	}
}

// "Which object types share a shape" is the question, and an object type
// implementing a child interface shares the parent's shape too. Reading
// Implements literally answers a narrower question than the page is asking.
func TestOntologyReportResolvesImplementorsThroughInterfaceInheritance(t *testing.T) {
	schema := aviation()
	schema.InterfaceTypes = append(schema.InterfaceTypes,
		cortexdb.OntologyInterfaceType{APIName: "Geocoded", Extends: []string{"Locatable"}})
	schema.ObjectTypes[1].Implements = []string{"Geocoded"}

	rep := buildOntologyReport([]cortexdb.OntologySchema{schema}, "", nil)
	var locatable OntologyInterfaceNode
	for _, it := range rep.Interfaces {
		if it.APIName == "Locatable" {
			locatable = it
		}
	}
	if strings.Join(locatable.Implementors, ",") != "Airport,Flight" {
		t.Errorf("Locatable implementors = %v, want Flight too — it implements an interface that extends it",
			locatable.Implementors)
	}
}

// Validation rejects cycles, but this reads schemas that are already stored,
// possibly written by an older binary. A view is the wrong place to find that
// out by hanging.
func TestOntologyReportSurvivesACycleInInterfaceInheritance(t *testing.T) {
	schema := aviation()
	schema.InterfaceTypes = []cortexdb.OntologyInterfaceType{
		{APIName: "A", Extends: []string{"B"}},
		{APIName: "B", Extends: []string{"A"}},
	}
	schema.ObjectTypes[0].Implements = []string{"A"}

	done := make(chan OntologyReport, 1)
	go func() { done <- buildOntologyReport([]cortexdb.OntologySchema{schema}, "", nil) }()
	select {
	case rep := <-done:
		if len(rep.Interfaces) != 2 {
			t.Errorf("interfaces = %v, want both drawn", rep.Interfaces)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cyclic Extends chain hung the report")
	}
}

/* ---- choosing among saved schemas ---- */

// The active one is the schema deciding what writes are allowed, so it is the
// one a reader means. Never "whichever came back first": a page that redrew a
// different schema on a refresh would look like the schema had changed.
func TestOntologyReportPrefersTheActiveSchemaAndThenAnExplicitOne(t *testing.T) {
	old := aviation()
	old.SchemaID, old.Active = "aviation-draft", false
	live := aviation()

	rep := buildOntologyReport([]cortexdb.OntologySchema{old, live}, "", nil)
	if rep.SchemaID != "aviation" {
		t.Errorf("drew %q, want the active schema", rep.SchemaID)
	}
	if len(rep.Schemas) != 2 {
		t.Errorf("schemas = %v, want both offered so a reader can switch", rep.Schemas)
	}
	if asked := buildOntologyReport([]cortexdb.OntologySchema{old, live}, "aviation-draft", nil); asked.SchemaID != "aviation-draft" {
		t.Errorf("asked for aviation-draft, drew %q", asked.SchemaID)
	}
}

// The zero value of Enforcement is the strict default. A page saying nothing
// about enforcement reads as "not enforced", which is its opposite.
func TestOntologyReportNamesTheDefaultEnforcementRatherThanLeavingItBlank(t *testing.T) {
	schema := aviation()
	schema.Enforcement = ""
	rep := buildOntologyReport([]cortexdb.OntologySchema{schema}, "", nil)
	if rep.Enforcement != string(cortexdb.OntologyEnforcementStrict) {
		t.Errorf("enforcement = %q, want the strict default said out loud", rep.Enforcement)
	}
}

/* ---- the endpoint ---- */

func ontologySource(f *fakeSource, hook func(context.Context, OntologyQuery) (OntologyReport, error)) *Source {
	src := f.source()
	src.Ontology = hook
	return src
}

func startOntologyServer(t *testing.T, hook func(context.Context, OntologyQuery) (OntologyReport, error)) *Server {
	t.Helper()
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A"}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sv, err := Start(ctx, ontologySource(f, hook), 0, 40*time.Millisecond, false)
	if err != nil {
		t.Fatalf("start live server: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	return sv
}

func TestLiveServerServesTheOntology(t *testing.T) {
	read := buildOntologyReading("whole store",
		map[string]int{"Airport": 4, "memory": 12}, map[string]int{}, aviation())
	want := buildOntologyReport([]cortexdb.OntologySchema{aviation()}, "", &read)
	sv := startOntologyServer(t, func(context.Context, OntologyQuery) (OntologyReport, error) { return want, nil })

	var got OntologyReport
	decodeGet(t, sv.URL()+"/api/ontology", &got)
	if !got.Available || got.State != OntologyLive {
		t.Fatalf("report = %+v, want an available live one", got)
	}
	if len(got.ObjectTypes) != 2 || len(got.LinkTypes) != 1 || len(got.Interfaces) != 1 {
		t.Errorf("report drew %d types / %d links / %d interfaces",
			len(got.ObjectTypes), len(got.LinkTypes), len(got.Interfaces))
	}
	if got.Usage.UndeclaredNodes[0].Name != "memory" {
		t.Errorf("undeclared = %v, want memory to survive the wire", strayNames(got.Usage.UndeclaredNodes))
	}
}

// A source with no ontology hook is not a store with no ontology. It is the
// difference between "nobody asked" and "we asked, and there is none", and a
// page handed an empty list of object types would render them the same.
func TestLiveServerSaysWhenItCannotBeAskedForAnOntology(t *testing.T) {
	sv := startOntologyServer(t, nil)

	var got OntologyReport
	decodeGet(t, sv.URL()+"/api/ontology", &got)
	if got.Available {
		t.Fatalf("report = %+v, want it to admit it cannot answer", got)
	}
	if got.State != OntologyUnreadable {
		t.Errorf("state = %q, want %q — not %q, which is a claim about the store",
			got.State, OntologyUnreadable, OntologyAbsent)
	}
	if got.Reason == "" {
		t.Error("no reason given, so the page has nothing to show but an empty diagram")
	}
	// Rendered by a page that indexes them.
	if got.ObjectTypes == nil || got.LinkTypes == nil || got.Interfaces == nil || got.Schemas == nil {
		t.Error("the lists must be empty, not null")
	}
	if got.Usage.UndeclaredNodes == nil || got.Usage.UndeclaredEdges == nil {
		t.Error("the usage lists must be empty, not null")
	}
}

// A read that fails must still answer, or the page keeps showing the schema it
// drew last as though it were this store's.
func TestLiveServerReportsAFailedOntologyReadWithoutFailingTheRequest(t *testing.T) {
	sv := startOntologyServer(t, func(context.Context, OntologyQuery) (OntologyReport, error) {
		return OntologyReport{}, errors.New("ontology list: database is locked")
	})

	var got OntologyReport
	decodeGet(t, sv.URL()+"/api/ontology", &got)
	if got.Available || got.State != OntologyUnreadable {
		t.Fatalf("report = %+v, want unreadable", got)
	}
	if !strings.Contains(got.Reason, "database is locked") {
		t.Errorf("reason = %q, want it to carry the read's own error", got.Reason)
	}
}

// gap=0 has to reach the source, not just the paint: the store's own
// vocabulary is two aggregate scans, and an embedder showing the model alone
// should cost the store nothing for the half it is not showing.
func TestTheOntologyEndpointPassesItsOptionsThroughToTheSource(t *testing.T) {
	seen := make(chan OntologyQuery, 4)
	sv := startOntologyServer(t, func(_ context.Context, q OntologyQuery) (OntologyReport, error) {
		seen <- q
		return buildOntologyReport(nil, "", nil), nil
	})

	var sink OntologyReport
	decodeGet(t, sv.URL()+"/api/ontology", &sink)
	if q := <-seen; !q.Usage {
		t.Error("the default did not ask for the store's own vocabulary, so the gap is never measured")
	}
	decodeGet(t, sv.URL()+"/api/ontology?gap=0&schema=aviation-draft", &sink)
	q := <-seen
	if q.Usage {
		t.Error("gap=0 still read the store")
	}
	if q.SchemaID != "aviation-draft" {
		t.Errorf("schema = %q, want the one asked for", q.SchemaID)
	}
}

// A second page, not a second app: it lives on the same loopback listener, and
// the root still belongs to the scene.
func TestTheOntologyPageHasItsOwnPathAndDoesNotTakeTheRoot(t *testing.T) {
	sv := startOntologyServer(t, func(context.Context, OntologyQuery) (OntologyReport, error) {
		return buildOntologyReport(nil, "", nil), nil
	})
	if !strings.HasPrefix(sv.URL(), "http://127.0.0.1:") {
		t.Fatalf("listening on %s, want 127.0.0.1 — the second page must not widen the bind", sv.URL())
	}

	page := httpGet(t, sv.URL()+"/ontology")
	if !strings.Contains(page, "CortexDB — ontology") {
		t.Error("/ontology does not serve the ontology page")
	}
	if strings.Contains(page, "ForceGraph3D") {
		t.Error("the ontology page pulls in the scene's renderer")
	}
	if !strings.Contains(httpGet(t, sv.URL()+"/"), "ForceGraph3D") {
		t.Error("the scene page no longer owns the root")
	}
	resp, err := http.Get(sv.URL() + "/ontologyz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown path answered %d, want 404", resp.StatusCode)
	}
}

/* ---- the page ---- */

// Both directions, and both relative. The pages are mounted behind other
// applications' proxies, and an absolute /ontology would leave the mount point.
func TestTheTwoPagesReachEachOther(t *testing.T) {
	if !strings.Contains(pageHTML, `<a href="ontology"`) {
		t.Error("the scene page has no way to reach the ontology")
	}
	if strings.Contains(pageHTML, `href="/ontology"`) {
		t.Error("the link is absolute, so it leaves the mount point")
	}
	if !strings.Contains(ontologyHTML, `<a href="."`) {
		t.Error("the ontology page has no way back to the scene")
	}
}

// Same reason the stream and the contract are relative.
func TestTheOntologyPageAsksForItsDataRelatively(t *testing.T) {
	if strings.Contains(ontologyHTML, `fetch("/api/`) {
		t.Error("the ontology is fetched at an absolute path, so the page breaks under a prefix")
	}
	if !strings.Contains(ontologyHTML, `fetch("api/ontology"`) {
		t.Error("the page never asks for the ontology")
	}
}

// One store, one word for how often it is read. A page that drifted from the
// constant would be polling at a cadence nothing in Go describes.
func TestTheOntologyPagePollsAtTheIntervalGoDeclares(t *testing.T) {
	want := fmt.Sprintf("var ONTOLOGY_MS = %d;", OntologyInterval.Milliseconds())
	if !strings.Contains(ontologyHTML, want) {
		t.Errorf("the page does not poll at OntologyInterval (%s); expected %q", OntologyInterval, want)
	}
}

// Every switch is also a URL option, because an embedder cannot press a
// button — the rule the scene page's options block states and this page
// inherits.
func TestTheOntologyPageSwitchesAreAlsoUrlOptions(t *testing.T) {
	for opt, expr := range map[string]string{
		"?gap=0":     `OPTS.gap !== "0"`,
		"?strays=0":  `OPTS.strays !== "0"`,
		"?panels=0":  `OPTS.panels === "0"`,
		"?schema=ID": `OPTS.schema`,
	} {
		if !strings.Contains(ontologyHTML, expr) {
			t.Errorf("%s is not read from the URL (expected %s)", opt, expr)
		}
		if !strings.Contains(ontologyHTML, opt) {
			t.Errorf("%s is not documented beside the others it belongs with", opt)
		}
	}
}

// Four states, four sentences. A page that branched on a list length instead
// would render "nobody asked", "nothing is saved" and "you turned the count
// off" as the same empty diagram.
func TestTheOntologyPageHasADistinctSentenceForEachState(t *testing.T) {
	for state, sentence := range map[string]string{
		OntologyUnreadable: "This view cannot be asked for an ontology.",
		OntologyAbsent:     "No ontology is saved in this store.",
		OntologyUnused:     "An ontology is saved, and nothing in the store uses it.",
		OntologyLive:       "The gap was not measured",
	} {
		if !strings.Contains(ontologyHTML, sentence) {
			t.Errorf("state %q has no sentence of its own; expected %q", state, sentence)
		}
		if !strings.Contains(ontologyHTML, `"`+state+`"`) {
			t.Errorf("the page never tests for state %q, so Go's decision is not the one drawn", state)
		}
	}
}

// The three things only readable from raw JSON today, and the reason the page
// exists. Each has to be drawn, not merely carried in the payload.
func TestTheOntologyPageDrawsTheGapTheCardinalityAndTheInterfaces(t *testing.T) {
	for what, mark := range map[string]string{
		// A declared type with nothing under it, dashed and labelled — and
		// never confused with one whose count was not taken.
		"a declaration nothing uses": `"unused"`,
		"a count nobody took":        `"not counted"`,
		// Per side, with the foreign key marked on the side that declares it.
		"the ONE side":       `side.cardinality === "ONE" ? "1"`,
		"the foreign key":    "side.foreign_key ?",
		"the undeclared":     "undeclared_nodes",
		"the interface lane": "data-iface",
	} {
		if !strings.Contains(ontologyHTML, mark) {
			t.Errorf("the page does not draw %s (expected %s)", what, mark)
		}
	}
}

// ?panels=0 means "the diagram alone" here, the way it means "the graph alone"
// next door. It hides chrome an embedder can do without — and the diagram, the
// finding above it and the band below it are not chrome, they are the answer.
func TestTheOntologyPageKeepsItsAnswerWhenTheChromeIsTurnedOff(t *testing.T) {
	if !strings.Contains(ontologyHTML, `<div class="card" id="canvas">`) ||
		!strings.Contains(ontologyHTML, `<div class="card" id="say">`) {
		t.Error("the diagram and the finding are panels, so ?panels=0 blanks the page")
	}
	if !strings.Contains(ontologyHTML, ".bare .panel") {
		t.Error("?panels=0 hides nothing")
	}
}

// The layout is a deliberate departure from the scene next door and the reason
// is written above the constant. If the page ever pulls in the force graph,
// that reasoning has been quietly reversed.
func TestTheOntologyPageIsNotTheSceneAgain(t *testing.T) {
	for _, wrong := range []string{"ForceGraph3D", "UnrealBloomPass", "3d-force-graph"} {
		if strings.Contains(ontologyHTML, wrong) {
			t.Errorf("the ontology page uses %s; tens of named types are not a force layout's problem", wrong)
		}
	}
	// And it reaches no CDN at all: the diagram is a few hundred lines of SVG
	// and a third-party script would be a way for it to fail.
	if strings.Contains(ontologyHTML, "https://") {
		t.Error("the ontology page loads something off the network")
	}
}

/* ---- the local reader, against a real store ---- */

// The page's numbers come off a real store or they are a shape nobody has seen
// it produce. This saves a schema, writes one node of a declared type, one of
// a type nothing declares, and edges of each — then reads it back the way the
// endpoint does.
func TestLocalOntologyReadsARealStore(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "ontology.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := db.SaveOntologySchema(ctx,
		cortexdb.OntologySaveRequest{Schema: aviation(), Activate: true}); err != nil {
		t.Fatalf("save ontology: %v", err)
	}

	for _, n := range []*graph.GraphNode{
		{ID: "entity:airport:lhr", NodeType: "Airport", Content: "Heathrow", Vector: []float32{1, 0, 0, 0}},
		{ID: "entity:airport:cdg", NodeType: "Airport", Content: "Charles de Gaulle", Vector: []float32{0, 1, 0, 0}},
		// Written by something that never heard of the schema. On a shared
		// brain this is most of what is there.
		{ID: "entity:weather", NodeType: "concept", Content: "fog", Vector: []float32{0, 0, 1, 0}},
	} {
		if err := db.Graph().UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}
	if err := db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
		ID: "e:stray", FromNodeID: "entity:airport:lhr", ToNodeID: "entity:weather",
		EdgeType: "mentions", Weight: 1,
	}); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	rep, err := localOntology(db)(ctx, OntologyQuery{Usage: true})
	if err != nil {
		t.Fatalf("localOntology: %v", err)
	}
	if rep.State != OntologyLive || rep.SchemaID != "aviation" {
		t.Fatalf("state = %q, schema = %q; want the saved aviation schema in use", rep.State, rep.SchemaID)
	}
	if got := objectNamed(t, rep, "Airport").Instances; got != 2 {
		t.Errorf("Airport instances = %d, want the 2 written", got)
	}
	// Declared and empty, beside one that is not: the finding this page draws
	// dashed.
	if got := objectNamed(t, rep, "Flight").Instances; got != 0 {
		t.Errorf("Flight instances = %d, want 0 — nothing wrote one", got)
	}
	if rep.DeclaredUnusedTypes != 1 || rep.DeclaredUnusedLinks != 1 {
		t.Errorf("unused = %d types / %d links, want Flight and flightDeparture",
			rep.DeclaredUnusedTypes, rep.DeclaredUnusedLinks)
	}
	if got := strayNames(rep.Usage.UndeclaredNodes); strings.Join(got, ",") != "concept=1" {
		t.Errorf("undeclared node types = %v, want just the concept nobody declared", got)
	}
	if got := strayNames(rep.Usage.UndeclaredEdges); strings.Join(got, ",") != "mentions=1" {
		t.Errorf("undeclared edge types = %v, want just the mentions edge", got)
	}
	if rep.Usage.Scope == "" {
		t.Error("the reading does not say what it counted, so two totals that disagree look like a fault")
	}

	// And with the gap switched off: the declarations, and nothing read about
	// the data — every count meaningless and saying so.
	bare, err := localOntology(db)(ctx, OntologyQuery{})
	if err != nil {
		t.Fatalf("localOntology without usage: %v", err)
	}
	if bare.Usage.Available {
		t.Error("gap=0 read the store anyway")
	}
	if bare.DeclaredUnusedTypes != 0 {
		t.Error("gap=0 still declared types unused, on a count it never took")
	}
	if len(bare.ObjectTypes) != 2 {
		t.Errorf("gap=0 drew %d object types, want the model itself", len(bare.ObjectTypes))
	}
}
