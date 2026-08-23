package cortexdb

import (
	"context"
	"strings"
	"testing"
)

// An interface was a first-class thing on one half of the schema and did not
// exist on the other. A type filter on FindNodes, search or brain expanded one
// into its implementors; a link side refused to name one at all, so the only
// way to model a relation whose source is polymorphic — anything Protector
// protects a Volume — was to give up and point the side at some open supertype.
// That is not a neutral fallback: cortexdb decides a traversal's direction by
// keeping only nodes of the far side's type, and no node is ever stored under a
// supertype's name, so the traversal returned nothing at all. A schema that
// reads as complete and answers nothing is the failure an ontology is supposed
// to prevent.
//
// These tests pin the whole path: the schema is accepted, an edge written
// between concrete types is oriented against the interface, and a traversal
// from either side reaches the right implementors.

// storageSchema has one relation with a polymorphic source: a Snapshot or a
// Backup protects a Volume. Nothing is ever stored with node_type "Protector".
func storageSchema() OntologySchema {
	stringProp := func(name string) OntologyProperty {
		return OntologyProperty{APIName: name, DataType: OntologyDataType{Kind: OntologyDataString}, Required: true}
	}
	objectType := func(name string, implements ...string) OntologyObjectType {
		return OntologyObjectType{
			APIName:    name,
			PrimaryKey: "code",
			Implements: implements,
			Properties: []OntologyProperty{stringProp("code")},
		}
	}
	return OntologySchema{
		SchemaID: "storage",
		Name:     "Storage",
		InterfaceTypes: []OntologyInterfaceType{
			{APIName: "Protector", Properties: []OntologyProperty{stringProp("code")}},
		},
		ObjectTypes: []OntologyObjectType{
			objectType("Snapshot", "Protector"),
			objectType("Backup", "Protector"),
			objectType("Volume"),
		},
		LinkTypes: []OntologyLinkType{{
			APIName: "protects",
			A: OntologyLinkSide{
				APIName:           "protects",
				ObjectTypeAPIName: "Protector",
				Cardinality:       OntologyCardinalityMany,
			},
			B: OntologyLinkSide{
				APIName:           "protectedBy",
				ObjectTypeAPIName: "Volume",
				Cardinality:       OntologyCardinalityMany,
			},
		}},
	}
}

func activateStorageSchema(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.SaveOntologySchema(context.Background(),
		OntologySaveRequest{Schema: storageSchema(), Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}
}

// seedStorage writes one Snapshot, one Backup and one Volume, each carrying the
// primary key the strict schema requires.
func seedStorage(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.GraphRAGTools().UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{
			{Name: "snap-1", Type: "Snapshot", Metadata: map[string]string{"code": "snap-1"}},
			{Name: "backup-1", Type: "Backup", Metadata: map[string]string{"code": "backup-1"}},
			{Name: "vol-1", Type: "Volume", Metadata: map[string]string{"code": "vol-1"}},
		},
	}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
}

func TestLinkSideMayNameAnInterface(t *testing.T) {
	db := openOntologyTestDB(t)
	activateStorageSchema(t, db)
}

// The write path recovers an edge's direction by matching its endpoint types
// against the two sides. Comparing names would reject every edge of a
// polymorphic relation — a Snapshot endpoint is not spelled "Protector" — so
// the write would be refused as connecting the wrong types.
func TestARelationBetweenTwoImplementorsIsOrientedAgainstTheInterface(t *testing.T) {
	db := openOntologyTestDB(t)
	activateStorageSchema(t, db)
	seedStorage(t, db)
	ctx := context.Background()

	for _, protector := range []string{"snap-1", "backup-1"} {
		if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{
			Relations: []ToolRelationInput{{From: protector, To: "vol-1", Type: "protects"}},
		}); err != nil {
			t.Fatalf("%s protects vol-1: %v", protector, err)
		}
	}
}

