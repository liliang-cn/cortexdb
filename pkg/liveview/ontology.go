package liveview

// Reading the ontology for the live view's second page.
//
// The first page draws what is *in* this brain: four thousand nodes with no
// inherent order, force-directed and truncated by degree. This draws what the
// brain is *allowed to talk about* — tens of object types with link types
// between them, declared in pkg/cortexdb/ontology_types.go and validated by
// ontology_validation.go. They are two different graphs about one store, which
// is why they are two pages rather than one with a switch: nothing on either
// is a filter of the other.
//
// A picture of the declarations alone describes intent. On its own that is a
// diagram somebody could have drawn on a whiteboard, and it would be true
// whether or not a single record ever conformed to it. So the report carries a
// second reading beside it — what the store's nodes and edges are actually
// typed as — and the gap between the two is the finding this page exists for:
//
//   - A declared object type with no instances is something nobody has used.
//   - A node_type in the data that the ontology never declared is the reverse,
//     and on a real brain it is most of them: 4,717 nodes typed by whatever
//     wrote them, under a schema that may not exist at all.
//
// Those two are told apart in Go and never inferred from a zero on the page,
// because a zero from a count that was taken and a zero from a count that
// could not be is the same zero, and only one of them is a finding.
//
// Deliberately NOT the vocabulary of ontology_diff.go. That file compares two
// schema versions and speaks of types added and removed — words that describe
// a change over time. This compares a schema against data, where nothing was
// added or removed and nothing changed; the two sides simply never agreed. So
// the words here are "declared", "unused" and "undeclared", and the one thing
// borrowed from over there is how names are matched: case-folded, exactly as
// ontologyAPIKey does it, so "Airport" in the schema and "airport" in the
// store are one type rather than a spurious pair of findings.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// OntologyInterval is how often the ontology page re-reads.
//
// Slower again than [ContractInterval], and for the same kind of reason one
// step further along. A contract tally moves when somebody reviews a record; a
// schema moves when somebody redesigns the model, which is a change measured
// in releases. Half a minute is already far faster than the thing being
// watched, and the read is not free: the store's vocabulary is two aggregate
// scans, the same cost class as the contract's.
const OntologyInterval = 30 * time.Second

// ontologyStrayLimit caps the undeclared-type lists the page is sent.
//
// The cap is on the answer, not on the work, exactly as
// contractAttentionLimit is: the totals are reported whole, so a reader is
// told how many types the ontology does not describe rather than shown a list
// that quietly stops. Forty because these are chips a few words wide and a
// reader can take in a screen of them; nobody reads four hundred.
const ontologyStrayLimit = 40

// ontologyUsageScan bounds the shared brain's half of the usage read.
//
// The library reads node_type and edge_type as GROUP BYs and needs no bound.
// A shared brain has no tool for that, so the counting is done over
// graph_list_all — the same door LoadRemote already uses, asked for once every
// OntologyInterval against a graph the structure poll is already pulling every
// two seconds. It is strictly less traffic than the page next door, and the
// limit is high enough that a brain of a few thousand nodes arrives whole; a
// brain that outgrows it says so through Usage.Scope rather than quietly
// counting part of itself.
const ontologyUsageScan = 20000

// The four things this page can have to say before it draws anything, decided
// here rather than guessed from an empty list on the page. They are ordered
// from "we know nothing" to "we know everything", and the first three are the
// ones a real machine is actually in.
const (
	// OntologyUnreadable is a source that keeps no ontology hook, or a read
	// that failed. It is not an absence of ontology — nobody asked.
	OntologyUnreadable = "unreadable"
	// OntologyAbsent is the answer, not the lack of one: this store was asked
	// and holds no schema. The likely state of a real brain today, and the
	// reason the undeclared side of the report is still worth drawing without
	// one — a store with no ontology still has a vocabulary, it just never
	// wrote it down.
	OntologyAbsent = "absent"
	// OntologyUnused is a schema that exists and describes nothing here: every
	// declared type at zero instances. Claimable only when the store's own
	// vocabulary could be read, because otherwise every count is zero for the
	// uninteresting reason.
	OntologyUnused = "unused"
	// OntologyLive is a schema that exists and has not been shown to be
	// unused. Whether anything conforms to it is Usage.Available's question,
	// not this one: with the second reading taken, live means at least one
	// declaration is in use; without it, live means only that there is a
	// schema. Collapsing the two here would let ?gap=0 — which reads nothing
	// about the data — report a store as conforming.
	OntologyLive = "live"
)

