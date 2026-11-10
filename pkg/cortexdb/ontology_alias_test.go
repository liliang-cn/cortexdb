package cortexdb

import (
	"context"
	"strings"
	"testing"
)

// A vocabulary that folds the spellings writers already use for one kind of
// thing: "pr", "pull request" and "Pull_Request" are all a merge_request.
func aliasSchema() OntologySchema {
	props := []OntologyProperty{
		{APIName: "id", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		{APIName: "name", DataType: OntologyDataType{Kind: OntologyDataString}},
	}
	return OntologySchema{
		SchemaID:       "brain",
		Enforcement:    OntologyEnforcementVocabulary,
		InterfaceTypes: []OntologyInterfaceType{{APIName: "Work"}},
		ObjectTypes: []OntologyObjectType{
			{APIName: "merge_request", PrimaryKey: "id", TitleProperty: "name", Properties: props,
				Implements: []string{"Work"}, Aliases: []string{"pr", "pull request", "Merge_Request"}},
			{APIName: "project", PrimaryKey: "id", TitleProperty: "name", Properties: props, Aliases: []string{"Project", "repository"}},
		},
	}
}

func nodeTypeOf(t *testing.T, db *DB, id string) string {
	t.Helper()
	node, err := db.graph.GetNode(context.Background(), id)
	if err != nil || node == nil {
		t.Fatalf("node %s: %v", id, err)
	}
	return node.NodeType
}

// A write under an alias is stored under the type's own name, on the node its
// name already keys to: the alias changes the spelling, never the identity.
func TestAWriteUnderAnAliasIsStoredUnderTheDeclaredType(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "CortexDB", Type: "repository"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: aliasSchema(), Activate: true}); err != nil {
		t.Fatal(err)
	}
	resp, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "#12", Type: "pr"},
		{Name: "#13", Type: "Pull Request"},
		{Name: "CortexDB", Type: "Project"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(resp.EntityNodeIDs, " "); got != "entity:12 entity:13 entity:cortexdb" {
		t.Fatalf("node ids %q: an alias must not move an entity to a new node", got)
	}
	for id, want := range map[string]string{"entity:12": "merge_request", "entity:13": "merge_request", "entity:cortexdb": "project"} {
		if got := nodeTypeOf(t, db, id); got != want {
			t.Errorf("%s stored as %q, want %q", id, got, want)
		}
	}
}

// A query by the type, or by an interface it implements, finds what was
// written under its aliases before the type declared them.
func TestAQueryByTypeFindsNodesStoredUnderItsAliases(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Fix the race", Type: "pull request"},
		{Name: "Fix the leak", Type: "Merge_Request"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: aliasSchema(), Activate: true}); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []string{"merge_request", "pr", "Work"} {
		resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Fix the race", "Fix the leak"}, NodeTypes: []string{filter}})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range resp.Matches {
			if len(m.Nodes) == 0 {
				t.Errorf("filter %q: %q not found", filter, m.Name)
			}
		}
	}
	expanded, err := db.expandOntologyTypeFilter(ctx, []string{"Work"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(expanded, ","); got != "merge_request,pr,pull request,Merge_Request" {
		t.Errorf("Work expands to %q", got)
	}
}

// An alias has one meaning, or the schema is refused.
func TestAnAliasThatNamesSomethingElseIsRefused(t *testing.T) {
	cases := map[string]func(s *OntologySchema){
		"another type's name": func(s *OntologySchema) {
			s.ObjectTypes[0].Aliases = append(s.ObjectTypes[0].Aliases, "PROJECT")
		},
		"an interface's name": func(s *OntologySchema) {
			s.ObjectTypes[1].Aliases = append(s.ObjectTypes[1].Aliases, "work")
		},
		"another type's alias": func(s *OntologySchema) {
			s.ObjectTypes[1].Aliases = append(s.ObjectTypes[1].Aliases, "PR")
		},
		"blank": func(s *OntologySchema) {
			s.ObjectTypes[0].Aliases = append(s.ObjectTypes[0].Aliases, "  ")
		},
	}
	for name, mutate := range cases {
		schema := aliasSchema()
		mutate(&schema)
		if err := validateOntologySchema(schema); err == nil {
			t.Errorf("%s: an alias naming it was accepted", name)
		}
	}
	if err := validateOntologySchema(aliasSchema()); err != nil {
		t.Errorf("the schema itself, with a case-only alias of its own name: %v", err)
	}
}
