package cortexdb

import (
	"context"
	"slices"
	"sort"
	"testing"
)

// facilityEntities is the same three objects every retrieval test below asks
// about: two Facility implementors and one object type that is not one.
func facilityEntities(airportType string) []ToolEntityInput {
	return []ToolEntityInput{
		{Name: "London Heathrow", Type: airportType, Metadata: map[string]string{
			"iataCode": "LHR", "facilityName": "London Heathrow", "position": "51.4700,-0.4543"}},
		{Name: "Sunderland Plant", Type: "Plant", Metadata: map[string]string{
			"plantCode": "SUN", "facilityName": "Sunderland Plant", "position": "54.9060,-1.3830"}},
		{Name: "Depot 4", Type: "Warehouse", Metadata: map[string]string{
			"warehouseCode": "W4", "storageUnits": "120"}},
	}
}

func facilityNames() []string {
	return []string{"London Heathrow", "Sunderland Plant", "Depot 4"}
}

// seedFacilities activates the facility schema and writes the three objects,
// each mentioned by one chunk so graph expansion has an edge to follow.
func seedFacilities(t *testing.T, db *DB, airportType string) string {
	t.Helper()
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: facilitySchema(), Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	return seedFacilityEntities(t, db, airportType)
}

func seedFacilityEntities(t *testing.T, db *DB, airportType string) string {
	t.Helper()
	ctx := context.Background()
	tools := db.GraphRAGTools()

	ingested, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "sites",
		Title:      "Sites",
		Content:    "London Heathrow, Sunderland Plant and Depot 4 are all sites we operate.",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	chunkID := ingested.ChunkNodeIDs[0]

	entities := facilityEntities(airportType)
	for i := range entities {
		entities[i].ChunkIDs = []string{chunkID}
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	return chunkID
}

// foundNodeTypes flattens a find_nodes response into the node types it matched.
func foundNodeTypes(resp *ToolFindNodesResponse) []string {
	types := make([]string, 0, len(resp.Matches))
	for _, match := range resp.Matches {
		for _, node := range match.Nodes {
			types = append(types, node.NodeType)
		}
	}
	sort.Strings(types)
	return types
}

func TestExpandOntologyTypeFilter(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	// Before any schema is active, nothing may be rewritten.
	before, err := db.expandOntologyTypeFilter(ctx, []string{"Facility", "Airport"})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if !slices.Equal(before, []string{"Facility", "Airport"}) {
		t.Fatalf("with no ontology the filter passes through untouched, got %v", before)
	}

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: facilitySchema(), Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	for _, testCase := range []struct {
		name  string
		given []string
		want  []string
	}{
		{"empty stays empty", nil, nil},
		{"interface becomes its implementors", []string{"Facility"}, []string{"Airport", "Plant"}},
		{"object types keep their declared casing", []string{"airport"}, []string{"Airport"}},
		{"unknown types pass through", []string{"Spacecraft"}, []string{"Spacecraft"}},
		// Every name is expanded, not just the first.
		{"several names", []string{"Warehouse", "Facility"}, []string{"Warehouse", "Airport", "Plant"}},
		// An implementor named alongside its interface is listed once: the
		// expanded list is what a traversal filter scans per node.
		{"overlapping names are de-duplicated", []string{"Facility", "Airport"}, []string{"Airport", "Plant"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := db.expandOntologyTypeFilter(ctx, testCase.given)
			if err != nil {
				t.Fatalf("expand: %v", err)
			}
			if !slices.Equal(got, testCase.want) {
				t.Fatalf("expected %v, got %v", testCase.want, got)
			}
		})
	}
}

func TestOntologyCanonicalNodeType(t *testing.T) {
	compiled := compileOntology(facilitySchema())

	if got := ontologyCanonicalNodeType(nil, "airport"); got != "airport" {
		t.Fatalf("without an ontology the caller's spelling is kept, got %q", got)
	}
	if got := ontologyCanonicalNodeType(compiled, "airport"); got != "Airport" {
		t.Fatalf("expected the declared casing, got %q", got)
	}
	if got := ontologyCanonicalNodeType(compiled, "Spacecraft"); got != "Spacecraft" {
		t.Fatalf("an undeclared type is not renamed, got %q", got)
	}
}

func TestFindNodesByInterfaceReturnsAllImplementors(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db, "Airport")

	resp, err := db.GraphRAGTools().FindNodes(context.Background(), ToolFindNodesRequest{
		Names:     facilityNames(),
		NodeTypes: []string{"Facility"},
	})
	if err != nil {
		t.Fatalf("find nodes: %v", err)
	}

	types := foundNodeTypes(resp)
	if len(types) != 2 || types[0] != "Airport" || types[1] != "Plant" {
		t.Fatalf("expected the 2 Facility implementors, got %v", types)
	}
}

