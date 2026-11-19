package cortexdb

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// aliasBrain holds Acme Corp, which entity resolution merged ACME Ltd into:
// the alias node is gone and its name is recorded on the canonical entity,
// as --resolve-entities records it.
func aliasBrain(t *testing.T) (*DB, *GraphRAGToolbox, context.Context) {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "alias.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{DocumentID: "d0", Entities: []ToolEntityInput{
		{Name: "Acme Corp", Description: "the canonical name"},
		{Name: "Widget"},
	}}); err != nil {
		t.Fatal(err)
	}
	node, err := db.Graph().GetNode(ctx, EntityNodeID("Acme Corp"))
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["aliases"] = []string{"ACME Ltd"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	return db, tools, ctx
}

// An entity written under a name the canonical entity carries as an alias
// lands on the canonical entity: no node for the old name comes back, and the
// canonical entity keeps its name, its content and its description, and
// gains the writer's document and mention.
func TestUpsertEntitiesUnderAnAliasWritesTheCanonicalEntity(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	acme := EntityNodeID("Acme Corp")
	chunk := graphChunkNodeID("d1", 0)
	resp, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{DocumentID: "d1", Entities: []ToolEntityInput{
		{Name: "acme ltd", Description: "an abbreviation", ChunkIDs: []string{chunk}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resp.EntityNodeIDs, []string{acme}) {
		t.Fatalf("wrote %v, want the canonical entity %s", resp.EntityNodeIDs, acme)
	}
	if nodes, err := db.Graph().GetNodesBatch(ctx, []string{EntityNodeID("ACME Ltd")}); err != nil || len(nodes) != 0 {
		t.Fatalf("the alias's node came back: %v %v", nodes, err)
	}
	node, err := db.Graph().GetNode(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	if node.Content != "Acme Corp" || node.Properties["name"] != "Acme Corp" || node.Properties["description"] != "the canonical name" {
		t.Fatalf("the canonical entity now reads %q, name %v, description %v", node.Content, node.Properties["name"], node.Properties["description"])
	}
	if docs := toStringSlice(node.Properties["source_document_ids"]); !slices.Contains(docs, "d0") || !slices.Contains(docs, "d1") {
		t.Fatalf("the canonical entity's documents are %v, want d0 and d1", docs)
	}
	edges, err := db.Graph().GetEdges(ctx, acme, "in")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(edges, func(e *graph.GraphEdge) bool { return e.FromNodeID == chunk && e.EdgeType == "mentions" }) {
		t.Fatalf("no mention from %s to the canonical entity: %v", chunk, edges)
	}
}

// A node that stands under the name itself wins over an alias: a name
// becomes an alias only once its own node is gone.
func TestUpsertEntitiesKeepsANodeThatStandsUnderTheName(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	// A writer that states the id writes that node, alias or not.
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{ID: EntityNodeID("ACME Ltd"), Name: "ACME Ltd"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Graph().GetNode(ctx, EntityNodeID("ACME Ltd")); err != nil {
		t.Fatal(err)
	}
	resp, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "ACME Ltd"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resp.EntityNodeIDs, []string{EntityNodeID("ACME Ltd")}) {
		t.Fatalf("wrote %v, want the node that stands under the name", resp.EntityNodeIDs)
	}
}

// A relation that names an alias at either end is written from or to the
// canonical entity.
func TestUpsertRelationsFollowTheAliasAtEitherEnd(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	acme, widget := EntityNodeID("Acme Corp"), EntityNodeID("Widget")
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{DocumentID: "d1", Relations: []ToolRelationInput{
		{From: "ACME Ltd", To: "Widget", Type: "makes"},
		{From: "Widget", To: "Acme ltd", Type: "made_by"},
	}}); err != nil {
		t.Fatal(err)
	}
	edges, err := db.Graph().GetEdges(ctx, acme, "both")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range edges {
		got = append(got, e.FromNodeID+" "+e.EdgeType+" "+e.ToNodeID)
	}
	slices.Sort(got)
	want := []string{acme + " makes " + widget, widget + " made_by " + acme}
	if !slices.Equal(got, want) {
		t.Fatalf("the canonical entity's edges are %v, want %v", got, want)
	}
}

// find_nodes answers a name that only an alias still carries with the
// canonical entity, and says the match came through an alias; a name a node
// still stands under is matched as before.
func TestFindNodesAnswersAnAliasWithTheCanonicalEntity(t *testing.T) {
	_, tools, ctx := aliasBrain(t)
	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"acme ltd", "Widget", "Nothing Here"}})
	if err != nil {
		t.Fatal(err)
	}
	m := resp.Matches
	if len(m) != 3 || len(m[0].Nodes) != 1 || m[0].Nodes[0].ID != EntityNodeID("Acme Corp") || m[0].Match != "alias" {
		t.Fatalf("acme ltd: %+v, want the canonical entity through an alias", m)
	}
	if len(m[1].Nodes) == 0 || m[1].Match != "exact" || len(m[2].Nodes) != 0 {
		t.Fatalf("Widget %+v and Nothing Here %+v, want an exact match and none", m[1], m[2])
	}
}

