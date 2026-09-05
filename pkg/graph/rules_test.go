package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
)

// The rule engine, proved over both databases.
//
// Everything here runs through backends(t), so a change that reaches for
// SQLite-only SQL fails against PostgreSQL rather than being discovered by
// whoever runs a shared brain on one.

type ruleNode struct {
	id       string
	nodeType string
	name     string
}

type ruleEdge struct {
	id        string
	from      string
	to        string
	edgeType  string
	propsJSON map[string]any
}

func seedRuleGraph(t *testing.T, ctx context.Context, g *GraphStore, nodes []ruleNode, edges []ruleEdge) {
	t.Helper()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, n := range nodes {
		node := &GraphNode{
			ID:       n.id,
			Vector:   []float32{1, 0, 0, 0},
			Content:  n.name,
			NodeType: n.nodeType,
			Properties: map[string]any{
				"name": n.name,
				"type": n.nodeType,
			},
		}
		if err := g.UpsertNode(ctx, node); err != nil {
			t.Fatalf("upsert node %s: %v", n.id, err)
		}
	}
	for _, e := range edges {
		edge := &GraphEdge{
			ID:         e.id,
			FromNodeID: e.from,
			ToNodeID:   e.to,
			EdgeType:   e.edgeType,
			Weight:     1,
			Properties: e.propsJSON,
		}
		if err := g.UpsertEdge(ctx, edge); err != nil {
			t.Fatalf("upsert edge %s: %v", e.id, err)
		}
	}
}

// socratesGraph is the classic, expressed in binary atoms because a graph edge
// is binary: Socrates is an instance of Human, Human is a subclass of Mortal.
func socratesGraph(t *testing.T, ctx context.Context, g *GraphStore) {
	t.Helper()
	seedRuleGraph(t, ctx, g,
		[]ruleNode{
			{id: "entity:socrates", nodeType: "person", name: "Socrates"},
			{id: "entity:plato", nodeType: "person", name: "Plato"},
			{id: "class:human", nodeType: "class", name: "Human"},
			{id: "class:mortal", nodeType: "class", name: "Mortal"},
			{id: "class:stone", nodeType: "class", name: "Stone"},
			{id: "entity:rock", nodeType: "thing", name: "Rock"},
		},
		[]ruleEdge{
			{id: "e1", from: "entity:socrates", to: "class:human", edgeType: "instance_of"},
			{id: "e2", from: "class:human", to: "class:mortal", edgeType: "subclass_of"},
			{id: "e3", from: "entity:rock", to: "class:stone", edgeType: "instance_of"},
		},
	)
}

func mustParseRule(t *testing.T, id, text string) Rule {
	t.Helper()
	rule, err := ParseRule(id, text)
	if err != nil {
		t.Fatalf("parse rule %s: %v", id, err)
	}
	return rule
}

func edgeTypesFrom(t *testing.T, ctx context.Context, g *GraphStore, nodeID string) []string {
	t.Helper()
	edges, err := g.GetEdges(ctx, nodeID, "out")
	if err != nil {
		t.Fatalf("get edges from %s: %v", nodeID, err)
	}
	out := make([]string, 0, len(edges))
	for _, edge := range edges {
		out = append(out, fmt.Sprintf("%s->%s", edge.EdgeType, edge.ToNodeID))
	}
	sort.Strings(out)
	return out
}

func TestRulesDeriveTheSocratesConclusionAndNothingElse(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			socratesGraph(t, ctx, b.store)

			rule := mustParseRule(t, "mortality",
				"IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)")

			result, err := b.store.ApplyRules(ctx, []Rule{rule}, RuleOptions{})
			if err != nil {
				t.Fatalf("apply rules: %v", err)
			}
			if len(result.CreatedEdgeIDs) != 1 {
				t.Fatalf("derived %d edges, want exactly 1: %v", len(result.CreatedEdgeIDs), result.CreatedEdgeIDs)
			}
			wantID := DerivedEdgeID("mortality", "entity:socrates", "class:mortal", "instance_of")
			if result.CreatedEdgeIDs[0] != wantID {
				t.Fatalf("derived %s, want %s", result.CreatedEdgeIDs[0], wantID)
			}

			// Socrates is mortal; the rock, which is an instance of a class
			// that is a subclass of nothing, gained nothing.
			got := edgeTypesFrom(t, ctx, b.store, "entity:socrates")
			want := []string{"instance_of->class:human", "instance_of->class:mortal"}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("socrates edges %v, want %v", got, want)
			}
			if got := edgeTypesFrom(t, ctx, b.store, "entity:rock"); len(got) != 1 {
				t.Errorf("rock edges %v, want only the explicit one", got)
			}
		})
	}
}

