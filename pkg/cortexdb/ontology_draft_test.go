package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Writing down a vocabulary that already exists.
//
// Every test here is about a way the obvious implementation is wrong. A
// faithful conversion of a real brain's node and edge types into an ontology
// produces a schema that is true and useless: 86% of its objects are "we did
// not recognise this" and a record kind, 92% of its relations are provenance,
// and the case collisions an ontology exists to resolve are reproduced intact.
// So the fixture is that brain in miniature — the same proportions, the same
// collisions — and the assertions are what must *not* come out of it.

// derivedBrain writes the shapes a real shared brain has, small enough to
// assert about exactly:
//
//   - `entity`, which is not a type but this codebase's own word for a write
//     that named none — see firstNonEmpty(entity.Type, "entity") on the write
//     path — plus one node nobody typed at all;
//   - `memory` and `document`, record kinds this library's own writers stamp;
//   - `journal`, a record kind nobody listed, recognisable only by its shape;
//   - `host`, `service`, `crate`, `tool`, `project`, the domain vocabulary
//     underneath, carrying the properties a primary key would have to come
//     from;
//   - `Crate` beside `crate`, and `dependsOn` beside `depends_on`, which are
//     the collisions a person decides and a machine must not;
//   - `mcp-server`, a type whose name cannot be an API name at all.
func derivedBrain(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "brain.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}

	var vec float32
	node := func(id, nodeType, content string, props map[string]interface{}) {
		t.Helper()
		vec++
		if err := db.graph.UpsertNode(ctx, &graph.GraphNode{
			ID: id, NodeType: nodeType, Content: content,
			Vector: []float32{vec, 1, 0, 0}, Properties: props,
		}); err != nil {
			t.Fatalf("UpsertNode %s: %v", id, err)
		}
	}
	var edgeSeq int
	edge := func(from, edgeType, to string) {
		t.Helper()
		edgeSeq++
		if err := db.graph.UpsertEdge(ctx, &graph.GraphEdge{
			ID: from + "|" + edgeType + "|" + to, FromNodeID: from, EdgeType: edgeType, ToNodeID: to, Weight: 1,
		}); err != nil {
			t.Fatalf("UpsertEdge %s: %v", from+edgeType+to, err)
		}
	}
	named := func(name string, extra ...string) map[string]interface{} {
		p := map[string]interface{}{"name": name}
		for i := 0; i+1 < len(extra); i += 2 {
			p[extra[i]] = extra[i+1]
		}
		return p
	}

	// The domain vocabulary. `name` is on all of them and distinct on all of
	// them; `arch` is on two hosts and the same on both, which is what makes
	// it a classifier rather than a key.
	node("host:dell", "host", "dell", named("dell", "arch", "x86_64", "_grade", "asserted"))
	node("host:hp", "host", "hp", named("hp", "arch", "x86_64"))
	node("host:mac", "host", "mac", named("mac"))
	node("svc:api", "service", "api", named("api"))
	node("svc:web", "service", "web", named("web"))
	node("crate:serde", "crate", "serde", named("serde"))
	node("crate:tokio", "crate", "tokio", named("tokio"))
	node("Crate:anyhow", "Crate", "anyhow", named("anyhow"))
	node("tool:ripgrep", "tool", "ripgrep", named("ripgrep"))
	// One node, two keys, both on all of it and both "distinct". Every key a
	// one-record type carries scores perfectly, which is why none of them is
	// evidence of anything.
	node("lib:hnsw", "library", "hnswlib", named("hnswlib", "description", "a vector index"))
	node("proj:cortexdb", "project", "cortexdb", named("cortexdb"))
	node("mcp-server:cortex", "mcp-server", "cortex mcp", named("cortex mcp"))

	// The unrecognised majority, and one node nobody typed at all.
	for _, id := range []string{"e1", "e2", "e3", "e4", "e5", "e6"} {
		node("entity:"+id, "entity", id, named(id))
	}
	node("untyped:1", "", "nobody typed this", named("orphan"))

	// Record kinds. `memory` and `document` are stamped by this library's own
	// writers; `journal` is nobody's listed word and has to be recognised by
	// what it looks like — written, pointing at whatever it happened to hold,
	// pointed at by nothing.
	for _, id := range []string{"m1", "m2", "m3", "m4"} {
		node("memory:"+id, "memory", id, map[string]interface{}{"memory": "true"})
	}
	node("doc:1", "document", "a document", map[string]interface{}{"document_id": "d1", "title": "a document"})
	for _, id := range []string{"j1", "j2", "j3"} {
		node("journal:"+id, "journal", id, map[string]interface{}{"at": "2026-09-05"})
	}

	// Provenance. A record mentions a thing; the mention is not a fact about
	// the world, and there are more of them than of everything else together.
	for _, m := range []string{"m1", "m2", "m3", "m4"} {
		for _, e := range []string{"e1", "e2", "e3", "e4", "e5", "e6"} {
			edge("memory:"+m, "mentions", "entity:"+e)
		}
		edge("memory:"+m, "mentions", "host:dell")
	}
	// The unlisted record kind, told apart by pointing at five different kinds
	// of thing: a relation is about two types, a record is about whatever it
	// held.
	for _, j := range []string{"j1", "j2", "j3"} {
		edge("journal:"+j, "logged", "host:hp")
		edge("journal:"+j, "logged", "svc:api")
		edge("journal:"+j, "logged", "crate:serde")
		edge("journal:"+j, "logged", "tool:ripgrep")
		edge("journal:"+j, "logged", "proj:cortexdb")
	}
	// A statistical artifact of the corpus, not an assertion about anything.
	edge("entity:e1", "co_occurs", "entity:e2")
	edge("entity:e2", "co_occurs", "entity:e3")
	// The relation vocabulary's own word for "no type was given".
	edge("entity:e4", "related_to", "entity:e5")

	// The domain relations, which are the small tail underneath all of that.
	edge("svc:api", "runs_on", "host:dell")
	edge("svc:web", "runs_on", "host:hp")
	edge("crate:serde", "depends_on", "crate:tokio")
	edge("crate:tokio", "depends_on", "crate:serde")
	// The same relation under a second spelling. Two spellings may be two
	// things, so nothing here may quietly fold them together.
	edge("Crate:anyhow", "dependsOn", "crate:serde")
	// A relation whose ends are both unclassified: real, and undeclarable
	// until somebody says what an `entity` is.
	edge("entity:e1", "written_in", "entity:e6")
	// An edge type that cannot be an API name.
	edge("proj:cortexdb", "pins.version", "crate:serde")

	return db
}