// The alias read goes through an index of the nodes that carry aliases, not
// through every node of the graph: on a brain of 411 066 nodes the scan took
// 0.6 s, once per call of a tool that follows aliases.
func TestTheAliasReadUsesThePartialIndex(t *testing.T) {
	db, _, ctx := aliasBrain(t)
	rows, err := db.query(ctx, "EXPLAIN QUERY PLAN "+entityAliasScan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "idx_graph_nodes_aliased") {
		t.Fatalf("the alias read plans %v, want the partial index", plan)
	}
}

// Two entities that carry the same alias: the first id in order keeps it,
// so the answer does not depend on the order the rows come back in.
func TestAnAliasTwoEntitiesCarryGoesToTheFirstID(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "Zeta Holdings"}}}); err != nil {
		t.Fatal(err)
	}
	node, err := db.Graph().GetNode(ctx, EntityNodeID("Zeta Holdings"))
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["aliases"] = []string{"ACME Ltd"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	table, err := db.entityAliasTable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := table[entityAliasKey("ACME Ltd")]; got != EntityNodeID("Acme Corp") {
		t.Fatalf("ACME Ltd goes to %q, want the first id, %q", got, EntityNodeID("Acme Corp"))
	}
}

// A failed read of the aliases, or of the nodes the names derive, is
// reported by each tool that follows them.
func TestTheToolsReportAFailedReadOfTheAliases(t *testing.T) {
	calls := map[string]func(*GraphRAGToolbox, context.Context) error{
		"upsert_entities": func(tools *GraphRAGToolbox, ctx context.Context) error {
			_, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "ACME Ltd"}}})
			return err
		},
		"upsert_relations": func(tools *GraphRAGToolbox, ctx context.Context) error {
			_, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{{From: "ACME Ltd", To: "Widget", Type: "makes"}}})
			return err
		},
		"find_nodes": func(tools *GraphRAGToolbox, ctx context.Context) error {
			_, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"ACME Ltd"}})
			return err
		},
	}
	for tool, call := range calls {
		t.Run(tool+" without the aliases", func(t *testing.T) {
			db, tools, ctx := aliasBrain(t)
			renameColumn(t, db, "graph_nodes", "properties", "properties_hidden")
			if err := call(tools, ctx); err == nil || !strings.Contains(err.Error(), "read the entity aliases") {
				t.Fatalf("%s with the aliases unreadable: %v", tool, err)
			}
		})
	}
	for _, tool := range []string{"upsert_entities", "upsert_relations"} {
		t.Run(tool+" without the nodes", func(t *testing.T) {
			db, tools, ctx := aliasBrain(t)
			renameColumn(t, db, "graph_nodes", "vector", "vector_hidden")
			if err := calls[tool](tools, ctx); err == nil || !strings.Contains(err.Error(), "read the entities named") {
				t.Fatalf("%s with the nodes unreadable: %v", tool, err)
			}
		})
	}
}

