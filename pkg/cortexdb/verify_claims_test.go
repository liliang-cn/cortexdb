package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// claimBrain opens a store whose ontology says a person lives in one city and
// works at one company, and knows and uses many things.
func claimBrain(t *testing.T) (*DB, *GraphRAGToolbox) {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "claims.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	idProp := []OntologyProperty{{APIName: "id", Required: true, DataType: OntologyDataType{Kind: "string"}}}
	one := func(api, a, b string) OntologyLinkType {
		return OntologyLinkType{APIName: api,
			A: OntologyLinkSide{APIName: api + "_a", ObjectTypeAPIName: a, Cardinality: OntologyCardinalityOne},
			B: OntologyLinkSide{APIName: api + "_b", ObjectTypeAPIName: b, Cardinality: OntologyCardinalityMany}}
	}
	many := func(api, a, b string) OntologyLinkType {
		return OntologyLinkType{APIName: api,
			A: OntologyLinkSide{APIName: api + "_a", ObjectTypeAPIName: a, Cardinality: OntologyCardinalityMany},
			B: OntologyLinkSide{APIName: api + "_b", ObjectTypeAPIName: b, Cardinality: OntologyCardinalityMany}}
	}
	_, err = db.SaveOntologySchema(context.Background(), OntologySaveRequest{
		Activate: true,
		Schema: OntologySchema{
			SchemaID: "claims", Name: "claims", Enforcement: OntologyEnforcementVocabulary,
			ObjectTypes: []OntologyObjectType{
				{APIName: "Person", DisplayName: "Person", PrimaryKey: "id", Properties: idProp},
				{APIName: "City", DisplayName: "City", PrimaryKey: "id", Properties: idProp},
				{APIName: "Company", DisplayName: "Company", PrimaryKey: "id", Properties: idProp},
				{APIName: "Tool", DisplayName: "Tool", PrimaryKey: "id", Properties: idProp},
			},
			LinkTypes: []OntologyLinkType{
				one("lives_in", "Person", "City"),
				one("works_at", "Person", "Company"),
				many("knows", "Person", "Person"),
				many("uses", "Person", "Tool"),
			},
		},
	})
	if err != nil {
		t.Fatalf("ontology: %v", err)
	}
	return db, db.GraphRAGTools()
}