func draft(t *testing.T, db *DB, req OntologyDraftRequest) *OntologyDraftResponse {
	t.Helper()
	got, err := db.DraftOntology(context.Background(), req)
	if err != nil {
		t.Fatalf("DraftOntology: %v", err)
	}
	return got
}

func findingFor(t *testing.T, findings []OntologyDraftTypeFinding, name string) OntologyDraftTypeFinding {
	t.Helper()
	for _, f := range findings {
		if f.Type == name {
			return f
		}
	}
	t.Fatalf("no finding for node type %q", name)
	return OntologyDraftTypeFinding{}
}

func linkFindingFor(t *testing.T, findings []OntologyDraftLinkFinding, name string) OntologyDraftLinkFinding {
	t.Helper()
	for _, f := range findings {
		if f.Type == name {
			return f
		}
	}
	t.Fatalf("no finding for edge type %q", name)
	return OntologyDraftLinkFinding{}
}

func decisionsOfKind(got *OntologyDraftResponse, kind string) []OntologyDraftDecision {
	out := make([]OntologyDraftDecision, 0)
	for _, d := range got.Decisions {
		if d.Kind == kind {
			out = append(out, d)
		}
	}
	return out
}

func objectTypeNames(schema OntologySchema) []string {
	out := make([]string, 0, len(schema.ObjectTypes))
	for _, o := range schema.ObjectTypes {
		out = append(out, o.APIName)
	}
	return out
}

func linkTypeNames(schema OntologySchema) []string {
	out := make([]string, 0, len(schema.LinkTypes))
	for _, l := range schema.LinkTypes {
		out = append(out, l.APIName)
	}
	return out
}