func TestFindNodesByInterfaceExcludesNonImplementors(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db, "Airport")

	// Asking only about the Warehouse: it is found by name with no type
	// filter, and must disappear once the filter is the interface it does not
	// implement. Otherwise "2 implementors" could hold because the filter was
	// ignored and the third name simply matched nothing.
	tools := db.GraphRAGTools()
	unfiltered, err := tools.FindNodes(context.Background(), ToolFindNodesRequest{Names: []string{"Depot 4"}})
	if err != nil {
		t.Fatalf("find nodes: %v", err)
	}
	// The chunk mentioning it matches the name too, so this is a membership
	// check rather than an exact one.
	if got := foundNodeTypes(unfiltered); !slices.Contains(got, "Warehouse") {
		t.Fatalf("expected the Warehouse to exist, got %v", got)
	}

	filtered, err := tools.FindNodes(context.Background(), ToolFindNodesRequest{
		Names:     []string{"Depot 4"},
		NodeTypes: []string{"Facility"},
	})
	if err != nil {
		t.Fatalf("find nodes: %v", err)
	}
	if got := foundNodeTypes(filtered); len(got) != 0 {
		t.Fatalf("Warehouse does not implement Facility and must not be returned, got %v", got)
	}
}

func TestFindNodesByObjectTypeIsUnaffectedByInterfaces(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db, "Airport")

	resp, err := db.GraphRAGTools().FindNodes(context.Background(), ToolFindNodesRequest{
		Names:     facilityNames(),
		NodeTypes: []string{"Airport"},
	})
	if err != nil {
		t.Fatalf("find nodes: %v", err)
	}
	if got := foundNodeTypes(resp); len(got) != 1 || got[0] != "Airport" {
		t.Fatalf("expected only Airport, got %v", got)
	}
}

func TestFindNodesTypeFilterPassesThroughWithoutAnActiveOntology(t *testing.T) {
	db := openOntologyTestDB(t)
	// No schema saved: the type filter must mean exactly what it meant before
	// interfaces existed.
	seedFacilityEntities(t, db, "Airport")
	tools := db.GraphRAGTools()

	byInterface, err := tools.FindNodes(context.Background(), ToolFindNodesRequest{
		Names:     facilityNames(),
		NodeTypes: []string{"Facility"},
	})
	if err != nil {
		t.Fatalf("find nodes: %v", err)
	}
	if got := foundNodeTypes(byInterface); len(got) != 0 {
		t.Fatalf("with no ontology, Facility is just an unknown node type, got %v", got)
	}

	byObjectType, err := tools.FindNodes(context.Background(), ToolFindNodesRequest{
		Names:     facilityNames(),
		NodeTypes: []string{"Airport"},
	})
	if err != nil {
		t.Fatalf("find nodes: %v", err)
	}
	if got := foundNodeTypes(byObjectType); len(got) != 1 || got[0] != "Airport" {
		t.Fatalf("with no ontology, a node type filter still filters, got %v", got)
	}
}

func TestExpandGraphByInterfaceReturnsAllImplementors(t *testing.T) {
	db := openOntologyTestDB(t)
	chunkID := seedFacilities(t, db, "Airport")

	resp, err := db.GraphRAGTools().ExpandGraph(context.Background(), ToolExpandGraphRequest{
		NodeIDs:   []string{chunkID},
		MaxHops:   1,
		NodeTypes: []string{"Facility"},
	})
	if err != nil {
		t.Fatalf("expand graph: %v", err)
	}

	types := make([]string, 0, len(resp.Nodes))
	for _, node := range resp.Nodes {
		if node.ID == chunkID {
			continue // the seed is always returned, filter or not
		}
		types = append(types, node.NodeType)
	}
	sort.Strings(types)
	if len(types) != 2 || types[0] != "Airport" || types[1] != "Plant" {
		t.Fatalf("expected the 2 Facility implementors, got %v", types)
	}
}

