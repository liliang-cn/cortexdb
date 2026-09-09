package cortexdb

import (
	"context"
	"testing"
)

// Naming an object that already exists must not erase what is known about it.
//
// The pattern this protects is the documented one: a document declares the
// entities it mentions, and a mention carries identity and nothing else — you
// name "sds-meta" in a runbook, you do not restate its purpose or its quorum
// setting. Before this, the second write replaced the property map and every
// domain property the first write established was gone, silently, with the
// ontology's required-property check still passing because the primary key was
// present.
func TestUpsertEntitiesKeepsPropertiesTheCallerDidNotMention(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{
			Name: "London Heathrow", Type: "Airport",
			Metadata: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		}},
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// The shape a document's entity declaration has: identity, no detail.
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "doc-1",
		Entities: []ToolEntityInput{{
			Name: "London Heathrow", Type: "Airport",
			Metadata: map[string]string{"iataCode": "LHR"},
		}},
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	node := requireEntityNode(t, db, "Airport", "iataCode", "LHR")
	if got := node.Properties["airportName"]; got != "London Heathrow" {
		t.Fatalf("airportName was erased by a write that never mentioned it: %#v", node.Properties)
	}
	if got := node.Properties["iataCode"]; got != "LHR" {
		t.Fatalf("iataCode = %#v", got)
	}
}

// A caller that does restate a property still wins. Merging is about what was
// left out, never about refusing an update.
func TestUpsertEntitiesLetsANamedPropertyWin(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	for _, name := range []string{"Heathrow", "London Heathrow"} {
		if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
			Entities: []ToolEntityInput{{
				Name: "London Heathrow", Type: "Airport",
				Metadata: map[string]string{"iataCode": "LHR", "airportName": name},
			}},
		}); err != nil {
			t.Fatalf("upsert %q: %v", name, err)
		}
	}

	node := requireEntityNode(t, db, "Airport", "iataCode", "LHR")
	if got := node.Properties["airportName"]; got != "London Heathrow" {
		t.Fatalf("the later value should win, got %#v", got)
	}
}

// The merge must not resurrect a property from a DIFFERENT object that happens
// to share a node id shape, and must not leak across types.
func TestUpsertEntitiesMergesPerObject(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{
			{Name: "London Heathrow", Type: "Airport",
				Metadata: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"}},
			{Name: "BA117", Type: "Flight",
				Metadata: map[string]string{"flightNumber": "BA117", "originIata": "LHR"}},
		},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{
			{Name: "BA117", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA117"}},
		},
	}); err != nil {
		t.Fatalf("re-declare: %v", err)
	}

	flight := requireEntityNode(t, db, "Flight", "flightNumber", "BA117")
	if got := flight.Properties["originIata"]; got != "LHR" {
		t.Fatalf("the flight's origin was erased: %#v", flight.Properties)
	}
	if _, leaked := flight.Properties["airportName"]; leaked {
		t.Fatalf("a property from another object leaked in: %#v", flight.Properties)
	}
}

// requireEntityNode finds one object by its ontology type and primary key.
func requireEntityNode(t *testing.T, db *DB, objectType, keyProperty, key string) *graphNodeView {
	t.Helper()
	ctx := context.Background()
	resolved, err := db.ResolveObjectSetObjects(ctx, ObjectSetResolveRequest{
		ObjectSet: ObjectSet{Kind: ObjectSetBase, ObjectType: objectType},
	})
	if err != nil {
		t.Fatalf("resolve %s: %v", objectType, err)
	}
	for _, object := range resolved.Objects {
		node, err := db.Graph().GetNode(ctx, object.ObjectID)
		if err != nil {
			t.Fatalf("get node %s: %v", object.ObjectID, err)
		}
		if fmtProperty(node.Properties[keyProperty]) == key {
			return &graphNodeView{Properties: stringProperties(node.Properties)}
		}
	}
	t.Fatalf("no %s with %s = %q", objectType, keyProperty, key)
	return nil
}

type graphNodeView struct{ Properties map[string]string }

func stringProperties(in map[string]interface{}) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = fmtProperty(v)
	}
	return out
}

func fmtProperty(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// The knowledge path is a second door onto the same act and had the same bug.
//
// SaveKnowledge builds its declared-entity nodes itself rather than going
// through the toolbox upsert, so fixing one left the other erasing properties —
// and this is the door the documented pattern actually uses: a document names
// the entities it mentions, carrying identity and nothing else.
func TestSaveKnowledgeEntitiesDoNotEraseAnObjectsProperties(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()

	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{
			Name: "London Heathrow", Type: "Airport",
			Metadata: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		}},
	}); err != nil {
		t.Fatalf("seed the estate: %v", err)
	}

	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "note-1",
		Title:       "A note that merely mentions the airport",
		Content:     "Ground handling at the airport is contracted out.",
		Entities: []ToolEntityInput{{
			Name: "London Heathrow", Type: "Airport",
			Metadata: map[string]string{"iataCode": "LHR"},
		}},
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}

	node := requireEntityNode(t, db, "Airport", "iataCode", "LHR")
	if got := node.Properties["airportName"]; got != "London Heathrow" {
		t.Fatalf("mentioning the airport in a note erased its name: %#v", node.Properties)
	}
}