// An entity whose properties cannot be read is skipped; the others still
// give their aliases.
func TestTheAliasReadSkipsAnUnreadableEntity(t *testing.T) {
	db, _, ctx := aliasBrain(t)
	if _, err := db.SQL().Exec(`INSERT INTO graph_nodes (id, vector, content, node_type, properties) VALUES ('entity:aaa_broken', x'', 'Broken', 'entity', '{"aliases": [')`); err != nil {
		t.Fatal(err)
	}
	table, err := db.entityAliasTable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(table) != 1 || table[entityAliasKey("ACME Ltd")] != EntityNodeID("Acme Corp") {
		t.Fatalf("the aliases read %v, want ACME Ltd to Acme Corp alone", table)
	}
}

// Only an id derived from the name follows an alias: an id the writer
// states, or one an ontology keys by type, is written as given, even in the
// request that also names the alias bare.
func TestAnIDTheWriterStatesOrTheOntologyKeysIsNotFollowed(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	acme, ltd := EntityNodeID("Acme Corp"), EntityNodeID("ACME Ltd")
	resp, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "ACME Ltd"},
		{ID: ltd, Name: "ACME Ltd"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resp.EntityNodeIDs, []string{acme, ltd}) {
		t.Fatalf("wrote %v, want the bare name followed and the stated id kept", resp.EntityNodeIDs)
	}
	node, err := db.Graph().GetNode(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["aliases"] = []string{"ACME Ltd", "Acme Limited"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: aliasSchema(), Activate: true}); err != nil {
		t.Fatal(err)
	}
	typed, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Acme Limited", Type: "project", Metadata: map[string]string{"id": "acme-limited"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(typed.EntityNodeIDs) != 1 || typed.EntityNodeIDs[0] == acme || !strings.HasPrefix(typed.EntityNodeIDs[0], ontologyNodeID("project", "")) {
		t.Fatalf("the typed entity was written to %v, want its own typed id", typed.EntityNodeIDs)
	}
}

// A batch that names the canonical entity and one of its aliases writes the
// canonical entity once, with what the entry under its own name says: the
// entry under the alias adds its mention and nothing else, whichever comes
// first.
func TestABatchThatNamesTheEntityAndItsAliasKeepsTheEntitysOwnUpdate(t *testing.T) {
	own := ToolEntityInput{Name: "Acme Corp", Description: "NEW description", Type: "organization"}
	for order, entries := range map[string][]ToolEntityInput{
		"own name first": {own, {Name: "ACME Ltd", Description: "an abbreviation", Type: "nickname", ChunkIDs: []string{graphChunkNodeID("d1", 0)}}},
		"alias first":    {{Name: "ACME Ltd", Description: "an abbreviation", Type: "nickname", ChunkIDs: []string{graphChunkNodeID("d1", 0)}}, own},
	} {
		t.Run(order, func(t *testing.T) {
			db, tools, ctx := aliasBrain(t)
			acme := EntityNodeID("Acme Corp")
			resp, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{DocumentID: "d1", Entities: entries})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(resp.EntityNodeIDs, []string{acme, acme}) {
				t.Fatalf("wrote %v, want the canonical entity for both entries", resp.EntityNodeIDs)
			}
			node, err := db.Graph().GetNode(ctx, acme)
			if err != nil {
				t.Fatal(err)
			}
			if node.Content != "Acme Corp" || node.Properties["name"] != "Acme Corp" || node.Properties["description"] != "NEW description" || node.NodeType != "organization" {
				t.Fatalf("the canonical entity reads %q, name %v, description %v, type %q: want its own entry's update",
					node.Content, node.Properties["name"], node.Properties["description"], node.NodeType)
			}
			if got := toStringSlice(node.Properties["aliases"]); !slices.Equal(got, []string{"ACME Ltd"}) {
				t.Fatalf("the canonical entity's aliases are %v", got)
			}
			edges, err := db.Graph().GetEdges(ctx, acme, "in")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(edges, func(e *graph.GraphEdge) bool {
				return e.FromNodeID == graphChunkNodeID("d1", 0) && e.EdgeType == "mentions"
			}) {
				t.Fatalf("the entry under the alias left no mention: %v", edges)
			}
		})
	}
}