// The headline finding on a real brain is not a type. Nearly half its nodes
// are typed `entity`, which is this codebase's own word for a write that named
// no type, and a deriver that turns that into an object type has written down
// "we did not recognise this" as a thing in the world.
func TestTheUnrecognisedMajorityIsNotAnObjectType(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})

	if contains(objectTypeNames(got.Schema), "entity") {
		t.Error("`entity` became an object type; it is the absence of one")
	}
	entity := findingFor(t, got.Report.NodeTypes, "entity")
	if entity.Bucket != OntologyBucketUnclassified {
		t.Errorf("entity bucketed %q, want %q", entity.Bucket, OntologyBucketUnclassified)
	}
	if entity.Rule != OntologyDraftRuleFallbackType {
		t.Errorf("entity classified by rule %q, want %q", entity.Rule, OntologyDraftRuleFallbackType)
	}
	if entity.Nodes != 6 {
		t.Errorf("entity counted %d nodes, want 6", entity.Nodes)
	}
	// A node nobody typed is the same finding by a different route, and it
	// must be counted rather than dropped: a deriver whose totals do not add
	// up leaves the reader to explain the gap.
	untyped := findingFor(t, got.Report.NodeTypes, "")
	if untyped.Bucket != OntologyBucketUnclassified || untyped.Rule != OntologyDraftRuleUntyped {
		t.Errorf("the untyped node came back as %+v", untyped)
	}
	if got.Report.Source.Buckets[OntologyBucketUnclassified] != 7 {
		t.Errorf("unclassified holds %d nodes, want the 6 entities and the untyped one",
			got.Report.Source.Buckets[OntologyBucketUnclassified])
	}
}

// A record kind is not a domain entity. `memory` and `document` are units of
// storage this library writes; declaring them as object types describes the
// filing cabinet instead of what is in it.
func TestRecordKindsAreBookkeepingAndSayWhichRuleSaidSo(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})

	for _, name := range []string{"memory", "document"} {
		f := findingFor(t, got.Report.NodeTypes, name)
		if f.Bucket != OntologyBucketBookkeeping {
			t.Errorf("%s bucketed %q, want bookkeeping", name, f.Bucket)
		}
		if f.Rule != OntologyDraftRuleWriterStamped {
			t.Errorf("%s classified by %q, want %q", name, f.Rule, OntologyDraftRuleWriterStamped)
		}
		if contains(objectTypeNames(got.Schema), name) {
			t.Errorf("%s became an object type", name)
		}
	}

	// The rule that matters, because a list of names only ever catches the
	// names on it: a record kind nobody listed, recognised by its shape —
	// pointed at by nothing, pointing at whatever it happened to hold.
	journal := findingFor(t, got.Report.NodeTypes, "journal")
	if journal.Bucket != OntologyBucketBookkeeping || journal.Rule != OntologyDraftRuleRecordShaped {
		t.Errorf("journal came back as %+v, want bookkeeping by %q", journal, OntologyDraftRuleRecordShaped)
	}
	if journal.Why == "" {
		t.Error("journal was bucketed without evidence; a person cannot overrule a verdict with no reasoning")
	}
}

// The whole rulebook has to travel with the verdicts. A report that says
// "bookkeeping" without saying what bookkeeping means is asking to be trusted.
func TestTheReportCarriesTheRulesItApplied(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})
	if len(got.Report.Rules) == 0 {
		t.Fatal("the report states no rules at all")
	}
	stated := map[string]bool{}
	for _, r := range got.Report.Rules {
		if r.Statement == "" {
			t.Errorf("rule %q is named but not stated", r.Name)
		}
		stated[r.Name] = true
	}
	for _, f := range got.Report.NodeTypes {
		if !stated[f.Rule] {
			t.Errorf("node type %q was decided by rule %q, which the report never states", f.Type, f.Rule)
		}
	}
	for _, f := range got.Report.EdgeTypes {
		if !stated[f.Rule] {
			t.Errorf("edge type %q was decided by rule %q, which the report never states", f.Type, f.Rule)
		}
	}
}

// A person must be able to overrule any of it, and the report must say that is
// what happened rather than presenting the caller's decision as its own.
func TestTheCallerCanOverruleTheBucketing(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{
		DomainTypes:      []string{"memory"},
		BookkeepingTypes: []string{"host"},
	})
	memory := findingFor(t, got.Report.NodeTypes, "memory")
	if memory.Bucket != OntologyBucketDomain || memory.Rule != OntologyDraftRuleOverride {
		t.Errorf("memory came back as %+v after being overruled into the domain", memory)
	}
	if !contains(objectTypeNames(got.Schema), "memory") {
		t.Error("memory was overruled into the domain and still is not an object type")
	}
	host := findingFor(t, got.Report.NodeTypes, "host")
	if host.Bucket != OntologyBucketBookkeeping || host.Rule != OntologyDraftRuleOverride {
		t.Errorf("host came back as %+v after being overruled out of the domain", host)
	}
}