func TestRulesMatchLiteralTermsByIDAndByTypeName(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			socratesGraph(t, ctx, b.store)

			// class:human is an exact node id; "class:Mortal" resolves the
			// other way, as Type:Name against node_type and the name property
			// — and case-insensitively, which is the point of writing it that
			// way rather than as an id.
			byID := mustParseRule(t, "by_id",
				"IF instance_of(?x, class:human) THEN reachable_by_id(?x, class:human)")
			byTypeName := mustParseRule(t, "by_type_name",
				"IF subclass_of(?c, Class:Mortal) THEN reachable_by_name(?c, Class:Mortal)")

			result, err := b.store.ApplyRules(ctx, []Rule{byID, byTypeName}, RuleOptions{})
			if err != nil {
				t.Fatalf("apply rules: %v", err)
			}
			if len(result.CreatedEdgeIDs) != 2 {
				t.Fatalf("derived %v, want one edge per rule", result.CreatedEdgeIDs)
			}
			if len(result.UnresolvedTerms) != 0 {
				t.Fatalf("unresolved terms %v", result.UnresolvedTerms)
			}
			for _, edge := range result.Edges {
				switch edge.EdgeType {
				case "reachable_by_id":
					if edge.FromNodeID != "entity:socrates" || edge.ToNodeID != "class:human" {
						t.Errorf("by id: %s -> %s", edge.FromNodeID, edge.ToNodeID)
					}
				case "reachable_by_name":
					if edge.FromNodeID != "class:human" || edge.ToNodeID != "class:mortal" {
						t.Errorf("by type name: %s -> %s", edge.FromNodeID, edge.ToNodeID)
					}
				default:
					t.Errorf("unexpected derived type %s", edge.EdgeType)
				}
			}
		})
	}
}

func TestRulesReportALiteralThatMatchesNoNode(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			socratesGraph(t, ctx, b.store)

			rule := mustParseRule(t, "nobody",
				"IF instance_of(?x, class:unicorn) THEN mythical(?x, class:unicorn)")
			result, err := b.store.ApplyRules(ctx, []Rule{rule}, RuleOptions{})
			if err != nil {
				t.Fatalf("apply rules: %v", err)
			}
			if len(result.CreatedEdgeIDs) != 0 {
				t.Fatalf("derived %v from a term that matches nothing", result.CreatedEdgeIDs)
			}
			if len(result.UnresolvedTerms) != 1 || result.UnresolvedTerms[0] != "class:unicorn" {
				t.Fatalf("unresolved terms %v, want [class:unicorn] — a rule that can never fire has to say so", result.UnresolvedTerms)
			}
		})
	}
}

// cycleGraph is a 3-cycle, whose transitive closure is every ordered pair
// including the self-loops. A rule set that did not terminate would hang here.
func cycleGraph(t *testing.T, ctx context.Context, g *GraphStore) {
	t.Helper()
	seedRuleGraph(t, ctx, g,
		[]ruleNode{
			{id: "n:a", nodeType: "node", name: "A"},
			{id: "n:b", nodeType: "node", name: "B"},
			{id: "n:c", nodeType: "node", name: "C"},
		},
		[]ruleEdge{
			{id: "c1", from: "n:a", to: "n:b", edgeType: "reaches"},
			{id: "c2", from: "n:b", to: "n:c", edgeType: "reaches"},
			{id: "c3", from: "n:c", to: "n:a", edgeType: "reaches"},
		},
	)
}

func TestTransitiveClosureTerminatesOnACycle(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			cycleGraph(t, ctx, b.store)

			rule := mustParseRule(t, "reach",
				"IF reaches(?x, ?y) AND reaches(?y, ?z) THEN reaches(?x, ?z)")
			result, err := b.store.ApplyRules(ctx, []Rule{rule}, RuleOptions{})
			if err != nil {
				t.Fatalf("apply rules: %v", err)
			}
			// The closure of a 3-cycle is all nine ordered pairs; three of them
			// are the explicit edges, so six are derived.
			if len(result.CreatedEdgeIDs) != 6 {
				t.Fatalf("derived %d edges, want 6: %v", len(result.CreatedEdgeIDs), result.CreatedEdgeIDs)
			}
			if result.CapHit {
				t.Fatalf("reported a cap on a graph that reaches a fixpoint: %s", result.CapReason)
			}
			for _, node := range []string{"n:a", "n:b", "n:c"} {
				if got := edgeTypesFrom(t, ctx, b.store, node); len(got) != 3 {
					t.Errorf("%s has %v, want an edge to all three nodes", node, got)
				}
			}
		})
	}
}