// OntologyProp is one declared property, flattened for display.
//
// Kind is the data type's discriminator only. An array's element type and a
// struct's fields are a tree, and a page drawing tens of types has no room to
// unfold one per property — the discriminator is what a reader is scanning
// for ("is this a string or a timestamp"), and the nesting is detail the
// schema JSON still holds for whoever needs it.
type OntologyProp struct {
	APIName     string `json:"api_name"`
	DisplayName string `json:"display_name,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Searchable  bool   `json:"searchable,omitempty"`
	Vectorized  bool   `json:"vectorized,omitempty"`
}

// OntologyObjectNode is one declared object type as the page draws it.
type OntologyObjectNode struct {
	APIName       string         `json:"api_name"`
	DisplayName   string         `json:"display_name,omitempty"`
	Description   string         `json:"description,omitempty"`
	PrimaryKey    string         `json:"primary_key,omitempty"`
	TitleProperty string         `json:"title_property,omitempty"`
	Status        string         `json:"status,omitempty"`
	Visibility    string         `json:"visibility,omitempty"`
	Implements    []string       `json:"implements"`
	Aliases       []string       `json:"aliases,omitempty"`
	Properties    []OntologyProp `json:"properties"`
	// Instances is how many nodes in the store carry this type, and is
	// meaningful only when Usage.Available. A type with none is a declaration
	// nobody has used; the page must not say that of a count it never took.
	Instances int `json:"instances"`
}

// OntologyLinkEnd is one side of a link type.
//
// Kept as two sides rather than folded into a single direction because that
// is how the schema models it and because folding loses the thing worth
// seeing: multiplicity is per side, and the ONE side is the side that carries
// the foreign key. A reader who only sees "one-to-many" cannot tell which end
// holds the column.
type OntologyLinkEnd struct {
	APIName     string `json:"api_name"`
	DisplayName string `json:"display_name,omitempty"`
	ObjectType  string `json:"object_type"`
	Cardinality string `json:"cardinality"`
	ForeignKey  string `json:"foreign_key,omitempty"`
}

// OntologyLinkEdge is one declared link type as the page draws it.
type OntologyLinkEdge struct {
	APIName     string          `json:"api_name"`
	Description string          `json:"description,omitempty"`
	Status      string          `json:"status,omitempty"`
	A           OntologyLinkEnd `json:"a"`
	B           OntologyLinkEnd `json:"b"`
	// Multiplicity is the pair read as one phrase — "one-to-many" and the
	// rest. Composed here because it is a fact about the two sides together
	// and a page recomposing it from two cardinality strings would be the
	// second place in this repository that knows the rule.
	Multiplicity string `json:"multiplicity"`
	// Instances is how many edges carry this link type. Same caveat as
	// OntologyObjectNode.Instances: only meaningful when Usage.Available.
	Instances int `json:"instances"`
}

// OntologyInterfaceNode is one declared interface, with the object types that
// implement it.
type OntologyInterfaceNode struct {
	APIName     string         `json:"api_name"`
	DisplayName string         `json:"display_name,omitempty"`
	Description string         `json:"description,omitempty"`
	Extends     []string       `json:"extends,omitempty"`
	Properties  []OntologyProp `json:"properties"`
	// Implementors is resolved through Extends transitively, so an object type
	// implementing a child interface is listed under the parent it inherits
	// from too. Reading Implements literally would answer a narrower question
	// than the one the page is asking, which is "which object types share this
	// shape".
	Implementors []string `json:"implementors"`
}

// OntologyStrayType is one type present in the store that the ontology never
// declared.
type OntologyStrayType struct {
	// Name is the node_type or edge_type verbatim, empty for the records that
	// carry none — which is a finding of its own and not the same as a type
	// the schema is missing.
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// OntologyUsage is the second reading: what the store's records are actually
// typed as, against which the declarations are held.
type OntologyUsage struct {
	// Available separates a count that came back zero from a count nobody
	// took. Without it a page has no way to tell "nothing uses this type"
	// from "this view cannot count", and would report the second as the first.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Scope says in words what was counted, because the two source shapes
	// count different sets: the library counts every row including chunks, a
	// shared brain answers over the entity graph with chunks excluded. Two
	// totals that disagree are not a fault and a reader should not have to
	// guess that.
	Scope string `json:"scope,omitempty"`

	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
	// NodeTypes and EdgeTypes are how many distinct types the store uses,
	// declared or not — the denominator the undeclared lists are a part of.
	NodeTypes int `json:"node_types"`
	EdgeTypes int `json:"edge_types"`

	// UndeclaredNodes and UndeclaredEdges are the types in the data that no
	// object or link type describes, largest first: on a store with no schema
	// this is the whole vocabulary, and it is the page's only content.
	UndeclaredNodes []OntologyStrayType `json:"undeclared_nodes"`
	UndeclaredEdges []OntologyStrayType `json:"undeclared_edges"`
	// The true counts behind the capped lists above.
	UndeclaredNodeTypes int  `json:"undeclared_node_types"`
	UndeclaredEdgeTypes int  `json:"undeclared_edge_types"`
	UndeclaredNodeCount int  `json:"undeclared_node_count"`
	UndeclaredEdgeCount int  `json:"undeclared_edge_count"`
	NodeListTruncated   bool `json:"node_list_truncated,omitempty"`
	EdgeListTruncated   bool `json:"edge_list_truncated,omitempty"`
}

// ontologyReading is one look at the store's vocabulary: the usage the page
// draws, plus the per-declared-type tallies that never reach it.
//
// The two travel together rather than as two arguments because pairing one
// store's counts with another store's usage figures is exactly the mistake
// that would produce a confident, wrong gap. The counts stay off the wire:
// the page needs a number per box, which it gets on the box, and shipping the
// map as well would be one more thing that can disagree with it.
type ontologyReading struct {
	Usage OntologyUsage
	nodes map[string]int
	edges map[string]int
}

// OntologySchemaRef names one saved schema, so a page drawing one of several
// can say which and offer the others.
type OntologySchemaRef struct {
	SchemaID string `json:"schema_id"`
	Name     string `json:"name,omitempty"`
	Version  int    `json:"version"`
	Active   bool   `json:"active"`
}

// OntologyReport is what the ontology page draws.
//
// Available and State carry the honesty, and they are not the same question.
// Available answers "could this view be asked at all"; State answers "and what
// did it find". A source with no ontology hook, a store with no schema, and a
// schema nothing conforms to are three different findings, and a page handed
// only an empty list of object types would render all three as the same
// empty diagram.
type OntologyReport struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// State is one of the four constants above, decided here.
	State string `json:"state"`

	// Saved reports whether the store holds any schema at all — the question
	// behind OntologyAbsent, kept as its own field so the page never has to
	// infer it from an empty ObjectTypes.
	Saved   bool                `json:"saved"`
	Schemas []OntologySchemaRef `json:"schemas"`

	SchemaID      string `json:"schema_id,omitempty"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	Version       int    `json:"version,omitempty"`
	Active        bool   `json:"active,omitempty"`
	Enforcement   string `json:"enforcement,omitempty"`
	StrictActions bool   `json:"strict_actions,omitempty"`
	// Actions and ObjectSets are counted, not listed: they describe how the
	// data is written and read rather than what shape it has — the same line
	// DiffOntologySchemas draws when it declines to compare them — so they
	// belong on this page as evidence the schema has them, not as a second
	// diagram.
	ActionTypes      int `json:"action_types"`
	ObjectSets       int `json:"object_sets"`
	SharedProperties int `json:"shared_properties"`

	ObjectTypes []OntologyObjectNode    `json:"object_types"`
	LinkTypes   []OntologyLinkEdge      `json:"link_types"`
	Interfaces  []OntologyInterfaceNode `json:"interfaces"`

	// DeclaredUnusedTypes and DeclaredUnusedLinks are how many declarations
	// nothing in the store uses. Counted here rather than on the page so the
	// sentence and the diagram cannot disagree about what "unused" means.
	DeclaredUnusedTypes int `json:"declared_unused_types"`
	DeclaredUnusedLinks int `json:"declared_unused_links"`

	Usage OntologyUsage `json:"usage"`

	// At is when this was read, so a slow number does not look like a stuck
	// one.
	At int64 `json:"at"`
}

