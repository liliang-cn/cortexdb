package cortexdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// The graph tools answer without node embeddings unless asked, and leave
// the stored node untouched either way.
func TestGraphToolsLeaveVectorsOutUnlessAsked(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "vec.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for _, n := range []*graph.GraphNode{
		{ID: "entity:kiln", Content: "Kiln", NodeType: "thing", Vector: []float32{0.25, 0.5, 0.75}},
		{ID: "entity:glaze", Content: "Glaze", NodeType: "thing", Vector: []float32{0.5, 0.5, 0.5}},
	} {
		if err := db.graph.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.graph.UpsertEdge(ctx, &graph.GraphEdge{ID: "e1", FromNodeID: "entity:kiln", ToNodeID: "entity:glaze", EdgeType: "uses", Weight: 1}); err != nil {
		t.Fatal(err)
	}
	tools := db.GraphRAGTools()
	calls := map[string]string{
		"find_nodes":   `{"names":["Kiln"]%s}`,
		"get_nodes":    `{"node_ids":["entity:kiln"]%s}`,
		"expand_graph": `{"node_ids":["entity:kiln"],"max_hops":1%s}`,
	}
	for name, body := range calls {
		for _, include := range []bool{false, true} {
			extra := ""
			if include {
				extra = `,"include_vectors":true`
			}
			out, err := tools.Call(ctx, name, json.RawMessage(strings.Replace(body, "%s", extra, 1)))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			raw, _ := json.Marshal(out)
			if !strings.Contains(string(raw), `"entity:kiln"`) {
				t.Fatalf("%s answered without the node: %s", name, raw)
			}
			if has := strings.Contains(string(raw), `"vector"`); has != include {
				t.Errorf("%s include_vectors=%v: vector in answer = %v: %s", name, include, has, raw)
			}
		}
	}
	nodes, err := db.graph.GetNodesBatch(ctx, []string{"entity:kiln"})
	if err != nil || len(nodes) != 1 || len(nodes[0].Vector) != 3 {
		t.Fatalf("stored node lost its vector: %+v %v", nodes, err)
	}
}