// A type implementing neither side must still be refused, or accepting
// interfaces would have widened the check into no check at all.
func TestARelationEndingOutsideTheInterfaceIsStillRejected(t *testing.T) {
	db := openOntologyTestDB(t)
	activateStorageSchema(t, db)
	seedStorage(t, db)

	err := db.validateRelationInputs(context.Background(), []ToolRelationInput{
		{From: "vol-1", To: "vol-1", Type: "protects"},
	})
	if err == nil || !strings.Contains(err.Error(), "connects Protector and Volume") {
		t.Fatalf("expected a Volume->Volume protects to be rejected, got %v", err)
	}
}

// The traversal itself: walking the interface side must reach the far side's
// implementors, and walking it from the far end must reach the near side's.
// Matching the side's name literally kept only nodes whose node_type was
// "Protector", which nothing is stored as, so both directions returned nothing.
func TestTraversalAcrossAnInterfaceSideReachesImplementors(t *testing.T) {
	db := openOntologyTestDB(t)
	activateStorageSchema(t, db)
	seedStorage(t, db)
	ctx := context.Background()

	for _, protector := range []string{"snap-1", "backup-1"} {
		if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{
			Relations: []ToolRelationInput{{From: protector, To: "vol-1", Type: "protects"}},
		}); err != nil {
			t.Fatalf("seed relation: %v", err)
		}
	}

	// From the volume back along "protectedBy": the far side is the interface,
	// so both implementors must come back — one object type would mean the
	// expansion had silently picked a single implementor.
	protectors, err := db.ResolveObjectSet(ctx, ObjectSet{
		Kind:   ObjectSetSearchAround,
		Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Volume"},
		Link:   "protectedBy",
	})
	if err != nil {
		t.Fatalf("search around protectedBy: %v", err)
	}
	if len(protectors) != 2 {
		t.Errorf("walking protectedBy from the volume reached %d nodes, want the Snapshot and the Backup", len(protectors))
	}

	// And forwards from one implementor, which must reach the volume.
	volumes, err := db.ResolveObjectSet(ctx, ObjectSet{
		Kind:   ObjectSetSearchAround,
		Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Snapshot"},
		Link:   "protects",
	})
	if err != nil {
		t.Fatalf("search around protects: %v", err)
	}
	if len(volumes) != 1 {
		t.Errorf("walking protects from the snapshot reached %d nodes, want the one volume", len(volumes))
	}
}

// Direction has to stay real, not merely non-empty: walking the interface side
// from the volume end reaches protectors, which are not the far side's type, so
// it must select nothing.
func TestTraversalAcrossAnInterfaceSideStillHasADirection(t *testing.T) {
	db := openOntologyTestDB(t)
	activateStorageSchema(t, db)
	seedStorage(t, db)
	ctx := context.Background()

	if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{
		Relations: []ToolRelationInput{{From: "snap-1", To: "vol-1", Type: "protects"}},
	}); err != nil {
		t.Fatalf("seed relation: %v", err)
	}

	got, err := db.ResolveObjectSet(ctx, ObjectSet{
		Kind:   ObjectSetSearchAround,
		Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Volume"},
		Link:   "protects",
	})
	if err != nil {
		t.Fatalf("search around: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("walking protects from the volume reached %d nodes, want none — protects runs the other way", len(got))
	}
}

// A side naming neither an object type nor an interface is still a typo, and a
// typo in a schema should cost the save rather than the relation.
func TestLinkSideRejectsATypeThatIsNeitherObjectNorInterface(t *testing.T) {
	schema := storageSchema()
	schema.LinkTypes[0].A.ObjectTypeAPIName = "Protecter"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "Protecter") {
		t.Fatalf("expected the misspelled side type to be rejected, got %v", err)
	}
}

// A foreign key is a column on one concrete row. An interface is a set of
// object types that may each declare the property differently or not at all, so
// accepting one would mean silently picking an implementor's column.
func TestInterfaceLinkSideRejectsAForeignKey(t *testing.T) {
	schema := storageSchema()
	schema.LinkTypes[0].A.Cardinality = OntologyCardinalityOne
	schema.LinkTypes[0].A.ForeignKeyProperty = "code"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "foreign key") {
		t.Fatalf("expected a foreign key on an interface side to be rejected, got %v", err)
	}
}