// OntologyQuery is what the page asks for.
type OntologyQuery struct {
	// SchemaID picks one of several saved schemas. Empty takes the active one,
	// falling back to the first by id — a choice, not a coin toss, so two
	// loads of one store draw the same page.
	SchemaID string
	// Usage requests the store's own vocabulary alongside the declarations.
	// False is the ?gap=0 page: the declarations alone, and nothing read for
	// the overlay — the same bargain the contract panel makes when it is
	// folded, which is that work nobody is looking at does not get done.
	Usage bool
}

// ontologyKey folds a type name the way the ontology's own matching does.
//
// Mirrors cortexdb.ontologyAPIKey, which is unexported. Spelled out rather
// than approximated because getting it wrong here does not fail — it produces
// a page reporting "Airport" as declared-but-unused and "airport" as an
// undeclared stray, which is two confident findings from one type.
func ontologyKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// unavailableOntology is the report for a view that cannot answer, for the
// same reason unavailableContract is a report rather than an HTTP error: a
// failed fetch leaves the page showing whatever it drew last, which after a
// source change is the previous store's schema presented as this one's.
func unavailableOntology(reason string) OntologyReport {
	return OntologyReport{
		State:       OntologyUnreadable,
		Reason:      reason,
		Schemas:     []OntologySchemaRef{},
		ObjectTypes: []OntologyObjectNode{},
		LinkTypes:   []OntologyLinkEdge{},
		Interfaces:  []OntologyInterfaceNode{},
		Usage:       emptyOntologyUsage(""),
		At:          time.Now().UnixMilli(),
	}
}