// Provenance is not a relation. `Memory —mentions→ Entity` as the central link
// type of a schema is true, is 86% of the edges, and says nothing about the
// world; the same goes for two things having been seen together.
func TestProvenanceEdgesNeverBecomeLinkTypesAndSayWhy(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})
	names := linkTypeNames(got.Schema)

	for _, c := range []struct {
		edgeType string
		rule     string
	}{
		{"mentions", OntologyDraftRuleProvenanceAttachment},
		{"logged", OntologyDraftRuleProvenanceAttachment},
		{"co_occurs", OntologyDraftRuleCoOccurrence},
		{"related_to", OntologyDraftRuleFallbackEdge},
	} {
		if contains(names, c.edgeType) {
			t.Errorf("%s became a link type", c.edgeType)
		}
		f := linkFindingFor(t, got.Report.EdgeTypes, c.edgeType)
		if f.Included {
			t.Errorf("%s reported as included", c.edgeType)
		}
		if f.Rule != c.rule {
			t.Errorf("%s excluded by %q, want %q", c.edgeType, f.Rule, c.rule)
		}
		if f.Why == "" {
			t.Errorf("%s was excluded without a reason", c.edgeType)
		}
	}

	// And the small tail underneath does survive, which is the point of
	// excluding the rest.
	if !contains(names, "runs_on") {
		t.Errorf("runs_on is a domain relation and is not in the draft: %v", names)
	}
}

// Two spellings may genuinely be two things. The schema cannot hold both —
// api names resolve case-insensitively — so the draft describes the one it saw
// more of and hands the other to a person, rather than folding them.
func TestSpellingCollisionsAreDecisionsNotMerges(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})

	names := objectTypeNames(got.Schema)
	if !contains(names, "crate") {
		t.Errorf("the majority spelling `crate` is not in the draft: %v", names)
	}
	if contains(names, "Crate") {
		t.Error("`Crate` and `crate` both became object types; one of them silently won")
	}
	minority := findingFor(t, got.Report.NodeTypes, "Crate")
	if minority.Withheld != OntologyDraftRuleSpellingCollision {
		t.Errorf("Crate withheld as %q, want %q", minority.Withheld, OntologyDraftRuleSpellingCollision)
	}
	// Withheld, not reclassified: it is still a domain type, and if a person
	// says the two are different things it becomes one on its own terms.
	if minority.Bucket != OntologyBucketDomain {
		t.Errorf("Crate bucketed %q; withholding a spelling is not a verdict about what it is", minority.Bucket)
	}

	merges := decisionsOfKind(got, OntologyDecisionMerge)
	var sawCrate, sawDepends bool
	for _, d := range merges {
		if d.Evidence == "" {
			t.Errorf("merge candidate %q carries no counts; a person cannot decide on a name alone", d.Target)
		}
		// The evidence is where both spellings and their counts have to be:
		// a person deciding whether two names are one thing decides on the
		// counts, not on the fact that a collision exists.
		if strings.Contains(d.Evidence, "Crate") && strings.Contains(d.Evidence, "crate") {
			sawCrate = true
		}
		if strings.Contains(d.Evidence, "dependsOn") && strings.Contains(d.Evidence, "depends_on") {
			sawDepends = true
		}
	}
	if !sawCrate {
		t.Errorf("crate/Crate is not on the to-decide list: %+v", merges)
	}
	// The same discipline on the relation side, where the two spellings do not
	// even collide in the schema — nothing forces the question, so the report
	// has to raise it.
	if !sawDepends {
		t.Errorf("depends_on/dependsOn is not on the to-decide list: %+v", merges)
	}
	if contains(linkTypeNames(got.Schema), "dependsOn") {
		t.Error("both spellings of depends_on became link types without anybody deciding")
	}
}