// A side name identifies one hop from the object type a traversal starts at.
// Interfaces make the collision indirect: "protects" hanging off Protector and
// "protects" hanging off Snapshot do not collide by name, but a traversal
// starting at a Snapshot — which implements Protector — matches both, and which
// one applies would be decided by declaration order.
func TestASideNameCannotCollideAcrossAnInterfaceAndItsImplementor(t *testing.T) {
	schema := storageSchema()
	schema.LinkTypes = append(schema.LinkTypes, OntologyLinkType{
		APIName: "supersedes",
		A: OntologyLinkSide{
			APIName: "protects", ObjectTypeAPIName: "Snapshot", Cardinality: OntologyCardinalityMany,
		},
		B: OntologyLinkSide{
			APIName: "supersededBy", ObjectTypeAPIName: "Volume", Cardinality: OntologyCardinalityMany,
		},
	})

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "both expose side") {
		t.Fatalf("expected the indirect side name collision to be rejected, got %v", err)
	}
}

// An edge's direction is recovered by matching its endpoints against the two
// sides. That works when the sides are disjoint, and when they are the same
// type — a self-link, where either orientation is the same statement. In
// between it does not: an edge between two Backups would match both readings.
func TestLinkEndsThatPartiallyOverlapAreRejected(t *testing.T) {
	schema := storageSchema()
	schema.InterfaceTypes = append(schema.InterfaceTypes, OntologyInterfaceType{
		APIName: "Storable",
		Properties: []OntologyProperty{
			{APIName: "code", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
	})
	// Backup now implements both, so Protector and Storable share exactly it.
	for i := range schema.ObjectTypes {
		switch schema.ObjectTypes[i].APIName {
		case "Backup":
			schema.ObjectTypes[i].Implements = []string{"Protector", "Storable"}
		case "Volume":
			schema.ObjectTypes[i].Implements = []string{"Storable"}
		}
	}
	schema.LinkTypes[0].B.ObjectTypeAPIName = "Storable"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "unambiguous direction") {
		t.Fatalf("expected partially overlapping ends to be rejected, got %v", err)
	}
}

// A self-link is the legitimate case of ends that overlap completely — a
// `conflicts_with` between two Volumes says the same thing read either way.
// Rejecting it would be the overlap check misfiring on the shape it must allow.
func TestASelfLinkWithIdenticalEndsIsAccepted(t *testing.T) {
	schema := storageSchema()
	schema.LinkTypes = append(schema.LinkTypes, OntologyLinkType{
		APIName: "conflictsWith",
		A: OntologyLinkSide{
			APIName: "conflictsWith", ObjectTypeAPIName: "Volume", Cardinality: OntologyCardinalityMany,
		},
		B: OntologyLinkSide{
			APIName: "conflictedBy", ObjectTypeAPIName: "Volume", Cardinality: OntologyCardinalityMany,
		},
	})

	if err := validateOntologySchema(schema); err != nil {
		t.Fatalf("a self-link between one object type must stay legal: %v", err)
	}
}

// Two interfaces with exactly the same implementors are also the self-link
// shape, one level up: every edge reads the same either way round.
func TestALinkBetweenTwoIdenticalInterfaceSetsIsAccepted(t *testing.T) {
	schema := storageSchema()
	schema.InterfaceTypes = append(schema.InterfaceTypes, OntologyInterfaceType{
		APIName: "Restorable",
		Properties: []OntologyProperty{
			{APIName: "code", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
	})
	for i := range schema.ObjectTypes {
		if name := schema.ObjectTypes[i].APIName; name == "Snapshot" || name == "Backup" {
			schema.ObjectTypes[i].Implements = []string{"Protector", "Restorable"}
		}
	}
	schema.LinkTypes[0].B.ObjectTypeAPIName = "Restorable"

	if err := validateOntologySchema(schema); err != nil {
		t.Fatalf("two interfaces over the same implementors must stay legal: %v", err)
	}
}