func TestExpandGraphTypeFilterPassesThroughWithoutAnActiveOntology(t *testing.T) {
	db := openOntologyTestDB(t)
	chunkID := seedFacilityEntities(t, db, "Airport")

	tools := db.GraphRAGTools()
	byInterface, err := tools.ExpandGraph(context.Background(), ToolExpandGraphRequest{
		NodeIDs:   []string{chunkID},
		MaxHops:   1,
		NodeTypes: []string{"Facility"},
	})
	if err != nil {
		t.Fatalf("expand graph: %v", err)
	}
	for _, node := range byInterface.Nodes {
		if node.ID != chunkID {
			t.Fatalf("with no ontology, Facility is just an unknown node type, got %s", node.NodeType)
		}
	}

	byObjectType, err := tools.ExpandGraph(context.Background(), ToolExpandGraphRequest{
		NodeIDs:   []string{chunkID},
		MaxHops:   1,
		NodeTypes: []string{"Airport"},
	})
	if err != nil {
		t.Fatalf("expand graph: %v", err)
	}
	found := 0
	for _, node := range byObjectType.Nodes {
		if node.ID != chunkID {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("with no ontology, a node type filter still filters, got %d neighbours", found)
	}
}

// KnowledgeMemory.Neighbors traverses the graph itself rather than going
// through expand_graph, so it needs its own coverage.
func TestKnowledgeMemoryNeighborsByInterfaceReturnsAllImplementors(t *testing.T) {
	db := openOntologyTestDB(t)
	chunkID := seedFacilities(t, db, "Airport")

	resp, err := db.KnowledgeMemory().Neighbors(context.Background(), KnowledgeMemoryNeighborsRequest{
		NodeID:    chunkID,
		MaxDepth:  1,
		NodeTypes: []string{"Facility"},
	})
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}

	types := make([]string, 0, len(resp.Neighbors))
	for _, node := range resp.Neighbors {
		types = append(types, node.NodeType)
	}
	sort.Strings(types)
	if len(types) != 2 || types[0] != "Airport" || types[1] != "Plant" {
		t.Fatalf("expected the 2 Facility implementors, got %v", types)
	}
}

// The write path stores the object type as the caller spelled it, while node
// IDs fold case — so "airport" and "Airport" are one object whose node_type
// depends on who wrote last. Retrieval expands to the declared spelling, so
// the stored value has to be the declared spelling too.
func TestUpsertEntitiesStoresTheDeclaredObjectTypeCasing(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	seedFacilities(t, db, "airport")

	row := db.SQL().QueryRowContext(ctx,
		`SELECT node_type FROM graph_nodes WHERE id = ?`, ontologyNodeID("Airport", "LHR"))
	var nodeType string
	if err := row.Scan(&nodeType); err != nil {
		t.Fatalf("read node type: %v", err)
	}
	if nodeType != "Airport" {
		t.Fatalf("expected the declared casing %q, got %q", "Airport", nodeType)
	}
}

func TestRetrievalByInterfaceFindsObjectsWrittenWithDifferentCasing(t *testing.T) {
	db := openOntologyTestDB(t)
	chunkID := seedFacilities(t, db, "airport")
	tools := db.GraphRAGTools()

	found, err := tools.FindNodes(context.Background(), ToolFindNodesRequest{
		Names:     facilityNames(),
		NodeTypes: []string{"Facility"},
	})
	if err != nil {
		t.Fatalf("find nodes: %v", err)
	}
	if got := foundNodeTypes(found); len(got) != 2 {
		t.Fatalf("expected the 2 Facility implementors, got %v", got)
	}

	expanded, err := tools.ExpandGraph(context.Background(), ToolExpandGraphRequest{
		NodeIDs:   []string{chunkID},
		MaxHops:   1,
		NodeTypes: []string{"Facility"},
	})
	if err != nil {
		t.Fatalf("expand graph: %v", err)
	}
	neighbours := 0
	for _, node := range expanded.Nodes {
		if node.ID != chunkID {
			neighbours++
		}
	}
	if neighbours != 2 {
		t.Fatalf("expected the 2 Facility implementors, got %d", neighbours)
	}
}
