package cortexdb

import "testing"

func TestBulkListingToolsAdvertiseTheirCursor(t *testing.T) {
	want := map[string]bool{"memory_list_all": false, "graph_list_all": false}
	for _, def := range ToolDefinitions() {
		if _, ok := want[def.Name]; !ok {
			continue
		}
		schema, _ := def.InputSchema["properties"].(map[string]any)
		if schema == nil {
			t.Fatalf("%s has no properties", def.Name)
		}
		if _, ok := schema["cursor"]; !ok {
			t.Errorf("%s does not advertise a cursor, so no caller will use one", def.Name)
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
