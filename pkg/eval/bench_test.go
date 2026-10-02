package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "bench", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestLongMemEvalGivesEveryQuestionItsOwnHaystack(t *testing.T) {
	s, err := LoadLongMemEval(openFixture(t, "longmemeval.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Skipped["abstention"]; got != 1 {
		t.Errorf("abstention questions skipped = %d, want 1", got)
	}
	if len(s.Corpora) != 2 {
		t.Fatalf("corpora = %d, want 2 (one per scored question)", len(s.Corpora))
	}

	degree := s.Corpora[0]
	if degree.ID != "q-degree" || len(degree.Queries) != 1 {
		t.Fatalf("first corpus = %s with %d queries, want q-degree with 1", degree.ID, len(degree.Queries))
	}
	q := degree.Queries[0]
	if q.Text != "What degree did I graduate with?" || q.Category != "single-session-user" {
		t.Errorf("query = %+v", q)
	}
	if !reflect.DeepEqual(q.Relevant, []string{"answer_s1"}) {
		t.Errorf("relevant = %v, want [answer_s1]", q.Relevant)
	}
	if len(degree.Documents) != 3 {
		t.Fatalf("documents = %d, want the 3 haystack sessions", len(degree.Documents))
	}
	answer := degree.Documents[1]
	if answer.ID != "answer_s1" || answer.Title != "Conversation on 2023/05/21 (Sun) 10:00" {
		t.Errorf("answer session = %q titled %q", answer.ID, answer.Title)
	}
	if !strings.Contains(answer.Content, "user: I graduated with a degree in Business Administration") ||
		!strings.Contains(answer.Content, "assistant: Marketing analyst roles") {
		t.Errorf("session content lost a turn or its role: %q", answer.Content)
	}

	trips := s.Corpora[1]
	if len(trips.Documents) != 3 {
		t.Errorf("documents = %d, want 3: a session id repeated in the haystack is indexed once", len(trips.Documents))
	}
	if strings.Contains(trips.Documents[2].Content, "assistant:") {
		t.Errorf("an empty turn was indexed: %q", trips.Documents[2].Content)
	}
	if !reflect.DeepEqual(trips.Queries[0].Relevant, []string{"answer_t1", "answer_t2"}) {
		t.Errorf("relevant = %v", trips.Queries[0].Relevant)
	}
}

func TestLongMemEvalRefusesAQuestionWhoseAnswerIsNotInItsHaystack(t *testing.T) {
	in := `[{"question_id":"q","question":"?","answer_session_ids":["gone"],
		"haystack_dates":["d"],"haystack_session_ids":["s"],"haystack_sessions":[[{"role":"user","content":"hi"}]]}]`
	if _, err := LoadLongMemEval(strings.NewReader(in)); err == nil {
		t.Fatal("loaded a question that can never be answered; want an error")
	}
}

func TestLoCoMoIndexesTurnsAndScoresTheirEvidence(t *testing.T) {
	s, err := LoadLoCoMo(openFixture(t, "locomo.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"adversarial (category 5)": 1, "no evidence turn in conversation": 1}
	if !reflect.DeepEqual(s.Skipped, want) {
		t.Errorf("skipped = %v, want %v", s.Skipped, want)
	}
	if len(s.Corpora) != 1 {
		t.Fatalf("corpora = %d, want 1", len(s.Corpora))
	}
	c := s.Corpora[0]

	var ids []string
	for _, d := range c.Documents {
		ids = append(ids, d.ID)
	}
	// Sessions in numeric order (session_10 after session_2), and the
	// zero-padded D2:01 dropped as a repeat of D2:1.
	if want := []string{"D1:1", "D1:2", "D2:1", "D2:2", "D10:1"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("turn ids = %v, want %v", ids, want)
	}
	painted := c.Documents[1]
	if painted.Group != "session_1" {
		t.Errorf("group = %q, want session_1", painted.Group)
	}
	if want := "[1:56 pm on 8 May, 2023] Melanie: That sounds meaningful. I painted a sunrise last year. (shared an image: a painting of a sunrise over a lake)"; painted.Content != want {
		t.Errorf("turn content = %q, want %q", painted.Content, want)
	}

	if len(c.Queries) != 4 {
		t.Fatalf("queries = %d, want 4", len(c.Queries))
	}
	study := c.Queries[2]
	if !reflect.DeepEqual(study.Relevant, []string{"D2:1", "D2:2"}) {
		t.Errorf("relevant = %v, want [D2:1 D2:2]: both references in one string, zero padding read as the same turn, repeats once", study.Relevant)
	}
	if study.Category != "category-3" || study.ID != "conv-1/q2" {
		t.Errorf("query id/category = %s/%s", study.ID, study.Category)
	}
	if got := LoCoMoSessionOf("D10:1"); got != "session_10" {
		t.Errorf("LoCoMoSessionOf(D10:1) = %q", got)
	}
}

func TestMuSiQueResolvesSupportingParagraphsToCorpusPassages(t *testing.T) {
	s, err := LoadMuSiQue(openFixture(t, "musique.json"), openFixture(t, "musique_corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := s.Corpora[0]
	if len(c.Documents) != 6 {
		t.Errorf("documents = %d, want 6: a passage listed twice in the corpus is indexed once", len(c.Documents))
	}
	if c.Documents[0].Content != "Lionel Messi\nMessi joined the youth academy of FC Barcelona." {
		t.Errorf("passage content = %q, want title, newline, text", c.Documents[0].Content)
	}
	want := [][]string{
		{passageID("Lionel Messi", "Messi joined the youth academy of FC Barcelona."), passageID("FC Barcelona", "FC Barcelona was founded by Joan Gamper in 1899.")},
		{passageID("James Joyce", "James Joyce, author of Ulysses, was born in Dublin."), passageID("Dublin", "The River Liffey flows through Dublin."), passageID("River Liffey", "The Liffey is a river in Ireland.")},
	}
	for i, q := range c.Queries {
		if !reflect.DeepEqual(q.Relevant, want[i]) {
			t.Errorf("%s relevant = %v, want %v", q.ID, q.Relevant, want[i])
		}
	}
	if c.Queries[0].Category != "2hop" || c.Queries[1].Category != "3hop" {
		t.Errorf("categories = %q, %q; want the hop count", c.Queries[0].Category, c.Queries[1].Category)
	}
}

func TestMuSiQueRefusesAGoldPassageMissingFromTheCorpus(t *testing.T) {
	qs := `[{"id":"2hop__1","question":"?","paragraphs":[{"title":"T","paragraph_text":"absent","is_supporting":true}]}]`
	if _, err := LoadMuSiQue(strings.NewReader(qs), strings.NewReader(`[{"title":"T","text":"present"}]`)); err == nil {
		t.Fatal("scored a question whose evidence cannot be retrieved; want an error")
	}
}

func Test2WikiMultiHopQAMatchesSupportingTitlesToJoinedSentences(t *testing.T) {
	s, err := Load2WikiMultiHopQA(openFixture(t, "2wikimultihopqa.json"), openFixture(t, "2wikimultihopqa_corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := s.Corpora[0]
	lothair := passageID("Lothair II", "Lothair II was a king. His mother was Ermengarde of Tours.")
	ermengarde := passageID("Ermengarde of Tours", "Ermengarde of Tours died on 20 March 851.")
	teutberga := passageID("Teutberga", "Teutberga was a queen of Lotharingia. She married Lothair II.")
	if got := c.Queries[0].Relevant; !reflect.DeepEqual(got, []string{lothair, ermengarde}) {
		t.Errorf("compositional relevant = %v, want Lothair II then Ermengarde, each once", got)
	}
	if got := c.Queries[1].Relevant; !reflect.DeepEqual(got, []string{teutberga}) {
		t.Errorf("comparison relevant = %v, want Teutberga", got)
	}
	if c.Queries[0].Category != "compositional" {
		t.Errorf("category = %q", c.Queries[0].Category)
	}
}

// The harness's numbers on a fixture are worked out by hand here, so a change
// to any metric, to how per-question corpora are pooled, or to how dialog
// rankings roll up to sessions shows up as a wrong number rather than a
// plausible one.
func TestHarnessComputesRecallAndNDCGOnALoadedFixture(t *testing.T) {
	s, err := LoadLoCoMo(openFixture(t, "locomo.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := s.Corpora[0]
	// A canned ranking per question, as a retriever would return it.
	rankings := map[string][]string{
		"conv-1/q0": {"D1:1", "D2:1"},          // hit at rank 1
		"conv-1/q1": {"D2:2", "D1:1", "D1:2"},  // hit at rank 3
		"conv-1/q2": {"D2:2", "D1:1", "D10:1"}, // one of two at rank 1
		"conv-1/q3": {"D1:1", "D2:1"},          // miss
	}
	doc := NewAccumulator("locomo", 1, 2, 3)
	sess := NewAccumulator("locomo/session", 1, 2, 3)
	groupOf := map[string]string{}
	for _, d := range c.Documents {
		groupOf[d.ID] = d.Group
	}
	group := func(id string) string { return groupOf[id] }
	for _, q := range c.Queries {
		got := rankings[q.ID]
		doc.Add(q.ID, got, q.Relevant, time.Duration(len(got))*time.Millisecond)
		sess.Add(q.ID, CollapseIDs(got, group), CollapseIDs(q.Relevant, group), -1)
	}
	d := doc.Report()
	ss := sess.Report()
	inv := func(rank int) float64 { return 1 / math.Log2(float64(rank+1)) }
	type check struct {
		name      string
		got, want float64
	}
	checks := []check{
		{"recall@1", d.RecallAtK[1], (1 + 0 + 0.5 + 0) / 4},
		{"recall@3", d.RecallAtK[3], (1 + 1 + 0.5 + 0) / 4},
		{"hit@3", d.HitAtK[3], 3.0 / 4},
		{"all@3", d.AllAtK[3], 2.0 / 4},
		{"ndcg@3", d.NDCGAtK[3], (1 + inv(3) + 1/(1+inv(2)) + 0) / 4},
		{"mrr", d.MRR, (1 + 1.0/3 + 1 + 0) / 4},
		// Sessions: q0 wants session_1 and gets [1,2]; q1 wants 1 and gets
		// [2,1]; q2 wants 2 and gets [2,1,10]; q3 wants 10 and gets [1,2].
		{"session recall@1", ss.RecallAtK[1], (1 + 0 + 1 + 0) / 4.0},
		{"session recall@2", ss.RecallAtK[2], (1 + 1 + 1 + 0) / 4.0},
		{"session ndcg@2", ss.NDCGAtK[2], (1 + inv(2) + 1 + 0) / 4},
	}
	for _, ch := range checks {
		if !approx(ch.got, ch.want) {
			t.Errorf("%s = %.6f, want %.6f", ch.name, ch.got, ch.want)
		}
	}
	if d.Latency == nil || d.Latency.P50 != 2 || d.Latency.Max != 3 {
		t.Errorf("latency = %+v, want p50 2ms and max 3ms", d.Latency)
	}
	if ss.Latency != nil {
		t.Errorf("session latency = %+v, want none: it was not measured", ss.Latency)
	}
}

func TestRunMeasuresEveryQueryThroughTheRetriever(t *testing.T) {
	ds := &Dataset{Name: "tiny", Queries: []Query{
		{ID: "a", Text: "alpha", Relevant: []string{"d1"}},
		{ID: "b", Text: "beta", Relevant: []string{"d2", "d3"}},
	}}
	r := RetrieverFunc(func(_ context.Context, q string, k int) ([]string, error) {
		if k != 5 {
			t.Errorf("asked for %d results, want the deepest cutoff 5", k)
		}
		if q == "alpha" {
			return []string{"d1"}, nil
		}
		return []string{"x", "d3", "d3", "d2"}, nil
	})
	rep, err := Run(context.Background(), ds, r, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !approx(rep.RecallAtK[5], 1) {
		t.Errorf("recall@5 = %v, want 1: a repeated id is one hit, not two", rep.RecallAtK[5])
	}
	if rep.Latency == nil || len(rep.PerQuery) != 2 {
		t.Errorf("latency %v, per-query %d; want both recorded", rep.Latency, len(rep.PerQuery))
	}
}

func TestSampleIsSeededStableAndNested(t *testing.T) {
	build := func() *Suite {
		s := &Suite{}
		for c := 0; c < 5; c++ {
			corpus := Corpus{ID: string(rune('A' + c))}
			for q := 0; q < 20; q++ {
				corpus.Queries = append(corpus.Queries, Query{ID: string(rune('a'+q%26)) + string(rune('0'+q/10))})
			}
			s.Corpora = append(s.Corpora, corpus)
		}
		return s
	}
	ids := func(s *Suite) map[string]bool {
		m := map[string]bool{}
		for _, c := range s.Corpora {
			for _, q := range c.Queries {
				m[c.ID+"/"+q.ID] = true
			}
		}
		return m
	}

	a, b := build(), build()
	a.Sample(30, 7)
	b.Sample(30, 7)
	if a.NumQueries() != 30 || !reflect.DeepEqual(ids(a), ids(b)) {
		t.Fatalf("same seed picked different questions (%d vs %d)", a.NumQueries(), b.NumQueries())
	}
	other := build()
	other.Sample(30, 8)
	if reflect.DeepEqual(ids(a), ids(other)) {
		t.Error("a different seed picked the same 30 of 100 questions")
	}
	bigger := build()
	bigger.Sample(60, 7)
	for id := range ids(a) {
		if !ids(bigger)[id] {
			t.Errorf("raising the limit dropped %s: a larger sample must contain the smaller one", id)
		}
	}
	all := build()
	all.Sample(0, 7)
	if all.NumQueries() != 100 {
		t.Errorf("limit 0 kept %d questions, want all 100", all.NumQueries())
	}
}

func TestSampleDropsCorporaLeftWithoutQuestions(t *testing.T) {
	s := &Suite{Corpora: []Corpus{{ID: "x", Queries: []Query{{ID: "1"}}}, {ID: "y", Queries: []Query{{ID: "2"}}}}}
	s.Sample(1, 1)
	if len(s.Corpora) != 1 || len(s.Corpora[0].Queries) != 1 {
		t.Errorf("kept %d corpora; a corpus with no question left must not be indexed", len(s.Corpora))
	}
}

func TestManifestPinsEveryFileToARevisionAndADigest(t *testing.T) {
	m, err := BenchManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"longmemeval", "locomo", "musique", "2wikimultihopqa"} {
		ds, ok := m[name]
		if !ok {
			t.Errorf("manifest lacks %s", name)
			continue
		}
		if ds.License == "" || ds.Source == "" {
			t.Errorf("%s: license and source must be recorded", name)
		}
		for _, f := range ds.Files {
			if len(f.SHA256) != 64 || f.Size <= 0 || !strings.HasPrefix(f.URL, "https://") {
				t.Errorf("%s: incomplete entry %+v", f.Path, f)
			}
			if strings.Contains(f.URL, "/main/") || strings.Contains(f.URL, "/master/") {
				t.Errorf("%s: URL follows a branch, so the file behind it can change: %s", f.Path, f.URL)
			}
		}
	}
}

func TestVerifyRejectsAFileThatDiffersFromTheManifest(t *testing.T) {
	dir := t.TempDir()
	content := []byte("[]")
	if err := os.WriteFile(filepath.Join(dir, "f.json"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	good := BenchFile{Path: "f.json", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))}
	if err := VerifyBenchFile(dir, good); err != nil {
		t.Errorf("matching file rejected: %v", err)
	}
	bad := good
	bad.SHA256 = strings.Repeat("0", 64)
	if err := VerifyBenchFile(dir, bad); err == nil {
		t.Error("a file with the wrong digest was accepted")
	}
	if err := VerifyBenchFile(dir, BenchFile{Path: "missing.json"}); err == nil {
		t.Error("a missing file was accepted")
	}
}
