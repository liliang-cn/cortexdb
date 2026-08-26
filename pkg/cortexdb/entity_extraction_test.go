package cortexdb

import (
	"strings"
	"testing"
)

func namesOf(entities []GraphEntity) []string {
	out := make([]string, 0, len(entities))
	for _, e := range entities {
		out = append(out, e.Name)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}

// Every one of these came back as an entity from a real store. English
// capitalises the first word of a sentence, so a Title Case pattern collects the
// grammar with the names — and co-occurrence then pairs each with everything
// near it, so one bad entity becomes a row of bad edges.
func TestExtractCorpusEntitiesDropsSentenceGrammar(t *testing.T) {
	// Long enough for the corroboration rule to engage, which is the shape the
	// junk came from: memories are written as multi-paragraph notes.
	text := strings.Join([]string{
		"Options added 2026-07-29 for the setup script.",
		"This is the CortexDB gateway that fronts the store.",
		"Only the CortexDB brain answers on that port.",
		"Requires resource-agents-base on Ubuntu, and Ubuntu ships it.",
		"Measured failover on a four node cluster with DRBD.",
		"Apparent numbers are the client timing out against DRBD.",
		"Next steps are unclear.",
		"The CortexDB store keeps the graph, and Heathrow is unrelated.",
		"We deployed Heathrow behind the Gateway service.",
		"Library code lives beside the Gateway.",
	}, "\n")

	got := namesOf(extractCorpusEntities(text))
	for _, junk := range []string{"Options", "This", "Only", "Requires", "Measured", "Apparent", "Next", "Library"} {
		if contains(got, junk) {
			t.Errorf("kept grammar or once-at-line-start word %q as an entity: %v", junk, got)
		}
	}
	for _, want := range []string{"CortexDB", "Ubuntu", "DRBD", "Gateway", "Heathrow"} {
		if !contains(got, want) {
			t.Errorf("dropped real entity %q: %v", want, got)
		}
	}
}

// "Printf" was an entity in a real store; it came from log.Printf in a memory
// that was discussing the call, not naming a thing.
func TestExtractEntitiesSkipsMemberAccess(t *testing.T) {
	text := "The logger calls log.Printf and req.Content is read by the Gateway service."
	got := namesOf(extractTitleEntities(text))
	for _, junk := range []string{"Printf", "Content"} {
		if contains(got, junk) {
			t.Errorf("kept dotted identifier tail %q: %v", junk, got)
		}
	}
	if !contains(got, "Gateway") {
		t.Errorf("dropped a real mid-sentence entity: %v", got)
	}
}

// A determiner in front of a name is not part of the name, but an adjective can
// be — dropping "New" from "New York" would invent a different place.
func TestExtractEntitiesStripsOnlyLeadingGrammar(t *testing.T) {
	got := namesOf(extractTitleEntities("we deployed to New York and This Gateway went live"))
	if !contains(got, "New York") {
		t.Errorf("mangled a name that starts with an adjective: %v", got)
	}
	if contains(got, "This Gateway") {
		t.Errorf("kept a determiner as part of the name: %v", got)
	}
	if !contains(got, "Gateway") {
		t.Errorf("lost the name after stripping the determiner: %v", got)
	}
}

// Queries are one line, so nearly every word in them opens a sentence. The
// corroboration rule must not apply there or entity hints would vanish — several
// retrieval paths decide whether to use the graph from exactly this call.
func TestExtractTitleEntitiesKeepsQueryEntities(t *testing.T) {
	got := namesOf(extractTitleEntities("CortexDB memory recall"))
	if !contains(got, "CortexDB") {
		t.Fatalf("a query's only entity was dropped: %v", got)
	}
}

// The plausibility filter exists for romanisation debris in bilingual text and
// demands three letters and a vowel — which "Go", "AWS" and "CI" fail. It stays
// off for text without CJK.
func TestPlausibilityFilterStaysOffForLatinText(t *testing.T) {
	got := namesOf(extractTitleEntities("the Api gateway is written in Go and built by Ci"))
	if !contains(got, "Go") {
		t.Errorf("short Latin entity dropped from non-CJK text: %v", got)
	}
}

// The old pattern required a lowercase run after the first capital, so a word
// with a capital in the middle had no word boundary to end on and matched
// nothing at all. Most of a technical corpus's vocabulary looks like this.
func TestExtractEntitiesSeesCamelCaseAndAcronyms(t *testing.T) {
	text := "The CortexDB store keeps SQLite and FTS5 behind GraphRAG, and DRBD replicates it over MCP."
	got := namesOf(extractTitleEntities(text))
	for _, want := range []string{"CortexDB", "SQLite", "FTS5", "GraphRAG", "DRBD", "MCP"} {
		if !contains(got, want) {
			t.Errorf("did not see %q: %v", want, got)
		}
	}
}

// Bilingual memories are the normal case in this store, and the plausibility
// filter's "three letters and a vowel" would drop every acronym in them.
func TestAcronymsSurviveTheCJKPlausibilityFilter(t *testing.T) {
	got := namesOf(extractTitleEntities("集群用 DRBD 复制，走 MCP 协议，索引是 FTS5"))
	for _, want := range []string{"DRBD", "MCP", "FTS5"} {
		if !contains(got, want) {
			t.Errorf("acronym %q dropped from CJK text: %v", want, got)
		}
	}
}