// Two entries under the alias, or two under the entity's own name, are one
// write of the canonical entity: under the alias it keeps its identity,
// under its own name the last entry is the one written, as before.
func TestEntriesThatLandOnOneEntityAreOneWrite(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	acme := EntityNodeID("Acme Corp")
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "ACME Ltd", Description: "first"}, {Name: "acme ltd", Description: "second"},
	}}); err != nil {
		t.Fatal(err)
	}
	node, err := db.Graph().GetNode(ctx, acme)
	if err != nil || node.Properties["description"] != "the canonical name" || node.Content != "Acme Corp" {
		t.Fatalf("two entries under the alias left %+v (%v)", node, err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Acme Corp", Description: "first"}, {Name: "Acme Corp", Description: "second"},
	}}); err != nil {
		t.Fatal(err)
	}
	if node, err = db.Graph().GetNode(ctx, acme); err != nil || node.Properties["description"] != "second" {
		t.Fatalf("two entries under the entity's own name left %+v (%v), want the last", node, err)
	}
}

// find_nodes answers an alias with the canonical entity even when other
// nodes contain the name: the alias ranks after a folded match and before
// containment, and an entity that also contains the name is served once.
func TestFindNodesRanksAnAliasBeforeContainment(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	acme := EntityNodeID("Acme Corp")
	node, err := db.Graph().GetNode(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["aliases"] = []string{"ACME Ltd", "Acme"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "ACME Ltd Pension Fund"}}}); err != nil {
		t.Fatal(err)
	}
	// A chunk that mentions the folded name, as the document that named it
	// still does.
	if err := db.Graph().UpsertNode(ctx, &graph.GraphNode{ID: graphChunkNodeID("d9", 0), Content: "ACME Ltd makes widgets.", NodeType: "chunk"}); err != nil {
		t.Fatal(err)
	}
	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"ACME Ltd", "acme", "ACME Ltd Pension Fund"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := func(m ToolNodeNameMatch) []string {
		var out []string
		for _, n := range m.Nodes {
			out = append(out, n.ID)
		}
		return out
	}
	m := resp.Matches
	if got := ids(m[0]); m[0].Match != "alias" || !slices.Equal(got, []string{acme, EntityNodeID("ACME Ltd Pension Fund"), graphChunkNodeID("d9", 0)}) {
		t.Fatalf("ACME Ltd: %s %v, want the canonical entity through its alias, then what contains the name", m[0].Match, got)
	}
	// "acme" is an alias of Acme Corp and is contained in its name.
	if got := ids(m[1]); m[1].Match != "alias" || got[0] != acme || len(got) != len(slices.Compact(slices.Sorted(slices.Values(got)))) {
		t.Fatalf("acme: %s %v, want the canonical entity first and once", m[1].Match, got)
	}
	if got := ids(m[2]); m[2].Match != "exact" || got[0] != EntityNodeID("ACME Ltd Pension Fund") {
		t.Fatalf("ACME Ltd Pension Fund: %s %v, want its own node, exact", m[2].Match, got)
	}
	// Under a type filter the alias match is held to the filter like any other.
	typed, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"ACME Ltd"}, NodeTypes: []string{"chunk"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(typed.Matches[0]); !slices.Equal(got, []string{graphChunkNodeID("d9", 0)}) || typed.Matches[0].Match != "contains" {
		t.Fatalf("ACME Ltd among chunks: %s %v, want the chunk alone", typed.Matches[0].Match, got)
	}
}