func TestRuleCapsAreReportedAndWriteNothing(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			cycleGraph(t, ctx, b.store)
			rule := mustParseRule(t, "reach",
				"IF reaches(?x, ?y) AND reaches(?y, ?z) THEN reaches(?x, ?z)")

			for _, tc := range []struct {
				name string
				opts RuleOptions
				says string
			}{
				{name: "derived", opts: RuleOptions{MaxDerived: 2}, says: "more than 2 edges"},
				{name: "iterations", opts: RuleOptions{MaxIterations: 1}, says: "after 1 iterations"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					result, err := b.store.ApplyRules(ctx, []Rule{rule}, tc.opts)
					if !errors.Is(err, ErrRuleCapExceeded) {
						t.Fatalf("error is %v, want ErrRuleCapExceeded", err)
					}
					if result == nil || !result.CapHit {
						t.Fatalf("result does not report the cap: %+v", result)
					}
					if got := result.CapReason; got == "" {
						t.Fatal("cap reason is empty")
					}
					// A graph that quietly stopped early is the failure mode
					// this is guarding: nothing may be written.
					edges, err := b.store.GetEdges(ctx, "n:a", "out")
					if err != nil {
						t.Fatalf("get edges: %v", err)
					}
					if len(edges) != 1 {
						t.Fatalf("a capped run wrote %d edges from n:a; it must write none", len(edges)-1)
					}
				})
			}
		})
	}
}

func TestDerivedEdgesCarryRuleProvenanceAndConfidence(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			socratesGraph(t, ctx, b.store)

			rule := mustParseRule(t, "mortality",
				"IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)")
			rule.Confidence = 0.5
			rule.Metadata = map[string]string{"note": "kept", "rule_id": "ignored"}

			result, err := b.store.ApplyRules(ctx, []Rule{rule}, RuleOptions{})
			if err != nil {
				t.Fatalf("apply rules: %v", err)
			}
			if len(result.Edges) != 1 {
				t.Fatalf("derived %d edges, want 1", len(result.Edges))
			}
			edge := result.Edges[0]
			if inferred, _ := edge.Properties["inferred"].(bool); !inferred {
				t.Errorf("inferred flag: %+v", edge.Properties)
			}
			if got, _ := edge.Properties["provenance"].(string); got != RuleProvenance {
				t.Errorf("provenance %q", got)
			}
			if got, _ := edge.Properties["rule_id"].(string); got != "mortality" {
				t.Errorf("rule_id %q — rule metadata must not be able to overwrite it", got)
			}
			if got, _ := edge.Properties["rule_text"].(string); got != rule.Text() {
				t.Errorf("rule_text %q, want %q", got, rule.Text())
			}
			if got, _ := edge.Properties["note"].(string); got != "kept" {
				t.Errorf("non-reserved metadata was dropped: %+v", edge.Properties)
			}
			if got, _ := edge.Properties["confidence"].(float64); got != 0.5 {
				t.Errorf("confidence %v, want 0.5 (min premise 1.0 times rule 0.5)", got)
			}
			supports, _ := edge.Properties["support_edge_ids"].([]string)
			if fmt.Sprint(supports) != "[e1 e2]" {
				t.Errorf("support edge ids %v, want the exact premises [e1 e2]", supports)
			}
		})
	}
}

func TestExplainShowsTheRuleAndThePremiseChain(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			cycleGraph(t, ctx, b.store)
			rule := mustParseRule(t, "reach",
				"IF reaches(?x, ?y) AND reaches(?y, ?z) THEN reaches(?x, ?z)")
			if _, err := b.store.ApplyRules(ctx, []Rule{rule}, RuleOptions{}); err != nil {
				t.Fatalf("apply rules: %v", err)
			}

			// a reaches c only through b, so the explanation is one rule
			// application over two explicit edges.
			id := DerivedEdgeID("reach", "n:a", "n:c", "reaches")
			explanation, err := b.store.ExplainEdge(ctx, id)
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			if !explanation.Inferred || explanation.RuleID != "reach" {
				t.Fatalf("explanation does not name the rule: %+v", explanation)
			}
			if explanation.RuleText != rule.Text() {
				t.Errorf("rule text %q, want %q", explanation.RuleText, rule.Text())
			}
			if fmt.Sprint(explanation.SupportEdgeIDs) != "[c1 c2]" {
				t.Fatalf("supports %v, want [c1 c2]", explanation.SupportEdgeIDs)
			}

			trace, err := b.store.ExplainEdgeTrace(ctx, id, 0)
			if err != nil {
				t.Fatalf("trace: %v", err)
			}
			if len(trace) != 3 {
				t.Fatalf("trace has %d entries, want the conclusion and its two premises", len(trace))
			}
			for _, entry := range trace[1:] {
				if entry.Depth != 1 || entry.ParentEdgeID != id {
					t.Errorf("premise entry %+v is not attached to the conclusion", entry)
				}
				if entry.Explanation.Inferred {
					t.Errorf("premise %s should be explicit", entry.EdgeID)
				}
			}

			// One that stands on a derived edge: the chain has to go deeper
			// than one level or the explanation stops being a derivation.
			deep, err := b.store.ExplainEdgeTrace(ctx, DerivedEdgeID("reach", "n:a", "n:a", "reaches"), 0)
			if err != nil {
				t.Fatalf("explain self loop: %v", err)
			}
			nested := false
			for _, entry := range deep {
				if entry.Depth >= 2 {
					nested = true
				}
			}
			if !nested {
				t.Errorf("a conclusion resting on a derived premise did not explain it: %+v", deep)
			}

			// And a depth of one stops at the conclusion, saying so rather than
			// letting it read as an edge with nothing under it.
			shallow, err := b.store.ExplainEdgeTrace(ctx, id, 1)
			if err != nil {
				t.Fatalf("shallow trace: %v", err)
			}
			if len(shallow) != 1 || !shallow[0].Truncated {
				t.Errorf("a trace cut short did not mark itself truncated: %+v", shallow)
			}
		})
	}
}