// Cardinality is not in the data. "Every host observed today has one rack" is
// a fact about today, and a guessed ONE is expensive downstream: alchemy raises
// CONFLICT_KIND_CARDINALITY against at_most_one, so it turns ordinary later
// writes into review items.
func TestCardinalityIsAlwaysManyAndSuspicionGoesToAPerson(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})
	if len(got.Schema.LinkTypes) == 0 {
		t.Fatal("no link types at all")
	}
	for _, l := range got.Schema.LinkTypes {
		if l.A.Cardinality != OntologyCardinalityMany || l.B.Cardinality != OntologyCardinalityMany {
			t.Errorf("link type %q declares %s/%s; nothing in the data says either",
				l.APIName, l.A.Cardinality, l.B.Cardinality)
		}
	}
	// runs_on: two services, one host each, no host with two. That is exactly
	// the evidence that looks like ONE and is not, so it must arrive as a
	// question with its counts.
	found := false
	for _, d := range decisionsOfKind(got, OntologyDecisionCardinality) {
		if strings.Contains(d.Target, "runs_on") {
			found = true
			if d.Evidence == "" {
				t.Error("a cardinality suspicion arrived without the observation behind it")
			}
		}
	}
	if !found {
		t.Errorf("nothing raised the one-ness of runs_on: %+v", got.Decisions)
	}
}

// The schema will not validate without a primary key, so the draft must emit
// one — and every one of them is a guess, which the draft has to say in both
// places a reader looks.
func TestEveryPrimaryKeyIsMarkedAGuess(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})

	guesses := map[string]OntologyDraftDecision{}
	for _, d := range append(decisionsOfKind(got, OntologyDecisionPrimaryKey), decisionsOfKind(got, OntologyDecisionNoPrimaryKey)...) {
		guesses[d.Target] = d
	}
	for _, o := range got.Schema.ObjectTypes {
		if o.PrimaryKey == "" {
			t.Errorf("object type %q has no primary key and cannot be saved", o.APIName)
		}
		if _, ok := guesses[o.APIName]; !ok {
			t.Errorf("object type %q declares primary key %q with nobody asked to confirm it",
				o.APIName, o.PrimaryKey)
		}
		if !strings.Contains(strings.ToLower(o.Description), "guess") {
			t.Errorf("object type %q does not say its primary key is a guess: %q", o.APIName, o.Description)
		}
	}

	// `name` is on every host and different on every host, which is the only
	// evidence data can offer. `arch` is on two of three and the same on both,
	// which is a classifier — picking it would re-identify every object.
	var host OntologyObjectType
	for _, o := range got.Schema.ObjectTypes {
		if o.APIName == "host" {
			host = o
		}
	}
	if host.PrimaryKey != "name" {
		t.Errorf("host primary key = %q, want the covered and distinct `name`", host.PrimaryKey)
	}
	if got := guesses["host"].Evidence; !strings.Contains(got, "3") {
		t.Errorf("the host primary key guess does not carry its counts: %q", got)
	}
	// The contract's own keys are not domain properties: they say how well
	// established a record is, not what it is.
	for _, p := range host.Properties {
		if strings.HasPrefix(p.APIName, "_") {
			t.Errorf("host declares %q, which is a contract key rather than a property of a host", p.APIName)
		}
	}
}

// A key is a promise that a value stays unique, and one record cannot evidence
// a promise. Every key a one-node type carries is on all of its records with
// one distinct value, so the best-scoring key is whichever sorted first — which
// on a real brain keyed a `Book` on its description.
func TestOneRecordEvidencesNoPrimaryKey(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})

	var library OntologyObjectType
	for _, o := range got.Schema.ObjectTypes {
		if o.APIName == "library" {
			library = o
		}
	}
	if library.APIName == "" {
		t.Fatal("the one-node type is not in the draft at all")
	}
	if library.PrimaryKey != ontologyDraftPlaceholderKey {
		t.Errorf("library primary key = %q; with one record nothing is evidenced, so it must be the placeholder",
			library.PrimaryKey)
	}
	var asked bool
	for _, d := range decisionsOfKind(got, OntologyDecisionNoPrimaryKey) {
		if d.Target == "library" {
			asked = true
			if !strings.Contains(d.Evidence, "description") || !strings.Contains(d.Evidence, "name") {
				t.Errorf("the question does not list what was there to choose from: %q", d.Evidence)
			}
		}
	}
	if !asked {
		t.Error("a placeholder key was declared without anybody being asked what identifies one of these")
	}

	// And the type that does have records keeps its evidenced key, with the
	// node type named in the evidence rather than left as a blank.
	for _, d := range decisionsOfKind(got, OntologyDecisionPrimaryKey) {
		if d.Target == "host" && !strings.Contains(d.Evidence, `"host"`) {
			t.Errorf("the host evidence does not name the type it counted: %q", d.Evidence)
		}
	}
}