// A relation between an alias and the entity that carries it would be a
// loop on that entity once followed; a merge drops such loops, and so does
// the write. A loop the writer states itself is written as before.
func TestARelationBetweenAnAliasAndItsEntityIsNotWrittenAsALoop(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	acme := EntityNodeID("Acme Corp")
	resp, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "ACME Ltd", To: "Acme Corp", Type: "same_as"},
		{From: "Acme Corp", To: "acme ltd", Type: "same_as"},
		{From: "ACME Ltd", To: "Widget", Type: "makes"},
		{From: "Acme Corp", To: "Acme Corp", Type: "owns"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	edges, err := db.Graph().GetEdges(ctx, acme, "both")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range edges {
		got = append(got, e.FromNodeID+" "+e.EdgeType+" "+e.ToNodeID)
	}
	slices.Sort(got)
	want := []string{acme + " makes " + EntityNodeID("Widget"), acme + " owns " + acme}
	if !slices.Equal(got, want) || len(resp.EdgeIDs) != 2 || resp.Written != 2 {
		t.Fatalf("edges %v, response %+v: want %v and two edges written", got, resp, want)
	}
}

// Under an ontology that validates relations, an endpoint named by an alias
// is followed before it is validated: the relation is written from the
// entity that carries the alias.
func TestAnAliasedEndpointIsFollowedBeforeAnOntologyValidatesIt(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema(), Activate: true}); err != nil {
		t.Fatal(err)
	}
	written, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Zurich Airport", Type: "Airport", Metadata: map[string]string{"iataCode": "ZRH"}},
		{Name: "LX 318", Type: "Flight", Metadata: map[string]string{"flightNumber": "LX318"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	airport := written.EntityNodeIDs[0]
	node, err := db.Graph().GetNode(ctx, airport)
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["aliases"] = []string{"Kloten"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{{From: "kloten", To: "LX 318", Type: "flightDeparture"}}}); err != nil {
		t.Fatalf("a relation from an alias under a validating ontology: %v", err)
	}
	edges, err := db.Graph().GetEdges(ctx, airport, "out")
	if err != nil || len(edges) != 1 || edges[0].ToNodeID != written.EntityNodeIDs[1] {
		t.Fatalf("the airport's edges are %v (%v), want the departure of LX 318", edges, err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{{From: "Nowhere", To: "LX 318", Type: "flightDeparture"}}}); err == nil {
		t.Fatal("a relation from a name nothing carries was written")
	}
}

// An entity the scan of contents leaves out, because it has no content, is
// still the answer to the alias it carries, and the match says so.
func TestFindNodesAnswersAnAliasOfAnEntityWithoutContent(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	bare := &graph.GraphNode{ID: "entity:bare", NodeType: "entity", Properties: map[string]any{"aliases": []string{"Old Bare Name"}}}
	if err := db.Graph().UpsertNode(ctx, bare); err != nil {
		t.Fatal(err)
	}
	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Old Bare Name"}})
	if err != nil {
		t.Fatal(err)
	}
	if m := resp.Matches; len(m) != 1 || len(m[0].Nodes) != 1 || m[0].Nodes[0].ID != bare.ID || m[0].Match != "alias" {
		t.Fatalf("Old Bare Name: %+v, want the entity that carries it, through an alias", m)
	}
}

// Under an active ontology, which resolves the endpoints itself, the
// relation between an alias and its entity is skipped as under none.
func TestTheLoopIsSkippedUnderAnOntologyToo(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: aliasSchema(), Activate: true}); err != nil {
		t.Fatal(err)
	}
	acme := EntityNodeID("Acme Corp")
	resp, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "ACME Ltd", To: "Acme Corp", Type: "same_as"},
		{From: "ACME Ltd", To: acme, Type: "same_as"},
		{From: acme, To: "Acme Corp", Type: "owns"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	edges, err := db.Graph().GetEdges(ctx, acme, "both")
	if err != nil || len(edges) != 1 || edges[0].EdgeType != "owns" || resp.Written != 1 {
		t.Fatalf("edges %v, response %+v (%v): want only the loop the caller stated", edges, resp, err)
	}
}

