package memoryflow

import "testing"

func TestResolveTaxonomyWithDefaultConventions(t *testing.T) {
	conventions := DefaultConventionSet("apollo")

	resolved := ResolveTaxonomy(Taxonomy{}, SourceHint{
		Source: "slack",
		Path:   "docs/spec/plan.md",
		Title:  "Launch plan",
		Tags:   []string{"team"},
	}, conventions)

	if resolved.Wing == "" {
		t.Fatalf("expected resolved wing, got %+v", resolved)
	}
	if resolved.Source != "slack" {
		t.Fatalf("expected source to be preserved, got %+v", resolved)
	}
	if len(resolved.Tags) == 0 {
		t.Fatalf("expected convention tags, got %+v", resolved)
	}
}

func TestResolveTaxonomyPreservesExplicitValues(t *testing.T) {
	conventions := DefaultConventionSet("apollo")

	resolved := ResolveTaxonomy(Taxonomy{
		Wing: "custom",
		Room: "ops",
		Kind: "diary",
		Tags: []string{"manual"},
	}, SourceHint{
		Source: "chatgpt",
		Path:   "src/main.go",
	}, conventions)

	if resolved.Wing != "custom" || resolved.Room != "ops" || resolved.Kind != "diary" {
		t.Fatalf("expected explicit taxonomy to win, got %+v", resolved)
	}
	if len(resolved.Tags) == 0 {
		t.Fatalf("expected explicit tags to be preserved, got %+v", resolved)
	}
}