// A type whose name cannot be an API name is a rename somebody has to make,
// not a name for the draft to invent.
func TestANameThatCannotBeAnAPINameIsAskedAbout(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})
	f := findingFor(t, got.Report.NodeTypes, "mcp-server")
	if f.Withheld != OntologyDraftRuleNotAnAPIName {
		t.Errorf("mcp-server withheld as %q, want %q", f.Withheld, OntologyDraftRuleNotAnAPIName)
	}
	renames := decisionsOfKind(got, OntologyDecisionRename)
	var saw bool
	for _, d := range renames {
		if d.Target == "mcp-server" || d.Target == "pins.version" {
			saw = true
		}
	}
	if !saw {
		t.Errorf("nothing asked for a rename: %+v", renames)
	}
	for _, name := range objectTypeNames(got.Schema) {
		if strings.Contains(name, "-") {
			t.Errorf("object type %q would not validate", name)
		}
	}
}

// A relation between two types nobody has classified is real and undeclarable:
// a link type needs two declared ends. Dropping it silently loses the finding
// that the relation exists and the endpoints are the problem.
func TestARelationWithUndeclaredEndsBecomesAQuestion(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})
	f := linkFindingFor(t, got.Report.EdgeTypes, "written_in")
	if f.Included || f.Rule != OntologyDraftRuleEndsNotDeclared {
		t.Errorf("written_in came back as %+v", f)
	}
	var saw bool
	for _, d := range decisionsOfKind(got, OntologyDecisionOrphanLink) {
		if d.Target == "written_in" {
			saw = true
			if !strings.Contains(d.Evidence, "entity") {
				t.Errorf("the question does not name the ends that are the problem: %q", d.Evidence)
			}
		}
	}
	if !saw {
		t.Error("a real relation was dropped without anybody being told why")
	}
}

// Nobody has signed this. A strict schema derived from the data rejects
// nothing by construction — it describes the data — and then refuses the first
// genuinely new fact for being new.
func TestTheDraftIsAVocabularyAndNobodyHasSignedIt(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})
	if got.Schema.Enforcement != OntologyEnforcementVocabulary {
		t.Errorf("enforcement = %q, want vocabulary", got.Schema.Enforcement)
	}
	if got.Schema.Active {
		t.Error("the draft came back active")
	}
	if got.Schema.StrictActions {
		t.Error("the draft closes the generic write path")
	}
	for _, o := range got.Schema.ObjectTypes {
		if o.Status != OntologyStatusExperimental {
			t.Errorf("object type %q is %q, want experimental", o.APIName, o.Status)
		}
	}
	for _, l := range got.Schema.LinkTypes {
		if l.Status != OntologyStatusExperimental {
			t.Errorf("link type %q is %q, want experimental", l.APIName, l.Status)
		}
	}
}

// A draft that cannot be saved is not a draft. This is the gate the deriver
// runs on itself, asserted here so that a change to either side is caught by
// the other.
func TestTheDraftPassesTheValidatorItWillBeSavedThrough(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{})
	if err := validateOntologySchema(got.Schema); err != nil {
		t.Fatalf("the draft does not validate: %v", err)
	}
}

// A deriver that writes is a deriver that decided. Saving is a person's call,
// through ontology_save, on a schema they have read.
func TestDraftingSavesNothing(t *testing.T) {
	db := derivedBrain(t)
	ctx := context.Background()
	draft(t, db, OntologyDraftRequest{})

	listed, err := db.ListOntologySchemas(ctx, OntologyListRequest{})
	if err != nil {
		t.Fatalf("ListOntologySchemas: %v", err)
	}
	if len(listed.Schemas) != 0 {
		t.Fatalf("drafting stored %d schemas", len(listed.Schemas))
	}
}