// With or without an ontology, a node that stands under the name wins over
// an alias, the name compared as its id derives it: an ontology that finds
// no node of that exact content does not send the relation to the entity
// that carries the alias.
func TestAnOntologyKeepsANodeThatStandsUnderTheName(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{ID: EntityNodeID("ACME Ltd"), Name: "ACME Ltd"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: aliasSchema(), Activate: true}); err != nil {
		t.Fatal(err)
	}
	_, _ = tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{{From: "acme ltd", To: "Widget", Type: "makes"}}})
	if edges, err := db.Graph().GetEdges(ctx, EntityNodeID("Acme Corp"), "both"); err != nil || len(edges) != 0 {
		t.Fatalf("the canonical entity has the edges %v (%v), want none: ACME Ltd stands under its own name", edges, err)
	}
}

// An entry under an alias describes the alias, not the entity: its
// description, its type and its metadata, a key "aliases" among them, do
// not reach the canonical entity, which keeps its properties, its alias
// list and its vector, alone or beside another entry under another alias.
func TestAnEntryUnderAnAliasChangesNothingTheEntitySays(t *testing.T) {
	for name, entries := range map[string][]ToolEntityInput{
		"one entry": {{Name: "ACME Ltd", Description: "per the alias", Type: "nickname", Metadata: map[string]string{"sector": "funds", "aliases": "AL"}}},
		"two aliases": {
			{Name: "ACME Ltd", Type: "nickname", Metadata: map[string]string{"sector": "funds"}},
			{Name: "Acme Limited", Description: "per the alias", Type: "nickname", Metadata: map[string]string{"aliases": "AL"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			db, tools, ctx := aliasBrain(t)
			acme := EntityNodeID("Acme Corp")
			before, err := db.Graph().GetNode(ctx, acme)
			if err != nil {
				t.Fatal(err)
			}
			// No description of its own, a second alias, and a vector no
			// name would give it.
			delete(before.Properties, "description")
			before.Properties["aliases"] = []string{"ACME Ltd", "Acme Limited"}
			before.Vector[0] += 0.5
			if err := db.Graph().UpsertNode(ctx, before); err != nil {
				t.Fatal(err)
			}
			resp, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{DocumentID: "d1", Entities: entries})
			if err != nil || slices.ContainsFunc(resp.EntityNodeIDs, func(id string) bool { return id != acme }) {
				t.Fatalf("wrote %+v (%v), want the canonical entity", resp, err)
			}
			after, err := db.Graph().GetNode(ctx, acme)
			if err != nil {
				t.Fatal(err)
			}
			if _, described := after.Properties["description"]; described || after.Properties["sector"] != nil || after.NodeType != before.NodeType || !slices.Equal(after.Vector, before.Vector) {
				t.Fatalf("the canonical entity reads type %q, properties %v: want it as it was", after.NodeType, after.Properties)
			}
			if got := toStringSlice(after.Properties["aliases"]); !slices.Equal(got, []string{"ACME Ltd", "Acme Limited"}) {
				t.Fatalf("the alias list reads %v", after.Properties["aliases"])
			}
			if got := toStringSlice(after.Properties["source_document_ids"]); !slices.Contains(got, "d1") {
				t.Fatalf("the writer's document is not among %v", got)
			}
		})
	}
}

// A confined caller is not sent to an entity none of its collections
// lists: the id the write answers would tell it the entity exists, and its
// name. It writes the name it gave, as before. An entity one of its
// collections lists is followed to.
func TestAConfinedWriterFollowsOnlyToAnEntityItsCollectionsList(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	alpha := WithCallerScope(ctx, alphaScope())
	acme := EntityNodeID("Acme Corp")
	write := func() string {
		t.Helper()
		resp, err := tools.UpsertEntities(alpha, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "ACME Ltd"}}})
		if err != nil || len(resp.EntityNodeIDs) != 1 {
			t.Fatalf("%+v, %v", resp, err)
		}
		return resp.EntityNodeIDs[0]
	}
	node, err := db.Graph().GetNode(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	node.Properties[entityCollectionsKey] = []string{"alpha", "beta"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if got := write(); got != acme {
		t.Fatalf("alpha wrote %q, want the entity alpha's collection lists", got)
	}
	node.Properties[entityCollectionsKey] = []string{"beta"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if got := write(); got != EntityNodeID("ACME Ltd") {
		t.Fatalf("alpha wrote %q, want the id its own name derives", got)
	}
}