// emptyOntologyUsage is a usage reading that was not taken. Lists are empty
// rather than nil because the page indexes them.
func emptyOntologyUsage(reason string) OntologyUsage {
	return OntologyUsage{
		Reason:          reason,
		UndeclaredNodes: []OntologyStrayType{},
		UndeclaredEdges: []OntologyStrayType{},
	}
}

// multiplicityOf reads the two sides as one phrase.
//
// The sides are named as they are declared: an A side of ONE means a
// traversal starting at A reaches one B, so the pair reads A-to-B. Anything
// the schema left blank stays blank rather than being guessed at — an
// undeclared cardinality is a schema that has not decided, and "many" is the
// wrong default to invent for it.
func multiplicityOf(a, b cortexdb.OntologyCardinality) string {
	word := func(c cortexdb.OntologyCardinality) string {
		switch c {
		case cortexdb.OntologyCardinalityOne:
			return "one"
		case cortexdb.OntologyCardinalityMany:
			return "many"
		default:
			return ""
		}
	}
	l, r := word(a), word(b)
	if l == "" || r == "" {
		return ""
	}
	return l + "-to-" + r
}

// propsOf flattens declared properties for display.
func propsOf(in []cortexdb.OntologyProperty) []OntologyProp {
	out := make([]OntologyProp, 0, len(in))
	for _, p := range in {
		out = append(out, OntologyProp{
			APIName:     p.APIName,
			DisplayName: p.DisplayName,
			Kind:        string(p.DataType.Kind),
			Required:    p.Required,
			Searchable:  p.Searchable,
			Vectorized:  p.Vectorized,
		})
	}
	return out
}