// A brain with nothing in it drafts an empty schema rather than failing: a
// caller asking what is here before anything is here should be told "nothing",
// which is a different answer from an error.
func TestAnEmptyBrainDraftsNothingWithoutFailing(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "empty.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.graph.InitGraphSchema(context.Background()); err != nil {
		t.Fatalf("schema: %v", err)
	}
	got := draft(t, db, OntologyDraftRequest{})
	if len(got.Schema.ObjectTypes) != 0 || len(got.Schema.LinkTypes) != 0 {
		t.Errorf("an empty brain produced %d object types and %d link types",
			len(got.Schema.ObjectTypes), len(got.Schema.LinkTypes))
	}
	if err := validateOntologySchema(got.Schema); err != nil {
		t.Errorf("the empty draft does not validate: %v", err)
	}
}

// The same brain drafted twice must give the same answer. Everything here is
// built out of Go maps, and a report whose order flaps cannot be diffed
// between two runs — which is the first thing anybody does with two drafts.
func TestTwoDraftsOfOneBrainAgree(t *testing.T) {
	db := derivedBrain(t)
	first := draft(t, db, OntologyDraftRequest{})
	second := draft(t, db, OntologyDraftRequest{})

	if strings.Join(objectTypeNames(first.Schema), ",") != strings.Join(objectTypeNames(second.Schema), ",") {
		t.Errorf("object types differ between runs:\n %v\n %v",
			objectTypeNames(first.Schema), objectTypeNames(second.Schema))
	}
	if strings.Join(linkTypeNames(first.Schema), ",") != strings.Join(linkTypeNames(second.Schema), ",") {
		t.Errorf("link types differ between runs:\n %v\n %v",
			linkTypeNames(first.Schema), linkTypeNames(second.Schema))
	}
	if len(first.Decisions) != len(second.Decisions) {
		t.Errorf("decision count differs between runs: %d then %d", len(first.Decisions), len(second.Decisions))
	}
	for i := range first.Decisions {
		if first.Decisions[i].Target != second.Decisions[i].Target {
			t.Errorf("decision %d differs: %q then %q", i, first.Decisions[i].Target, second.Decisions[i].Target)
		}
	}
}

// The counts a reader acts on. A threshold that quietly dropped small types
// would describe a fraction of the vocabulary while looking like all of it, so
// what it dropped stays in the report.
func TestAThresholdDropsTypesFromTheDraftAndNotFromTheReport(t *testing.T) {
	got := draft(t, derivedBrain(t), OntologyDraftRequest{MinNodes: 3})
	if contains(objectTypeNames(got.Schema), "tool") {
		t.Error("a single-node type survived a threshold of 3")
	}
	f := findingFor(t, got.Report.NodeTypes, "tool")
	if f.Withheld != OntologyDraftRuleBelowThreshold {
		t.Errorf("tool withheld as %q, want %q", f.Withheld, OntologyDraftRuleBelowThreshold)
	}
	if f.Nodes != 1 {
		t.Errorf("tool counted %d nodes", f.Nodes)
	}
}

// The MCP surface. The two guards in tool_mutates_test.go prove the tool is
// declared, classified and reachable; this proves it answers, and that what
// comes back over the wire still carries all three parts. A response that
// arrived as a schema alone would look like a working tool.
func TestTheDraftToolAnswersWithAllThreeParts(t *testing.T) {
	db := derivedBrain(t)
	got, err := db.GraphRAGTools().Call(context.Background(), "ontology_draft", []byte(`{"schema_id":"shared-brain"}`))
	if err != nil {
		t.Fatalf("call ontology_draft: %v", err)
	}
	resp, ok := got.(*OntologyDraftResponse)
	if !ok {
		t.Fatalf("ontology_draft returned %T", got)
	}
	if resp.Schema.SchemaID != "shared-brain" {
		t.Errorf("schema_id = %q, want the caller's", resp.Schema.SchemaID)
	}
	if len(resp.Schema.ObjectTypes) == 0 {
		t.Error("the draft carries no object types")
	}
	if len(resp.Report.NodeTypes) == 0 || len(resp.Report.EdgeTypes) == 0 || len(resp.Report.Rules) == 0 {
		t.Error("the report came back without its reasoning")
	}
	if len(resp.Decisions) == 0 {
		t.Error("nothing was left for a person to decide, which on this fixture is impossible")
	}
}