func TestReapplyingTheSameRulesDerivesNothingNew(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			socratesGraph(t, ctx, b.store)
			rule := mustParseRule(t, "mortality",
				"IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)")

			first, err := b.store.ApplyRules(ctx, []Rule{rule}, RuleOptions{})
			if err != nil {
				t.Fatalf("first run: %v", err)
			}
			if len(first.CreatedEdgeIDs) != 1 {
				t.Fatalf("first run derived %v", first.CreatedEdgeIDs)
			}

			second, err := b.store.ApplyRules(ctx, []Rule{rule}, RuleOptions{})
			if err != nil {
				t.Fatalf("second run: %v", err)
			}
			if len(second.CreatedEdgeIDs) != 0 {
				t.Fatalf("second run derived %v, want nothing", second.CreatedEdgeIDs)
			}
			if fmt.Sprint(second.UnchangedEdgeIDs) != fmt.Sprint(first.CreatedEdgeIDs) {
				t.Errorf("second run reported unchanged %v, want %v", second.UnchangedEdgeIDs, first.CreatedEdgeIDs)
			}
		})
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			socratesGraph(t, ctx, b.store)
			rule := mustParseRule(t, "mortality",
				"IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)")

			result, err := b.store.ApplyRules(ctx, []Rule{rule}, RuleOptions{DryRun: true})
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if len(result.CreatedEdgeIDs) != 1 || !result.DryRun {
				t.Fatalf("dry run should report the edge it would write: %+v", result)
			}
			if got := edgeTypesFrom(t, ctx, b.store, "entity:socrates"); len(got) != 1 {
				t.Fatalf("dry run wrote to the graph: %v", got)
			}
		})
	}
}

func TestRuleStoreRoundTrip(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			rule := mustParseRule(t, "mortality",
				"IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)")
			rule.Name = "Mortality"
			rule.Confidence = 0.9
			rule.Note = "the classic"

			stored, err := b.store.SaveRule(ctx, rule, true)
			if err != nil {
				t.Fatalf("save rule: %v", err)
			}
			if stored.Text != rule.Text() || !stored.Enabled {
				t.Fatalf("stored rule: %+v", stored)
			}
			if len(stored.When) != 2 || stored.Then.Predicate != "instance_of" {
				t.Fatalf("structure did not survive the round trip: %+v", stored.Rule)
			}

			// Saving again is an update, not a second row.
			rule.Note = "edited"
			if _, err := b.store.SaveRule(ctx, rule, false); err != nil {
				t.Fatalf("resave: %v", err)
			}
			all, err := b.store.ListRules(ctx, false)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(all) != 1 || all[0].Note != "edited" || all[0].Enabled {
				t.Fatalf("list after update: %+v", all)
			}
			enabled, err := b.store.ListRules(ctx, true)
			if err != nil {
				t.Fatalf("list enabled: %v", err)
			}
			if len(enabled) != 0 {
				t.Fatalf("a disabled rule was listed as enabled: %+v", enabled)
			}

			deleted, err := b.store.DeleteRule(ctx, "mortality")
			if err != nil || !deleted {
				t.Fatalf("delete: %v %v", deleted, err)
			}
			again, err := b.store.DeleteRule(ctx, "mortality")
			if err != nil {
				t.Fatalf("delete twice: %v", err)
			}
			if again {
				t.Error("deleting a rule that is not there reported a deletion")
			}
			missing, err := b.store.GetRule(ctx, "mortality")
			if err != nil {
				t.Fatalf("get after delete: %v", err)
			}
			if missing != nil {
				t.Errorf("rule survived deletion: %+v", missing)
			}
		})
	}
}