// implementorsOf resolves which object types implement an interface,
// following Extends transitively.
//
// The closure is walked with a seen-set, which is also what stops a schema
// with a cycle in its Extends chain from hanging this page. Validation rejects
// cycles, but this reads schemas that were already stored — possibly by an
// older binary — and a view is the wrong place to discover that the hard way.
func implementorsOf(schema cortexdb.OntologySchema, target string) []string {
	extends := make(map[string][]string, len(schema.InterfaceTypes))
	for _, it := range schema.InterfaceTypes {
		extends[ontologyKey(it.APIName)] = it.Extends
	}
	want := ontologyKey(target)

	var reaches func(name string, seen map[string]bool) bool
	reaches = func(name string, seen map[string]bool) bool {
		k := ontologyKey(name)
		if seen[k] {
			return false
		}
		seen[k] = true
		if k == want {
			return true
		}
		for _, parent := range extends[k] {
			if reaches(parent, seen) {
				return true
			}
		}
		return false
	}

	names := make([]string, 0)
	for _, ot := range schema.ObjectTypes {
		for _, impl := range ot.Implements {
			if reaches(impl, map[string]bool{}) {
				names = append(names, ot.APIName)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}

// pickOntologySchema chooses which of several saved schemas the page draws.
//
// The active one, because it is the one deciding what writes are allowed; then
// an explicit request; then the first by id. Never "whichever came back first"
// — the list is ordered by the store but a page that redrew a different schema
// on a refresh would look like the schema had changed.
func pickOntologySchema(schemas []cortexdb.OntologySchema, want string) (cortexdb.OntologySchema, bool) {
	if len(schemas) == 0 {
		return cortexdb.OntologySchema{}, false
	}
	sorted := append([]cortexdb.OntologySchema(nil), schemas...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].SchemaID < sorted[j].SchemaID })

	if want = strings.TrimSpace(want); want != "" {
		for _, s := range sorted {
			if ontologyKey(s.SchemaID) == ontologyKey(want) {
				return s, true
			}
		}
	}
	for _, s := range sorted {
		if s.Active {
			return s, true
		}
	}
	return sorted[0], true
}

// buildOntologyReport shapes the saved schemas and the store's own vocabulary
// into what the page draws.
//
// read nil means the second reading was not taken — either the page asked for
// the declarations alone, or counting failed — and every Instances stays zero
// with Usage.Available false to say the zeros mean nothing.
func buildOntologyReport(schemas []cortexdb.OntologySchema, want string, read *ontologyReading) OntologyReport {
	rep := OntologyReport{
		Available:   true,
		Schemas:     make([]OntologySchemaRef, 0, len(schemas)),
		ObjectTypes: make([]OntologyObjectNode, 0),
		LinkTypes:   make([]OntologyLinkEdge, 0),
		Interfaces:  make([]OntologyInterfaceNode, 0),
		Usage:       emptyOntologyUsage("the store's own vocabulary was not read"),
		At:          time.Now().UnixMilli(),
	}
	nodeCounts, edgeCounts := map[string]int{}, map[string]int{}
	if read != nil {
		rep.Usage = read.Usage
		nodeCounts, edgeCounts = read.nodes, read.edges
	}

	for _, s := range schemas {
		rep.Schemas = append(rep.Schemas, OntologySchemaRef{
			SchemaID: s.SchemaID, Name: s.Name, Version: s.Version, Active: s.Active,
		})
	}
	sort.SliceStable(rep.Schemas, func(i, j int) bool { return rep.Schemas[i].SchemaID < rep.Schemas[j].SchemaID })

	schema, ok := pickOntologySchema(schemas, want)
	if !ok {
		// Asked and answered: this store holds no schema. Distinct from
		// Available false, which is nobody having asked, and the reason the
		// undeclared half below is still worth drawing — a store with no
		// ontology still has a vocabulary.
		rep.State = OntologyAbsent
		return rep
	}
	rep.Saved = true
	rep.SchemaID = schema.SchemaID
	rep.Name = schema.Name
	rep.Description = schema.Description
	rep.Version = schema.Version
	rep.Active = schema.Active
	rep.Enforcement = string(schema.Enforcement)
	if rep.Enforcement == "" {
		// The zero value is the strict default, and the page saying nothing
		// about enforcement would read as "not enforced" — the opposite.
		rep.Enforcement = string(cortexdb.OntologyEnforcementStrict)
	}
	rep.StrictActions = schema.StrictActions
	rep.ActionTypes = len(schema.ActionTypes)
	rep.ObjectSets = len(schema.ObjectSets)
	rep.SharedProperties = len(schema.SharedProperties)

	for _, ot := range schema.ObjectTypes {
		node := OntologyObjectNode{
			APIName:       ot.APIName,
			DisplayName:   ot.DisplayName,
			Description:   ot.Description,
			PrimaryKey:    ot.PrimaryKey,
			TitleProperty: ot.TitleProperty,
			Status:        string(ot.Status),
			Visibility:    string(ot.Visibility),
			Implements:    append([]string{}, ot.Implements...),
			Aliases:       append([]string{}, ot.Aliases...),
			Properties:    propsOf(ot.Properties),
			Instances:     nodeCounts[ontologyKey(ot.APIName)],
		}
		if node.Implements == nil {
			node.Implements = []string{}
		}
		rep.ObjectTypes = append(rep.ObjectTypes, node)
	}
	for _, lt := range schema.LinkTypes {
		rep.LinkTypes = append(rep.LinkTypes, OntologyLinkEdge{
			APIName:     lt.APIName,
			Description: lt.Description,
			Status:      string(lt.Status),
			A: OntologyLinkEnd{
				APIName: lt.A.APIName, DisplayName: lt.A.DisplayName,
				ObjectType: lt.A.ObjectTypeAPIName, Cardinality: string(lt.A.Cardinality),
				ForeignKey: lt.A.ForeignKeyProperty,
			},
			B: OntologyLinkEnd{
				APIName: lt.B.APIName, DisplayName: lt.B.DisplayName,
				ObjectType: lt.B.ObjectTypeAPIName, Cardinality: string(lt.B.Cardinality),
				ForeignKey: lt.B.ForeignKeyProperty,
			},
			Multiplicity: multiplicityOf(lt.A.Cardinality, lt.B.Cardinality),
			Instances:    edgeCounts[ontologyKey(lt.APIName)],
		})
	}
	for _, it := range schema.InterfaceTypes {
		rep.Interfaces = append(rep.Interfaces, OntologyInterfaceNode{
			APIName:      it.APIName,
			DisplayName:  it.DisplayName,
			Description:  it.Description,
			Extends:      append([]string{}, it.Extends...),
			Properties:   propsOf(it.Properties),
			Implementors: implementorsOf(schema, it.APIName),
		})
	}

	// Unused is a claim about the data, so it is only counted when the data
	// was counted. Without the second reading every Instances is zero for the
	// uninteresting reason, and reporting all of them unused would be the one
	// mistake this whole file is arranged to prevent.
	if rep.Usage.Available {
		for _, ot := range rep.ObjectTypes {
			if ot.Instances == 0 {
				rep.DeclaredUnusedTypes++
			}
		}
		for _, lt := range rep.LinkTypes {
			if lt.Instances == 0 {
				rep.DeclaredUnusedLinks++
			}
		}
	}

	switch {
	case rep.Usage.Available &&
		rep.DeclaredUnusedTypes == len(rep.ObjectTypes) &&
		rep.DeclaredUnusedLinks == len(rep.LinkTypes):
		// Everything declared, nothing conforming. A schema describing a store
		// it has never touched — which reads very differently from one whose
		// types are simply all in use, and identically to it on any page that
		// only draws boxes.
		rep.State = OntologyUnused
	default:
		rep.State = OntologyLive
	}
	return rep
}

// buildOntologyReading turns raw type tallies into the second reading, splitting
// them against what the schema declares.
//
// The schema is passed in rather than applied afterwards because "undeclared"
// is only meaningful relative to one, and a store with no schema at all makes
// every type undeclared — which is the correct and most common answer, not an
// edge case to special-case away.
func buildOntologyReading(scope string, nodeTypes, edgeTypes map[string]int, schema cortexdb.OntologySchema) ontologyReading {
	u := emptyOntologyUsage("")
	u.Available = true
	u.Scope = scope
	u.NodeTypes = len(nodeTypes)
	u.EdgeTypes = len(edgeTypes)
	read := ontologyReading{nodes: map[string]int{}, edges: map[string]int{}}

	declaredNodes := make(map[string]bool, len(schema.ObjectTypes))
	for _, ot := range schema.ObjectTypes {
		declaredNodes[ontologyKey(ot.APIName)] = true
	}
	declaredEdges := make(map[string]bool, len(schema.LinkTypes))
	for _, lt := range schema.LinkTypes {
		declaredEdges[ontologyKey(lt.APIName)] = true
	}

	split := func(counts map[string]int, declared map[string]bool, into map[string]int) ([]OntologyStrayType, int, int, int) {
		strays := make([]OntologyStrayType, 0)
		total := 0
		strayRecords := 0
		for name, n := range counts {
			total += n
			key := ontologyKey(name)
			if declared[key] {
				into[key] += n
				continue
			}
			strays = append(strays, OntologyStrayType{Name: name, Count: n})
			strayRecords += n
		}
		// Largest first, ties by name, so the same store draws the same list
		// twice running and the biggest hole in the schema is the one a reader
		// lands on.
		sort.SliceStable(strays, func(i, j int) bool {
			if strays[i].Count != strays[j].Count {
				return strays[i].Count > strays[j].Count
			}
			return strays[i].Name < strays[j].Name
		})
		return strays, total, len(strays), strayRecords
	}

	nodeStrays, nodeTotal, nodeStrayTypes, nodeStrayRecords := split(nodeTypes, declaredNodes, read.nodes)
	edgeStrays, edgeTotal, edgeStrayTypes, edgeStrayRecords := split(edgeTypes, declaredEdges, read.edges)

	u.Nodes, u.Edges = nodeTotal, edgeTotal
	u.UndeclaredNodeTypes, u.UndeclaredEdgeTypes = nodeStrayTypes, edgeStrayTypes
	u.UndeclaredNodeCount, u.UndeclaredEdgeCount = nodeStrayRecords, edgeStrayRecords
	if len(nodeStrays) > ontologyStrayLimit {
		nodeStrays, u.NodeListTruncated = nodeStrays[:ontologyStrayLimit], true
	}
	if len(edgeStrays) > ontologyStrayLimit {
		edgeStrays, u.EdgeListTruncated = edgeStrays[:ontologyStrayLimit], true
	}
	u.UndeclaredNodes, u.UndeclaredEdges = nodeStrays, edgeStrays
	read.Usage = u
	return read
}

// localOntology reads the ontology and the store's vocabulary straight off an
// open database.
func localOntology(db *cortexdb.DB) func(context.Context, OntologyQuery) (OntologyReport, error) {
	return func(ctx context.Context, q OntologyQuery) (OntologyReport, error) {
		listed, err := db.ListOntologySchemas(ctx, cortexdb.OntologyListRequest{})
		if err != nil {
			return OntologyReport{}, fmt.Errorf("ontology list: %w", err)
		}
		schemas := listed.Schemas
		if !q.Usage {
			return buildOntologyReport(schemas, q.SchemaID, nil), nil
		}

		chosen, _ := pickOntologySchema(schemas, q.SchemaID)
		nodeTypes, err := db.Graph().NodeTypeCounts(ctx)
		if err != nil {
			// The declarations are still worth drawing without the overlay, so
			// this degrades the second reading instead of failing the whole
			// report. The page then says the gap could not be measured rather
			// than drawing every type at zero.
			rep := buildOntologyReport(schemas, q.SchemaID, nil)
			rep.Usage = emptyOntologyUsage(fmt.Sprintf("node type counts: %v", err))
			return rep, nil
		}
		edgeTypes, err := db.Graph().EdgeTypeCounts(ctx)
		if err != nil {
			rep := buildOntologyReport(schemas, q.SchemaID, nil)
			rep.Usage = emptyOntologyUsage(fmt.Sprintf("edge type counts: %v", err))
			return rep, nil
		}
		read := buildOntologyReading("counted over every row in the store, chunk nodes included",
			nodeTypes, edgeTypes, chosen)
		return buildOntologyReport(schemas, q.SchemaID, &read), nil
	}
}

// remoteOntology reads the ontology off a shared brain.
//
// Through CallTool, the same door LoadRemote and remoteContract already use:
// one route to a shared brain is one route to keep working, and a server too
// old for the tools fails with a message naming them rather than a transport
// error the page would have to translate.
func remoteOntology(addr, token string) func(context.Context, OntologyQuery) (OntologyReport, error) {
	return func(ctx context.Context, q OntologyQuery) (OntologyReport, error) {
		conn, err := dial(addr, token)
		if err != nil {
			return OntologyReport{}, fmt.Errorf("connect to %s: %w", addr, err)
		}
		defer func() { _ = conn.Close() }()

		callCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		defer cancel()
		client := rpcv1.NewToolsServiceClient(conn)

		call := func(name string, args any, out any) error {
			body, merr := json.Marshal(args)
			if merr != nil {
				return merr
			}
			resp, cerr := client.CallTool(callCtx, &rpcv1.CallToolRequest{Name: name, ArgsJson: string(body)})
			if cerr != nil {
				return remoteToolErr(name, addr, cerr)
			}
			if uerr := json.Unmarshal([]byte(resp.GetResultJson()), out); uerr != nil {
				return fmt.Errorf("decode %s: %w", name, uerr)
			}
			return nil
		}

		var listed cortexdb.OntologyListResponse
		if err := call("ontology_list", cortexdb.OntologyListRequest{}, &listed); err != nil {
			return OntologyReport{}, err
		}
		if !q.Usage {
			return buildOntologyReport(listed.Schemas, q.SchemaID, nil), nil
		}

		chosen, _ := pickOntologySchema(listed.Schemas, q.SchemaID)
		var graphAll cortexdb.GraphListAllResponse
		if err := call("graph_list_all", cortexdb.GraphListAllRequest{Limit: ontologyUsageScan}, &graphAll); err != nil {
			rep := buildOntologyReport(listed.Schemas, q.SchemaID, nil)
			rep.Usage = emptyOntologyUsage(err.Error())
			return rep, nil
		}

		nodeTypes := map[string]int{}
		for _, n := range graphAll.Nodes {
			nodeTypes[n.Type]++
		}
		edgeTypes := map[string]int{}
		for _, e := range graphAll.Edges {
			edgeTypes[e.Type]++
		}
		scope := "counted over the entity graph this brain returns — chunk nodes and the edges that only wire them are not in it"
		if graphAll.Truncated {
			// Said out loud for the same reason the contract panel says which
			// shelf it counted: a tally over part of a store looks exactly
			// like a tally over all of it.
			scope = fmt.Sprintf("%s; and only the %d most-connected of %d nodes came back",
				scope, len(graphAll.Nodes), graphAll.TotalNodes)
		}
		read := buildOntologyReading(scope, nodeTypes, edgeTypes, chosen)
		return buildOntologyReport(listed.Schemas, q.SchemaID, &read), nil
	}
}
