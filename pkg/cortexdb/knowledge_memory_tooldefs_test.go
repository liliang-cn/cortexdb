package cortexdb

import "testing"

func TestBulkListingToolsAdvertiseTheirCursor(t *testing.T) {
	// graph_list_all also needs "order": order "id" is how a walk's *first*
	// page is requested, before any cursor exists. Most LLM function-calling
	// clients only ever populate fields the schema lists, so if "order"
	// silently disappeared, no caller could start a full graph walk — and
	// nothing else in this suite would catch it.
	requiredProps := map[string][]string{
		"memory_list_all": {"cursor"},
		"graph_list_all":  {"cursor", "order"},
	}
	want := map[string]bool{"memory_list_all": false, "graph_list_all": false}
	for _, def := range ToolDefinitions() {
		props, ok := requiredProps[def.Name]
		if !ok {
			continue
		}
		schema, _ := def.InputSchema["properties"].(map[string]any)
		if schema == nil {
			t.Fatalf("%s has no properties", def.Name)
		}
		for _, prop := range props {
			if _, ok := schema[prop]; !ok {
				if prop == "order" {
					t.Errorf("%s does not advertise \"order\": order \"id\" is how a caller "+
						"starts a graph walk's first page (before any cursor exists) — without "+
						"it in the schema, no caller can ever begin a full walk", def.Name)
					continue
				}
				t.Errorf("%s does not advertise a cursor, so no caller will use one", def.Name)
			}
		}
		want[def.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s missing from ToolDefinitions()", name)
		}
	}
}

func TestToolCountIsUnchangedByTheCursorWork(t *testing.T) {
	// The cursor adds arguments, not tools. If this moves, something
	// unintended was registered.
	if got := len(ToolDefinitions()); got != toolCount {
		t.Fatalf("tool count %d, want %d", got, toolCount)
	}
}