func contractMeta(extra map[string]string) map[string]string {
	m := map[string]string{KeyProducer: ProducerLLMExtract, KeyGrade: GradeAsserted, KeySource: "fixture"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func writeRelation(t *testing.T, tools *GraphRAGToolbox, doc string, rel ToolRelationInput) string {
	t.Helper()
	res, err := tools.UpsertRelations(context.Background(), ToolUpsertRelationsRequest{DocumentID: doc, Relations: []ToolRelationInput{rel}})
	if err != nil {
		t.Fatalf("relation %v: %v", rel, err)
	}
	return res.EdgeIDs[0]
}

func TestVerifyClaimsVerdicts(t *testing.T) {
	db, tools := claimBrain(t)
	ctx := context.Background()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Leo", Type: "Person"}, {Name: "Mia", Type: "Person"},
		{Name: "Beijing", Type: "City"}, {Name: "Chengdu", Type: "City"}, {Name: "Vienna", Type: "City"},
		{Name: "Go", Type: "Tool"}, {Name: "Rust", Type: "Tool"},
	}}); err != nil {
		t.Fatal(err)
	}
	writeRelation(t, tools, "d1", ToolRelationInput{From: "Leo", To: "Beijing", Type: "lives_in", ChunkIDs: []string{"d1#1"},
		Metadata: contractMeta(map[string]string{"valid_from": "2019-01-01T00:00:00Z", "valid_to": "2023-01-01T00:00:00Z"})})
	writeRelation(t, tools, "d2", ToolRelationInput{From: "Leo", To: "Chengdu", Type: "lives_in", ChunkIDs: []string{"d2#4"},
		Metadata: contractMeta(map[string]string{"valid_from": "2023-01-01T00:00:00Z"})})
	goEdge := writeRelation(t, tools, "d3", ToolRelationInput{From: "Leo", To: "Go", Type: "uses", Metadata: contractMeta(nil)})
	writeRelation(t, tools, "d4", ToolRelationInput{From: "Mia", To: "Go", Type: "avoids",
		Metadata: contractMeta(map[string]string{KeyContradicts: `["` + goEdge + `"]`})})
	rustEdge := writeRelation(t, tools, "d5", ToolRelationInput{From: "Mia", To: "Rust", Type: "uses", Metadata: contractMeta(nil)})
	if err := db.Graph().DeleteEdge(ctx, rustEdge); err != nil {
		t.Fatal(err)
	}

	res, err := db.VerifyClaims(ctx, []Claim{
		{"Leo", "lives_in", "Chengdu"},               // supported
		{"leo", "Lives In", "chengdu"},               // supported, folded
		{EntityNodeID("Leo"), "LIVES-IN", "Chengdu"}, // supported, by id
		{"Leo", "lives_in", "Beijing"},               // contradicted: ended, and another value current
		{"Leo", "lives_in", "Vienna"},                // contradicted: single-valued
		{"Leo", "lives_in", "Atlantis"},              // contradicted: single-valued, object unknown
		{"Leo", "uses", "Go"},                        // contradicted: explicit
		{"Leo", "uses", "Rust"},                      // absent: no edge
		{"Mia", "uses", "Rust"},                      // absent: retracted
		{"Nobody", "uses", "Go"},                     // absent: unknown subject
		{"Leo", "knows", "Atlantis"},                 // absent: unknown object
		{"Leo", "lives_in", "Beijing"},               // repeated claim, same answer
	}, VerifyClaimsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ status, reason string }{
		{ClaimSupported, ClaimReasonCurrentEdge},
		{ClaimSupported, ClaimReasonCurrentEdge},
		{ClaimSupported, ClaimReasonCurrentEdge},
		{ClaimContradicted, ClaimReasonSingleValuedConflict},
		{ClaimContradicted, ClaimReasonSingleValuedConflict},
		{ClaimContradicted, ClaimReasonSingleValuedConflict},
		{ClaimContradicted, ClaimReasonExplicit},
		{ClaimAbsent, ClaimReasonNoEdge},
		{ClaimAbsent, ClaimReasonRetracted},
		{ClaimAbsent, ClaimReasonUnknownSubject},
		{ClaimAbsent, ClaimReasonUnknownObject},
		{ClaimContradicted, ClaimReasonSingleValuedConflict},
	}
	for i, w := range want {
		got := res.Verdicts[i]
		if got.Status != w.status || got.Reason != w.reason {
			t.Errorf("claim %d %v: got %s/%s, want %s/%s", i, got.Claim, got.Status, got.Reason, w.status, w.reason)
		}
	}

	// A supported verdict cites its source and carries the contract.
	sup := res.Verdicts[0]
	if len(sup.Evidence) != 1 || sup.Evidence[0].Provenance.DocumentID != "d2" ||
		sup.Evidence[0].Grade != GradeAsserted || sup.Evidence[0].Producer != ProducerLLMExtract || !sup.Evidence[0].Cited {
		t.Errorf("supported evidence = %+v", sup.Evidence)
	}
	// A contradicted one names what the graph holds instead.
	con := res.Verdicts[4]
	if con.SingleValuedBy != "ontology" || len(con.Evidence) == 0 || con.Evidence[0].Role != EvidenceOtherValue ||
		con.Evidence[0].ObjectID != EntityNodeID("Chengdu") {
		t.Errorf("contradicted evidence = %+v", con)
	}

	// In 2020 Leo lived in Beijing, so Chengdu was the other value then.
	past, err := db.VerifyClaims(ctx, []Claim{{"Leo", "lives_in", "Beijing"}, {"Leo", "lives_in", "Chengdu"}},
		VerifyClaimsOptions{At: time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if past.Verdicts[0].Status != ClaimSupported || past.Verdicts[1].Status != ClaimContradicted || past.Verdicts[1].Reason != ClaimReasonSingleValuedConflict {
		t.Errorf("as-of verdicts = %s/%s, %s/%s", past.Verdicts[0].Status, past.Verdicts[0].Reason, past.Verdicts[1].Status, past.Verdicts[1].Reason)
	}

	// The tool surface answers the same through Call.
	raw, _ := json.Marshal(ToolVerifyClaimsRequest{Claims: []Claim{{"Leo", "lives_in", "Vienna"}}})
	out, err := tools.Call(ctx, "verify_claims", raw)
	if err != nil {
		t.Fatal(err)
	}
	if r := out.(VerifyClaimsResult); r.Verdicts[0].Status != ClaimContradicted {
		t.Errorf("tool verdict = %+v", r.Verdicts[0])
	}
}

// TestVerifyClaimsPrecisionRecall is the measurement: a seeded graph, a known
// set of true facts, fabricated triples and contradicted triples, and the
// precision and recall of each verdict class.
func TestVerifyClaimsPrecisionRecall(t *testing.T) {
	db, tools := claimBrain(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(7))

	const people, cities, companies, toolsN = 30, 10, 8, 12
	person := func(i int) string { return fmt.Sprintf("Person %02d", i) }
	city := func(i int) string { return fmt.Sprintf("City %02d", i) }
	company := func(i int) string { return fmt.Sprintf("Company %02d", i) }
	tool := func(i int) string { return fmt.Sprintf("Tool %02d", i) }
	ents := []ToolEntityInput{}
	for i := 0; i < people; i++ {
		ents = append(ents, ToolEntityInput{Name: person(i), Type: "Person"})
	}
	for i := 0; i < cities; i++ {
		ents = append(ents, ToolEntityInput{Name: city(i), Type: "City"})
	}
	for i := 0; i < companies; i++ {
		ents = append(ents, ToolEntityInput{Name: company(i), Type: "Company"})
	}
	for i := 0; i < toolsN; i++ {
		ents = append(ents, ToolEntityInput{Name: tool(i), Type: "Tool"})
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: ents}); err != nil {
		t.Fatal(err)
	}

	type labeled struct {
		claim    Claim
		expected string
		category string
	}
	var set []labeled
	add := func(c Claim, exp, cat string) { set = append(set, labeled{c, exp, cat}) }
	doc := 0
	rel := func(from, to, typ string, meta map[string]string) string {
		doc++
		return writeRelation(t, tools, fmt.Sprintf("doc-%d", doc), ToolRelationInput{
			From: from, To: to, Type: typ, ChunkIDs: []string{fmt.Sprintf("doc-%d#0", doc)}, Metadata: contractMeta(meta)})
	}
	variant := func(i int, c Claim) Claim {
		// A third of the true claims are phrased the way a model writes them.
		if i%3 == 0 {
			return Claim{strings.ToLower(c.Subject), strings.ReplaceAll(c.Relation, "_", " "), strings.ToUpper(c.Object)}
		}
		return c
	}

	liveCity := make([]int, people)
	job := make([]int, people)
	born := make([]int, people)
	knows := map[[2]int]bool{}
	uses := map[[2]int]bool{}
	var usesEdges [][3]int // person, tool, edge index into usesIDs
	usesIDs := []string{}
	for p := 0; p < people; p++ {
		liveCity[p] = rng.Intn(cities)
		if p < 10 { // movers: an ended interval before the current one
			prev := (liveCity[p] + 1 + rng.Intn(cities-1)) % cities
			rel(person(p), city(prev), "lives_in", map[string]string{"valid_from": "2019-01-01T00:00:00Z", "valid_to": "2023-01-01T00:00:00Z"})
			add(Claim{person(p), "lives_in", city(prev)}, ClaimContradicted, "ended (moved away)")
		}
		rel(person(p), city(liveCity[p]), "lives_in", map[string]string{"valid_from": "2023-01-01T00:00:00Z"})
		add(variant(p, Claim{person(p), "lives_in", city(liveCity[p])}), ClaimSupported, "true lives_in")

		job[p] = rng.Intn(companies)
		rel(person(p), company(job[p]), "works_at", nil)
		add(variant(p+1, Claim{person(p), "works_at", company(job[p])}), ClaimSupported, "true works_at")

		born[p] = rng.Intn(cities)
		rel(person(p), city(born[p]), "born_in", nil)
		add(variant(p+2, Claim{person(p), "born_in", city(born[p])}), ClaimSupported, "true born_in")

		for k := 0; k < 2; k++ {
			q := (p + 1 + rng.Intn(people-1)) % people
			if !knows[[2]int{p, q}] {
				knows[[2]int{p, q}] = true
				rel(person(p), person(q), "knows", nil)
				add(Claim{person(p), "knows", person(q)}, ClaimSupported, "true knows")
			}
			u := rng.Intn(toolsN)
			if !uses[[2]int{p, u}] {
				uses[[2]int{p, u}] = true
				usesIDs = append(usesIDs, rel(person(p), tool(u), "uses", nil))
				usesEdges = append(usesEdges, [3]int{p, u, len(usesIDs) - 1})
			}
		}
	}
	// uses: 5 explicitly contradicted, 3 retracted, the rest true.
	for i, ue := range usesEdges {
		c := Claim{person(ue[0]), "uses", tool(ue[1])}
		switch {
		case i < 5:
			rel(person((ue[0]+1)%people), tool(ue[1]), "reports_not_using",
				map[string]string{KeyContradicts: `["` + usesIDs[ue[2]] + `"]`})
			add(c, ClaimContradicted, "explicit _contradicts")
		case i < 8:
			if err := db.Graph().DeleteEdge(ctx, usesIDs[ue[2]]); err != nil {
				t.Fatal(err)
			}
			add(c, ClaimAbsent, "retracted")
		default:
			add(c, ClaimSupported, "true uses")
		}
	}
	// Contradicted: a different value on a single-valued link.
	for i := 0; i < 20; i++ {
		p := rng.Intn(people)
		add(Claim{person(p), "lives_in", city((liveCity[p] + 1 + rng.Intn(cities-1)) % cities)}, ClaimContradicted, "wrong lives_in")
	}
	for i := 0; i < 15; i++ {
		p := rng.Intn(people)
		add(Claim{person(p), "works_at", company((job[p] + 1 + rng.Intn(companies-1)) % companies)}, ClaimContradicted, "wrong works_at")
	}
	for i := 0; i < 15; i++ {
		p := rng.Intn(people)
		add(Claim{person(p), "born_in", city((born[p] + 1 + rng.Intn(cities-1)) % cities)}, ClaimContradicted, "wrong born_in (not in ontology)")
	}
	for i := 0; i < 10; i++ {
		add(Claim{person(rng.Intn(people)), "lives_in", fmt.Sprintf("Atlantis %d", i)}, ClaimContradicted, "fabricated object on single-valued link")
	}
	// Fabricated: nothing in the graph either way.
	for n := 0; n < 20; {
		p, q := rng.Intn(people), rng.Intn(people)
		if p == q || knows[[2]int{p, q}] {
			continue
		}
		knows[[2]int{p, q}] = true
		add(Claim{person(p), "knows", person(q)}, ClaimAbsent, "fabricated knows")
		n++
	}
	for n := 0; n < 15; {
		p, u := rng.Intn(people), rng.Intn(toolsN)
		if uses[[2]int{p, u}] {
			continue
		}
		uses[[2]int{p, u}] = true
		add(Claim{person(p), "uses", tool(u)}, ClaimAbsent, "fabricated uses")
		n++
	}
	for i := 0; i < 10; i++ {
		add(Claim{fmt.Sprintf("Ghost %d", i), "lives_in", city(i % cities)}, ClaimAbsent, "fabricated subject")
	}
	for i := 0; i < 10; i++ {
		add(Claim{person(i), "mentors", person(i + 1)}, ClaimAbsent, "fabricated relation")
	}
	for i := 0; i < 5; i++ {
		// Near-miss names: containment must not match.
		add(Claim{fmt.Sprintf("Person %02d Junior", i), "lives_in", city(liveCity[i])}, ClaimAbsent, "near-miss subject name")
	}

	claims := make([]Claim, len(set))
	for i, l := range set {
		claims[i] = l.claim
	}
	classes := []string{ClaimSupported, ClaimContradicted, ClaimAbsent}

	measure := func(label string, opts VerifyClaimsOptions) map[string][2]float64 {
		start := time.Now()
		res, err := db.VerifyClaims(ctx, claims, opts)
		if err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		confusion := map[string]map[string]int{}
		missesByCat := map[string]int{}
		for _, c := range classes {
			confusion[c] = map[string]int{}
		}
		for i, v := range res.Verdicts {
			confusion[set[i].expected][v.Status]++
			if v.Status != set[i].expected {
				missesByCat[set[i].category+" -> "+v.Status]++
			}
		}
		out := map[string][2]float64{}
		t.Logf("[%s] %d claims in %s", label, len(claims), elapsed.Round(time.Millisecond))
		for _, c := range classes {
			tp := confusion[c][c]
			predicted, actual := 0, 0
			for _, e := range classes {
				predicted += confusion[e][c]
				actual += confusion[c][e]
			}
			p, r := 1.0, 1.0
			if predicted > 0 {
				p = float64(tp) / float64(predicted)
			}
			if actual > 0 {
				r = float64(tp) / float64(actual)
			}
			out[c] = [2]float64{p, r}
			t.Logf("[%s] %-12s n=%3d  precision=%.3f  recall=%.3f", label, c, actual, p, r)
		}
		keys := make([]string, 0, len(missesByCat))
		for k := range missesByCat {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.Logf("[%s] miss: %s x%d", label, k, missesByCat[k])
		}
		return out
	}

	// Ontology only: born_in is not declared anywhere, so a wrong birthplace
	// cannot be told from a second one and is reported absent.
	base := measure("ontology only", VerifyClaimsOptions{})
	if base[ClaimSupported] != [2]float64{1, 1} {
		t.Errorf("supported precision/recall = %v, want 1/1", base[ClaimSupported])
	}
	if base[ClaimContradicted][0] != 1 {
		t.Errorf("contradicted precision = %v, want 1", base[ClaimContradicted][0])
	}
	// With born_in declared single-valued the gap closes.
	declared := measure("born_in declared", VerifyClaimsOptions{SingleValued: []string{"born_in"}})
	for _, c := range classes {
		if declared[c] != [2]float64{1, 1} {
			t.Errorf("%s precision/recall with declaration = %v, want 1/1", c, declared[c])
		}
	}
}
