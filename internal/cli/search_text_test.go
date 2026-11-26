package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestSearchPrintsHitsAsText(t *testing.T) {
	useLocalBrain(t)
	if code, _, errOut := runCLI(t, ProgCortexDB, "remember", "--id", "deploy-key", "the deploy key rotates weekly"); code != exitOK {
		t.Fatalf("remember: exit %d: %s", code, errOut)
	}
	code, out, errOut := runCLI(t, ProgCortexDB, "search", "deploy", "key")
	if code != exitOK {
		t.Fatalf("search: exit %d: %s", code, errOut)
	}
	if !strings.HasPrefix(out, "Memories\n") || !strings.Contains(out, "deploy-key") || !strings.Contains(out, "rotates weekly") {
		t.Fatalf("text search printed %q", out)
	}
	if json.Valid([]byte(out)) || strings.Contains(out, "memory_plan") {
		t.Fatalf("text search printed the raw result: %q", out)
	}

	code, out, _ = runCLI(t, ProgCortexDB, "search", "deploy", "key", "--format", "json")
	var res cortexdb.KnowledgeMemoryRecallResponse
	if code != exitOK || json.Unmarshal([]byte(out), &res) != nil || len(res.Memories) == 0 {
		t.Fatalf("json search: exit %d, %q", code, out)
	}
}

func TestPrintRecallKeepsToAScreen(t *testing.T) {
	res := cortexdb.KnowledgeMemoryRecallResponse{Degraded: []string{"embedder_unreachable"}}
	res.Memories = []cortexdb.MemorySearchHit{{Memory: cortexdb.MemoryRecord{ID: "m1", Content: strings.Repeat("很长 ", 200)}, Score: 0.5, Via: []string{"keyword"}}}
	for i := 0; i < searchFactLimit+5; i++ {
		res.GraphFacts = append(res.GraphFacts, cortexdb.KnowledgeMemoryGraphFact{Subject: fmt.Sprintf("s%d", i), Predicate: "p", Object: "o"})
	}
	raw, _ := json.Marshal(res)
	var b strings.Builder
	if ok, err := printRecall(&b, string(raw)); !ok || err != nil {
		t.Fatalf("printRecall: %v %v", ok, err)
	}
	out := b.String()
	for _, want := range []string{"0.500  m1  (keyword)", "… 5 more", "degraded: embedder_unreachable"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if n := len([]rune(line)); n > searchSnippetRunes+20 {
			t.Errorf("a %d-rune line: %q", n, line)
		}
	}

	b.Reset()
	if ok, _ := printRecall(&b, `{"query":"x"}`); !ok || b.String() != "no results\n" {
		t.Fatalf("empty result printed %q", b.String())
	}
	if ok, _ := printRecall(&b, "not json"); ok {
		t.Fatal("printRecall took text that is not a result")
	}
}
