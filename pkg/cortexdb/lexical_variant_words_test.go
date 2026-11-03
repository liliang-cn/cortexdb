package cortexdb

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// openVariantStore is a no-embedder SQLite store.
func openVariantStore(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "variants.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if db.HasEmbedder() {
		t.Fatal("a lexical test must run without an embedder")
	}
	return db
}

func variantKnowledgeIDs(t *testing.T, db *DB, req KnowledgeSearchRequest) []string {
	t.Helper()
	req.RetrievalMode = RetrievalModeLexical
	req.TopK = 5
	resp, err := db.SearchKnowledge(context.Background(), req)
	if err != nil {
		t.Fatalf("search %q: %v", req.Query, err)
	}
	var ids []string
	for _, h := range resp.Results {
		ids = append(ids, h.KnowledgeID)
	}
	return ids
}

// A French question, asked with its German translation as an alternate query,
// over a store with one German row that answers it. The words of the question
// and of the variant never all occur in one row, so a variant searched only as
// an AND of its words finds nothing; its words must reach the OR query, as
// those of keywords do.
const (
	variantQuestion = "Qui règle la procédure des cantons ?"
	variantSentence = "Wie regelt der Bund das Verfahren der Kantone?"
)

// A French question with a German variant. de-salary answers it; de-pay
// shares one content word of the variant (Gehalt); de-other shares only the
// variant's function words das, der and im, which most German rows contain.
const (
	salaryQuestion = "Qui fixe le salaire des juges ?"
	salaryVariant  = "Wer legt das Gehalt der Richter im Bund fest?"
)

// saveVariantCorpus stores the German answers, German rows that share only a
// variant's function words or one of its words, and French and German rows
// that share only the question's or the variant's function words: enough rows
// that a function word is as common as it is in a real store.
func saveVariantCorpus(t *testing.T, save func(id, content string)) {
	t.Helper()
	save("de-answer", "Der Bundesrat regelt das Verfahren der Kantone im Einzelnen.")
	save("de-other", "Das Parlament tagt in der Regel viermal im Jahr.")
	save("de-salary", "Das Parlament legt das Gehalt der Richter am Bundesgericht fest.")
	save("de-pay", "Das Gehalt der Angestellten steigt im Kanton Bern.")
	save("fr-other", "Le peuple suisse élit le Conseil national tous les quatre ans.")
	for i := 0; i < 20; i++ {
		save(fmt.Sprintf("fr-filler-%02d", i), fmt.Sprintf("La loi %d traite des questions de la vie des communes et qui les paie.", i))
		save(fmt.Sprintf("de-filler-%02d", i), fmt.Sprintf("Das Gesetz %d gilt in der Gemeinde im ganzen Land.", i))
	}
}

func saveVariantKnowledge(t *testing.T, db *DB) {
	t.Helper()
	saveVariantCorpus(t, func(id, content string) {
		if _, err := db.SaveKnowledge(context.Background(), KnowledgeSaveRequest{KnowledgeID: id, Content: content}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	})
}

func variantMemoryIDs(t *testing.T, query string, alternates []string) []string {
	t.Helper()
	db := openVariantStore(t)
	memories := map[string]string{}
	saveVariantCorpus(t, func(id, content string) { memories[id] = content })
	saveGlobalMemories(t, db, memories)
	resp, err := db.SearchMemory(context.Background(), MemorySearchRequest{Query: query, AlternateQueries: alternates, Scope: "global", TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	return memoryIDs(resp.Results)
}

func variantTextOnlyIDs(t *testing.T, query string, alternates []string) []string {
	t.Helper()
	db := openVariantStore(t)
	saveVariantKnowledge(t, db)
	results, err := db.SearchTextOnly(context.Background(), query, TextSearchOptions{TopK: 100, AlternateQueries: alternates})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var ids []string
	for _, r := range results {
		ids = append(ids, r.DocID)
	}
	return ids
}

func orQueries(queries []string) (plain, prefix []string) {
	for _, q := range queries {
		if !strings.Contains(q, " OR ") {
			continue
		}
		if strings.Contains(q, "*") {
			prefix = strings.Split(q, " OR ")
		} else {
			plain = strings.Split(q, " OR ")
		}
	}
	return plain, prefix
}

func TestLexicalSearchQueriesTakeAlternateWords(t *testing.T) {
	alternates := []string{variantSentence}
	queries := lexicalSearchQueries(variantQuestion, nil, alternates, alternateQueryWords(alternates))
	plain, prefix := orQueries(queries)
	for _, want := range []string{"procédure", "cantons", "verfahren", "kantone", "bund"} {
		if !slices.Contains(plain, want) {
			t.Errorf("OR query %q lacks %q; queries = %q", plain, want, queries)
		}
	}
	// The prefix form searches a word by the root of its stem: kanton* finds
	// Kantone and Kantons alike.
	for _, want := range []string{"verfahren*", "kanton*", "bund*"} {
		if !slices.Contains(prefix, want) {
			t.Errorf("prefix OR query %q lacks %q; queries = %q", prefix, want, queries)
		}
	}
	// The question's own OR query stays as it was, without the variant's words.
	if !slices.Contains(queries, strings.Join(lexicalQueryKeywords(variantQuestion), " OR ")) {
		t.Errorf("queries %q lack the question's own OR query", queries)
	}
	// Each alternate query still runs as a query of its own.
	if !slices.Contains(queries, sanitizeFTSQuery(variantSentence)) {
		t.Errorf("queries %q lack the alternate query itself", queries)
	}
}

func TestAlternateQueryWordsTakeEveryAlternate(t *testing.T) {
	got := alternateQueryWords([]string{"Wie regelt der Bund", "das Verfahren der Kantone"})
	for _, want := range []string{"regelt", "bund", "das", "verfahren", "kantone"} {
		if !slices.Contains(got, want) {
			t.Errorf("words %q lack %q", got, want)
		}
	}
	der := 0
	for _, w := range got {
		if w == "der" {
			der++
		}
	}
	if slices.Contains(got, "bunddas") || der != 1 {
		t.Errorf("words %q: alternates glued together, or a word taken twice", got)
	}
	if words := alternateQueryWords(nil); len(words) != 0 {
		t.Errorf("no alternate gave words %q", words)
	}
}

// A word of a variant is common when it is in half of the rows the variant's
// other words reach, and those rows are three or more.
func TestVariantCommonIsAFrequentWordOfTheMatchSet(t *testing.T) {
	cases := []struct {
		df, matched int
		want        bool
	}{
		{0, 0, false},
		{2, 2, false}, // two rows: too few to say
		{3, 3, true},
		{2, 4, true},
		{1, 3, false},
		{49, 100, false},
		{50, 100, true},
	}
	for _, c := range cases {
		if got := variantCommon(c.df, c.matched); got != c.want {
			t.Errorf("variantCommon(%d, %d) = %v, want %v", c.df, c.matched, got, c.want)
		}
	}
}

// Each word is measured on the rows the rest of its variant reaches: das and
// der are in most German rows that the other German words lead to; gehalt is
// in one of them. In French, each word has one or two rows beside it, too
// few to say: those words are undecided, and do not join.
func TestVariantWordsLeaveOutCommonWords(t *testing.T) {
	german := []string{"Das Gehalt der Richter.", "Das Gesetz der Gemeinde.", "Der Rat tagt.", "Das Volk wählt."}
	french := []string{"Le salaire des juges.", "La loi des communes.", "Le conseil siège."}
	var asked []string
	v := variantWordsOf([]string{"das Gehalt der Richter", "le salaire der juges"}, func(orQuery string) ([]string, error) {
		asked = append(asked, orQuery)
		if strings.Contains(orQuery, "gehalt") {
			return german, nil
		}
		return french, nil
	})
	if len(asked) != 2 || !strings.Contains(asked[0], "das OR gehalt") {
		t.Errorf("match queries %q, want one OR query per variant", asked)
	}
	if !slices.Equal(v.kept, []string{"gehalt", "richter"}) {
		t.Errorf("kept %q, want gehalt richter", v.kept)
	}
	if !v.common["das"] || !v.common["der"] || v.common["le"] || v.common["gehalt"] || v.common["salaire"] {
		t.Errorf("common %v, want das and der, not le, gehalt or salaire", v.common)
	}
	// A keyword the caller gave counts as it always has.
	if c := v.commonBeside([]string{"Der Rat"}); c["der"] || !c["das"] {
		t.Errorf("common beside the keyword der: %v, want das only", c)
	}
	if c := (variantWords{}).commonBeside([]string{"x"}); c != nil {
		t.Errorf("no common word: %v", c)
	}
	// A match that failed leaves the variant words out: the search runs as
	// it did before they joined the OR query.
	v = variantWordsOf([]string{"das Gehalt der Richter"}, func(string) ([]string, error) { return nil, fmt.Errorf("closed") })
	if len(v.kept) != 0 || len(v.common) != 0 {
		t.Errorf("after a failed match: %+v, want nothing", v)
	}
	// The same when a later variant's match would have worked.
	failFirst := 0
	v = variantWordsOf([]string{"das Gehalt der Richter", "das Gehalt der Richter im Bund"}, func(string) ([]string, error) {
		failFirst++
		if failFirst == 1 {
			return nil, fmt.Errorf("closed")
		}
		return german, nil
	})
	if len(v.kept) != 0 || len(v.common) != 0 {
		t.Errorf("after a failed first match: %+v, want nothing", v)
	}
	// And when the first variant's match worked and a later one failed.
	failSecond := 0
	v = variantWordsOf([]string{"das Gehalt der Richter", "le salaire der juges"}, func(string) ([]string, error) {
		failSecond++
		if failSecond == 2 {
			return nil, fmt.Errorf("closed")
		}
		return german, nil
	})
	if failSecond != 2 || len(v.kept) != 0 || len(v.common) != 0 {
		t.Errorf("after a failed second match: %+v, want nothing", v)
	}
	if v := variantWordsOf([]string{"?", ""}, nil); len(v.kept) != 0 {
		t.Errorf("variants without words: %+v", v)
	}
}

// A word that the rest of its variant never leads to is undecided, however
// many rows hold it: "k8s" and "rollout" reach nothing, so the three
// kubernetes rows say nothing about kubernetes, which does not join the OR
// query and counts as before. k8s and rollout have the three kubernetes rows
// beside them, and are in none. A word in every row that the others reach is
// common. A variant of one or two words is not measured at all.
func TestVariantWordsMeasureEachWordBesideTheOthers(t *testing.T) {
	rows := []string{"We deploy with kubernetes every week.", "Our kubernetes cluster runs in two zones.", "The kubernetes upgrade is planned."}
	asked := 0
	match := func(string) ([]string, error) { asked++; return rows, nil }
	v := variantWordsOf([]string{"k8s rollout kubernetes"}, match)
	if len(v.common) != 0 || !slices.Equal(v.kept, []string{"k8s", "rollout"}) {
		t.Errorf("k8s rollout kubernetes: %+v, want k8s and rollout kept, kubernetes undecided", v)
	}
	for _, short := range []string{"Kubernetes", "k8s kubernetes"} {
		if v := variantWordsOf([]string{short}, match); asked != 1 || len(v.common) != 0 || len(v.kept) != 0 {
			t.Errorf("variant %q: %+v after %d match queries, want it undecided and not measured", short, v, asked)
		}
	}
	// kubernetes is in all three rows that "cluster", "upgrade" and "deploy"
	// reach; each of those is in one.
	v = variantWordsOf([]string{"kubernetes cluster upgrade deploy"}, match)
	if !v.common["kubernetes"] || v.common["cluster"] || v.common["deploy"] {
		t.Errorf("common %v, want kubernetes alone", v.common)
	}
	// A row that holds no word of the variant (the word index folds accents,
	// the count does not) is not among the rows the others reach.
	none := append(append([]string{}, rows...), "Nothing here.", "Nothing here.", "Nothing here.", "Nothing here.")
	v = variantWordsOf([]string{"kubernetes cluster upgrade deploy"}, func(string) ([]string, error) { return none, nil })
	if !v.common["kubernetes"] {
		t.Errorf("with rows that hold no word: common %v, want kubernetes", v.common)
	}
	// A row that holds the word alone is not among the rows the others reach,
	// and does not count for the word either: kubernetes is in one of the four
	// rows deploy reaches (rollout reaches none).
	v = variantWordsOf([]string{"kubernetes deploy rollout"}, func(string) ([]string, error) {
		return []string{"We deploy on Monday.", "We deploy on Tuesday.", "We deploy at night.", "We deploy with kubernetes.", "Kubernetes one.", "Kubernetes two.", "Kubernetes three."}, nil
	})
	if len(v.common) != 0 || !slices.Equal(v.kept, []string{"kubernetes", "deploy", "rollout"}) {
		t.Errorf("kubernetes deploy rollout: %+v, want all three kept (one row in four beside kubernetes and deploy)", v)
	}
	// A word inside another is not the word: der is in "oder", not in these
	// rows, so it is in none of the three rows beside it (Pension reaches none).
	v = variantWordsOf([]string{"Gehalt der Pension"}, func(string) ([]string, error) {
		return []string{"Gehalt oder Lohn.", "Gehalt oder Rente.", "Gehalt oder Sold.", "Der Rat."}, nil
	})
	if v.common["der"] || !slices.Contains(v.kept, "der") {
		t.Errorf("der inside oder: %+v, want der kept", v)
	}
}

// A common word a row shares with an alternate query still weighs what its
// rarity says, but it is not one of the separate words that make a row an
// answer.
func TestRarityCoverageDoesNotCountCommonWords(t *testing.T) {
	q := newRarityQuery("salaire juges", []string{salaryVariant})
	q.common = map[string]bool{"das": true, "der": true, "im": true}
	s := newRarityScorer(q, map[string]int{"bund": 1, "salaire": 1}, 100)
	m := s.coverage("Das Parlament tagt in der Regel viermal im Jahr.")
	if m.words != 0 || m.share <= 0 {
		t.Errorf("row of common words only: %+v, want some share and no word", m)
	}
	if s.answers(m, 0.01) {
		t.Errorf("row of common words only answers: %+v", m)
	}
	m = s.coverage("Der Bundesrat regelt das Verfahren der Kantone im Einzelnen.")
	if m.words != 1 {
		t.Errorf("row with one content word (bund) and common ones: %+v, want one word", m)
	}
	m = s.coverage("Das Salaire der Juges.")
	if m.words != 2 || !s.answers(m, DefaultKnowledgeKeywordFloor) {
		t.Errorf("row with both question words: %+v, want two words and an answer", m)
	}
}

func TestKnowledgeSearchFindsDocumentThroughAlternateSentence(t *testing.T) {
	db := openVariantStore(t)
	saveVariantKnowledge(t, db)
	got := variantKnowledgeIDs(t, db, KnowledgeSearchRequest{Query: variantQuestion, AlternateQueries: []string{variantSentence}})
	if len(got) == 0 || got[0] != "de-answer" {
		t.Fatalf("results %v, want the German answer first", got)
	}
	if slices.Contains(got, "de-other") {
		t.Fatalf("results %v include the row that shares only the variant's function words", got)
	}
}

// The German variant is the second of two: every alternate's words join.
func TestKnowledgeSearchFindsDocumentThroughSecondAlternate(t *testing.T) {
	db := openVariantStore(t)
	saveVariantKnowledge(t, db)
	got := variantKnowledgeIDs(t, db, KnowledgeSearchRequest{Query: variantQuestion, AlternateQueries: []string{"Chi stabilisce la procedura dei cantoni?", variantSentence}})
	if len(got) == 0 || got[0] != "de-answer" {
		t.Fatalf("results %v, want the German answer first", got)
	}
}

func TestSearchTextFindsDocumentThroughAlternateSentence(t *testing.T) {
	db := openVariantStore(t)
	saveVariantKnowledge(t, db)
	resp, err := db.GraphRAGTools().SearchText(context.Background(), ToolSearchTextRequest{Query: variantQuestion, TopK: 5, AlternateQueries: []string{variantSentence}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var got []string
	for _, c := range resp.Chunks {
		got = append(got, c.DocumentID)
	}
	if !slices.Contains(got, "de-answer") {
		t.Fatalf("results %v, want the German answer among them", got)
	}
}

func TestSearchTextOnlyFindsDocumentThroughAlternateSentence(t *testing.T) {
	got := variantTextOnlyIDs(t, variantQuestion, []string{variantSentence})
	if !slices.Contains(got, "de-answer") {
		t.Fatalf("results %v, want the German answer among them", got)
	}
}

func TestMemorySearchFindsMemoryThroughAlternateSentence(t *testing.T) {
	got := variantMemoryIDs(t, variantQuestion, []string{variantSentence})
	if len(got) == 0 || got[0] != "de-answer" {
		t.Fatalf("results %v, want the German answer first", got)
	}
	if slices.Contains(got, "de-other") {
		t.Fatalf("results %v include the row that shares only the variant's function words", got)
	}
}

// A variant's function words are common in the store, so they do not reach
// the OR query, and a row that shares only them is not found, on any path.
func TestKnowledgeSearchRefusesRowSharingOnlyCommonVariantWords(t *testing.T) {
	db := openVariantStore(t)
	saveVariantKnowledge(t, db)
	if got := variantKnowledgeIDs(t, db, KnowledgeSearchRequest{Query: salaryQuestion, AlternateQueries: []string{salaryVariant}}); !slices.Equal(got, []string{"de-salary"}) {
		t.Fatalf("results %v, want [de-salary]", got)
	}
}

func TestMemorySearchRefusesRowSharingOnlyCommonVariantWords(t *testing.T) {
	if got := variantMemoryIDs(t, salaryQuestion, []string{salaryVariant}); !slices.Equal(got, []string{"de-salary"}) {
		t.Fatalf("results %v, want [de-salary]", got)
	}
}

func TestSearchTextOnlyLeavesOutCommonVariantWords(t *testing.T) {
	got := variantTextOnlyIDs(t, salaryQuestion, []string{salaryVariant})
	if !slices.Contains(got, "de-salary") {
		t.Fatalf("results %v lack the German answer", got)
	}
	for _, id := range got {
		if id == "de-other" || strings.HasPrefix(id, "de-filler") {
			t.Fatalf("results %v include a row that shares only the variant's function words", got)
		}
	}
}

// de-pay shares Gehalt, and the common der and im, with the variant: one
// word.
func TestKnowledgeSearchDoesNotCountCommonVariantWord(t *testing.T) {
	db := openVariantStore(t)
	saveVariantKnowledge(t, db)
	if got := variantKnowledgeIDs(t, db, KnowledgeSearchRequest{Query: "salaire juges", AlternateQueries: []string{"Gehalt der Richter im Bund"}}); !slices.Equal(got, []string{"de-salary"}) {
		t.Fatalf("results %v, want [de-salary]", got)
	}
}

func TestMemorySearchDoesNotCountCommonVariantWord(t *testing.T) {
	if got := variantMemoryIDs(t, "salaire juges", []string{"Gehalt der Richter im Bund"}); !slices.Equal(got, []string{"de-salary"}) {
		t.Fatalf("results %v, want [de-salary]", got)
	}
}

// A failed match leaves the variant words out on both stores.
func TestVariantMatchFailureLeavesTheWordsOut(t *testing.T) {
	db := openVariantStore(t)
	_ = db.Close()
	ctx := context.Background()
	if v := db.chunkVariantWords(ctx, "", []string{salaryVariant}); len(v.kept) != 0 {
		t.Errorf("chunks, closed store: %+v, want nothing", v)
	}
	if v := db.memoryVariantWords(ctx, "global", []string{salaryVariant}); len(v.kept) != 0 {
		t.Errorf("memory, closed store: %+v, want nothing", v)
	}
}

// The match set is taken in the collection searched: in "de", das and der are
// in most rows the variant reaches; over the whole store the variant also
// reaches 120 rows of "other" that name a Richter and hold neither.
func TestChunkVariantWordsCountTheSearchedCollection(t *testing.T) {
	db := openVariantStore(t)
	ctx := context.Background()
	saveVariantCorpus(t, func(id, content string) {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content, Collection: "de"}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	})
	for i := 0; i < 120; i++ {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: fmt.Sprintf("other-%03d", i), Content: fmt.Sprintf("Richter Nummer %d, Akte geschlossen.", i), Collection: "other"}); err != nil {
			t.Fatalf("save other-%03d: %v", i, err)
		}
	}
	alternates := []string{salaryVariant}
	if v := db.chunkVariantWords(ctx, "de", alternates); slices.Contains(v.kept, "das") || slices.Contains(v.kept, "der") || !slices.Contains(v.kept, "gehalt") {
		t.Errorf("collection de: kept %q, want gehalt without das and der", v.kept)
	}
	if v := db.chunkVariantWords(ctx, "", alternates); !slices.Contains(v.kept, "das") {
		t.Errorf("whole store: kept %q, want das, which most of that match set lacks", v.kept)
	}
	results, err := db.SearchTextOnly(ctx, salaryQuestion, TextSearchOptions{Collection: "de", TopK: 100, AlternateQueries: alternates})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range results {
		if r.DocID == "de-other" || strings.HasPrefix(r.DocID, "de-filler") {
			t.Fatalf("collection de: results include %s, which shares only the variant's function words", r.DocID)
		}
	}
}

// The memory path takes the match set in the memory bucket searched, not in
// the knowledge chunks, which here hold no German at all.
func TestMemoryVariantWordsCountTheMemoryBucket(t *testing.T) {
	db := openVariantStore(t)
	ctx := context.Background()
	memories := map[string]string{}
	saveVariantCorpus(t, func(id, content string) { memories[id] = content })
	saveGlobalMemories(t, db, memories)
	for i := 0; i < 20; i++ {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: fmt.Sprintf("en-%02d", i), Content: fmt.Sprintf("Report %d of the weather service.", i)}); err != nil {
			t.Fatalf("save en-%02d: %v", i, err)
		}
	}
	_, bucketID, err := resolveMemoryBucket("global", "", "", "")
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	if v := db.memoryVariantWords(ctx, bucketID, []string{salaryVariant}); slices.Contains(v.kept, "das") || slices.Contains(v.kept, "der") || !slices.Contains(v.kept, "gehalt") {
		t.Errorf("kept %q, want gehalt without das and der", v.kept)
	}
}

// The reviewer's store: 60 English rows and 4 German ones. German is a small
// share of the store, and its function words are still common among the rows
// a German variant reaches.
const (
	weatherQuestion = "What is the weather in Bern tomorrow?"
	weatherVariant  = "Wie ist das Wetter in der Stadt Bern im Sommer?"
)

func saveMinorityCorpus(t *testing.T, save func(id, content string), extra map[string]string) {
	t.Helper()
	for i := 0; i < 60; i++ {
		save(fmt.Sprintf("en-%02d", i), fmt.Sprintf("Release %d of the build tool ships with faster caching and a new log format.", i))
	}
	save("de-school", "Die Schule ist im Kanton der Ort, an dem das Kind lernt.")
	save("de-salary", "Das Parlament legt das Gehalt der Richter am Bundesgericht fest.")
	save("de-law", "Das Gesetz ist im ganzen Land gültig, sobald der Rat es beschliesst.")
	save("de-other", "Das Parlament tagt in der Regel viermal im Jahr.")
	for id, content := range extra {
		save(id, content)
	}
}

func minorityResults(t *testing.T, extra map[string]string) map[string][]string {
	t.Helper()
	ctx := context.Background()
	db := openVariantStore(t)
	memories := map[string]string{}
	saveMinorityCorpus(t, func(id, content string) {
		memories[id] = content
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}, extra)
	saveGlobalMemories(t, db, memories)
	alternates := []string{weatherVariant}
	got := map[string][]string{
		"SearchKnowledge": variantKnowledgeIDs(t, db, KnowledgeSearchRequest{Query: weatherQuestion, AlternateQueries: alternates}),
	}
	mem, err := db.SearchMemory(ctx, MemorySearchRequest{Query: weatherQuestion, AlternateQueries: alternates, Scope: "global", TopK: 5})
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	got["SearchMemory"] = memoryIDs(mem.Results)
	only, err := db.SearchTextOnly(ctx, weatherQuestion, TextSearchOptions{TopK: 10, AlternateQueries: alternates})
	if err != nil {
		t.Fatalf("SearchTextOnly: %v", err)
	}
	for _, r := range only {
		got["SearchTextOnly"] = append(got["SearchTextOnly"], r.DocID)
	}
	resp, err := db.GraphRAGTools().SearchText(ctx, ToolSearchTextRequest{Query: weatherQuestion, TopK: 10, AlternateQueries: alternates})
	if err != nil {
		t.Fatalf("search_text: %v", err)
	}
	for _, c := range resp.Chunks {
		got["search_text"] = append(got["search_text"], c.DocumentID)
	}
	return got
}

func TestMinorityLanguageVariantFindsNothingThroughItsFunctionWords(t *testing.T) {
	for path, ids := range minorityResults(t, nil) {
		if len(ids) != 0 {
			t.Errorf("%s: results %v, want none; the German rows share only the variant's function words", path, ids)
		}
	}
}

func TestMinorityLanguageVariantStillFindsItsContentWord(t *testing.T) {
	got := minorityResults(t, map[string]string{"de-weather": "Im Sommer ist das Wetter in der Stadt oft warm."})
	for path, ids := range got {
		if !slices.Equal(ids, []string{"de-weather"}) {
			t.Errorf("%s: results %v, want [de-weather]", path, ids)
		}
	}
}

// Keywords alone run as on main: a keyword most rows hold still counts as a
// word of the row.
func TestKeywordsAloneAreNotSortedByTheVariantRule(t *testing.T) {
	ctx := context.Background()
	db := openVariantStore(t)
	memories := map[string]string{"hit": "We deploy with kubernetes every week."}
	for i := 0; i < 40; i++ {
		memories[fmt.Sprintf("k-%02d", i)] = fmt.Sprintf("Note %d about kubernetes.", i)
	}
	for id, content := range memories {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	saveGlobalMemories(t, db, memories)
	got := variantKnowledgeIDs(t, db, KnowledgeSearchRequest{Query: "deploy service weekly", Keywords: []string{"kubernetes"}})
	if len(got) == 0 || got[0] != "hit" {
		t.Errorf("knowledge: results %v, want hit first", got)
	}
	mem, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "deploy service weekly", Keywords: []string{"kubernetes"}, Scope: "global", TopK: 5})
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	if ids := memoryIDs(mem.Results); len(ids) == 0 || ids[0] != "hit" {
		t.Errorf("memory: results %v, want hit first", ids)
	}
}

// The knowledge floor reports a failed count of the chunks, with or without
// alternate queries.
func TestKnowledgeFloorReportsAFailedCount(t *testing.T) {
	db := openVariantStore(t)
	_ = db.Close()
	result := &GraphRAGQueryResult{Chunks: []GraphRAGChunkResult{{Content: "Das Gehalt der Richter."}}}
	plan := RetrievalPlan{Query: salaryQuestion, AlternateQueries: []string{salaryVariant}}
	if err := db.applyKnowledgeFloors(context.Background(), result, plan, RetrievalDecision{}, "", GraphRAGQueryOptions{}); err == nil {
		t.Fatal("floor on a closed store: no error")
	}
}

// kubeAnswers searches a store, as knowledge and as memory, for "deploy
// service weekly" with one alternate query, and returns both answers.
func kubeAnswers(t *testing.T, rows map[string]string, req KnowledgeSearchRequest) (knowledge, memory []string) {
	t.Helper()
	ctx := context.Background()
	db := openVariantStore(t)
	for id, content := range rows {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	saveGlobalMemories(t, db, rows)
	req.Query = "deploy service weekly"
	knowledge = variantKnowledgeIDs(t, db, req)
	mem, err := db.SearchMemory(ctx, MemorySearchRequest{Query: req.Query, Keywords: req.Keywords, AlternateQueries: req.AlternateQueries, EntityNames: req.EntityNames, Scope: "global", TopK: 5})
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	return knowledge, memoryIDs(mem.Results)
}

// The reviewer's store: seven rows, three about kubernetes. A variant that
// reaches the store through kubernetes alone, as a synonym, as one word or as
// a sentence in a language the store lacks, answers as on main: the hit
// holds a question word and the variant's word.
var kubeRows = map[string]string{
	"hit":    "We deploy with kubernetes every week.",
	"k-zone": "Our kubernetes cluster runs in two zones.",
	"k-up":   "The kubernetes upgrade is planned for spring.",
	"desk":   "The service desk opens at nine.",
	"report": "Weekly reports go to the finance team.",
	"lunch":  "Lunch is served in the main hall.",
	"backup": "Backups run every night.",
}

func TestVariantReachingTheStoreThroughOneWordAnswersAsOnMain(t *testing.T) {
	for _, variant := range []string{"k8s kubernetes", "kubernetes", "Wie oft deployen wir auf Kubernetes?"} {
		knowledge, memory := kubeAnswers(t, kubeRows, KnowledgeSearchRequest{AlternateQueries: []string{variant}})
		if !slices.Equal(knowledge, []string{"hit"}) || !slices.Equal(memory, []string{"hit"}) {
			t.Errorf("variant %q: knowledge %v, memory %v, want [hit] and [hit] as on main", variant, knowledge, memory)
		}
	}
}

// kubeSynonymRows is the seven-row store and three rows that spell the term
// both ways.
func kubeSynonymRows() map[string]string {
	rows := map[string]string{}
	for id, content := range kubeRows {
		rows[id] = content
	}
	for i := 0; i < 3; i++ {
		rows[fmt.Sprintf("syn%d", i)] = fmt.Sprintf("k8s is short for kubernetes, note %d.", i)
	}
	return rows
}

// A short alias given as a variant, over a store where three rows spell the
// term both ways: a variant of two words is not measured, so "k8s" reaching
// three rows that all hold kubernetes does not make kubernetes common, and the
// hit answers as on main, in either order. The words are counted after
// stopwords are dropped: "k8s and kubernetes" is two words.
func TestSynonymPairSearchesAsOnMain(t *testing.T) {
	rows := kubeSynonymRows()
	for _, variant := range []string{"k8s kubernetes", "kubernetes k8s", "k8s and kubernetes"} {
		knowledge, memory := kubeAnswers(t, rows, KnowledgeSearchRequest{AlternateQueries: []string{variant}})
		if !slices.Equal(knowledge, []string{"hit"}) || !slices.Equal(memory, []string{"hit"}) {
			t.Errorf("variant %q: knowledge %v, memory %v, want [hit] and [hit] as on main", variant, knowledge, memory)
		}
	}
}

// The limit named in Breaking Changes: with a third word, the alias is
// measured, "k8s" reaches the three rows, all hold kubernetes, and kubernetes
// is common; the hit then shares only "deploy" with the search. A connector
// the stopword list lacks ("oder") is a third word. Given as keywords, the
// same words find the hit.
func TestAliasWithAThirdWordIsMeasured(t *testing.T) {
	rows := kubeSynonymRows()
	for _, variant := range []string{"k8s kubernetes cluster", "k8s oder kubernetes"} {
		knowledge, memory := kubeAnswers(t, rows, KnowledgeSearchRequest{AlternateQueries: []string{variant}})
		if len(knowledge) != 0 || len(memory) != 0 {
			t.Errorf("variant %q: knowledge %v, memory %v, want none (the named limit)", variant, knowledge, memory)
		}
	}
	knowledge, memory := kubeAnswers(t, rows, KnowledgeSearchRequest{Keywords: []string{"k8s", "kubernetes"}})
	if !slices.Equal(knowledge, []string{"hit"}) || !slices.Equal(memory, []string{"hit"}) {
		t.Errorf("keywords k8s kubernetes: knowledge %v, memory %v, want [hit] and [hit]", knowledge, memory)
	}
}

// The round-2 kubernetes store: 41 rows that all name kubernetes. "rollout"
// reaches none of them, so kubernetes is not measured beside it.
func TestVariantWithAnUnmatchedWordKeepsAWordMostRowsHold(t *testing.T) {
	rows := map[string]string{"hit": "We deploy with kubernetes every week."}
	for i := 0; i < 40; i++ {
		rows[fmt.Sprintf("k-%02d", i)] = fmt.Sprintf("Note %d about kubernetes.", i)
	}
	knowledge, memory := kubeAnswers(t, rows, KnowledgeSearchRequest{AlternateQueries: []string{"kubernetes rollout"}})
	if !slices.Equal(knowledge, []string{"hit"}) || !slices.Equal(memory, []string{"hit"}) {
		t.Errorf("knowledge %v, memory %v, want [hit] and [hit] as on main", knowledge, memory)
	}
	// "kubernetes note", two words, is not measured, and answers as on main.
	knowledge, memory = kubeAnswers(t, rows, KnowledgeSearchRequest{AlternateQueries: []string{"kubernetes note"}})
	if !slices.Equal(knowledge, []string{"hit"}) || !slices.Equal(memory, []string{"hit"}) {
		t.Errorf("variant kubernetes note: knowledge %v, memory %v, want [hit] and [hit] as on main", knowledge, memory)
	}
	// In "kubernetes release note", "note" leads to the 40 notes, which all
	// name kubernetes: kubernetes is common beside it, and the hit then shares
	// one word with the search. As an entity name, kubernetes counts as it
	// always has.
	knowledge, memory = kubeAnswers(t, rows, KnowledgeSearchRequest{AlternateQueries: []string{"kubernetes release note"}})
	if len(knowledge) != 0 || len(memory) != 0 {
		t.Errorf("variant kubernetes release note: knowledge %v, memory %v, want none", knowledge, memory)
	}
	knowledge, _ = kubeAnswers(t, rows, KnowledgeSearchRequest{AlternateQueries: []string{"kubernetes release note"}, EntityNames: []string{"Kubernetes"}})
	if !slices.Equal(knowledge, []string{"hit"}) {
		t.Errorf("entity name kubernetes: knowledge %v, want [hit]", knowledge)
	}
}

// The salary variant over the reviewer's 60 English and 4 German rows: das,
// der and im are common beside the rest of the sentence, and only de-salary
// shares content words with it.
func TestMinorityLanguageVariantFindsItsAnswerAlone(t *testing.T) {
	ctx := context.Background()
	db := openVariantStore(t)
	memories := map[string]string{}
	saveMinorityCorpus(t, func(id, content string) {
		memories[id] = content
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}, nil)
	saveGlobalMemories(t, db, memories)
	alternates := []string{salaryVariant}
	if got := variantKnowledgeIDs(t, db, KnowledgeSearchRequest{Query: salaryQuestion, AlternateQueries: alternates}); !slices.Equal(got, []string{"de-salary"}) {
		t.Errorf("knowledge: %v, want [de-salary]", got)
	}
	mem, err := db.SearchMemory(ctx, MemorySearchRequest{Query: salaryQuestion, AlternateQueries: alternates, Scope: "global", TopK: 5})
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	if got := memoryIDs(mem.Results); !slices.Equal(got, []string{"de-salary"}) {
		t.Errorf("memory: %v, want [de-salary]", got)
	}
	only, err := db.SearchTextOnly(ctx, salaryQuestion, TextSearchOptions{TopK: 10, AlternateQueries: alternates})
	if err != nil {
		t.Fatalf("SearchTextOnly: %v", err)
	}
	if len(only) != 1 || only[0].DocID != "de-salary" {
		t.Errorf("SearchTextOnly: %d rows, want de-salary alone", len(only))
	}
}

// Past the cap, the rows are read in rowid / id order, not by rank. One row in
// three of the first twenty holds Gehalt, and every row after them; ranked,
// or all read, Gehalt would look common.
func TestVariantMatchPastTheCapReadsRowsInRowidOrder(t *testing.T) {
	ctx := context.Background()
	db := openVariantStore(t)
	memories := map[string]string{}
	for i := 0; i < 60; i++ {
		content := fmt.Sprintf("Der Rat Nummer %d tagt im Saal.", i)
		if i%3 == 0 || i >= 21 {
			content = fmt.Sprintf("Das Gehalt Nummer %d der Angestellten steigt.", i)
		}
		id := fmt.Sprintf("row-%02d", i)
		memories[id] = content
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("row-%02d", i)
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Scope: "global", Content: memories[id]}); err != nil {
			t.Fatalf("save memory %s: %v", id, err)
		}
	}
	_, bucketID, err := resolveMemoryBucket("global", "", "", "")
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	alternates := []string{"Gehalt der Richter"}
	for name, v := range map[string]variantWords{
		"chunks": db.chunkVariantWordsUpTo(ctx, "", alternates, 20),
		"memory": db.memoryVariantWordsUpTo(ctx, bucketID, alternates, 20),
	} {
		if !slices.Contains(v.kept, "gehalt") || !v.common["der"] {
			t.Errorf("%s: kept %q, common %v, want gehalt kept and der common", name, v.kept, v.common)
		}
	}
}

// The match query reads the content alone, in rowid order on SQLite and id
// order on PostgreSQL.
func TestVariantMatchSQLReadsContentInRowidOrIdOrder(t *testing.T) {
	pg, args := variantMatchSQL(sqldialect.For(sqldialect.Postgres), "gehalt OR richter", "chunks_fts", "embeddings a", " JOIN collections c ON a.collection_id = c.id", "c.name = ?", []any{"de"}, 7)
	if !strings.HasPrefix(pg, "SELECT a.content FROM embeddings a JOIN collections c") || !strings.HasSuffix(pg, "AND c.name = ? ORDER BY a.id LIMIT ?") {
		t.Errorf("PostgreSQL: %s", pg)
	}
	if len(args) < 3 || args[len(args)-2] != "de" || args[len(args)-1] != 7 {
		t.Errorf("PostgreSQL args %v, want the condition's, then de and 7", args)
	}
	lite, args := variantMatchSQL(sqldialect.For(sqldialect.SQLite), "gehalt OR richter", "messages_fts", "messages a", "", "a.session_id = ?", []any{"b"}, 7)
	if lite != "SELECT a.content FROM messages_fts JOIN messages a ON a.rowid = messages_fts.rowid WHERE messages_fts MATCH ? AND a.session_id = ? ORDER BY messages_fts.rowid LIMIT ?" || !slices.Equal(args, []any{"gehalt OR richter", "b", 7}) {
		t.Errorf("SQLite: %s %v", lite, args)
	}
}

// stageCount is how many times a request ran stage, from its stage log.
func stageCount(s *observability.Stages, stage string) int {
	for _, part := range strings.Fields(s.String()) {
		name, rest, _ := strings.Cut(part, "=")
		if name != stage {
			continue
		}
		if _, n, ok := strings.Cut(rest, "×"); ok {
			c, _ := strconv.Atoi(n)
			return c
		}
		return 1
	}
	return 0
}

// A search sorts its variants once: SearchKnowledge on its tool path and in
// its floor, SearchTextOnly across the over-fetches of the gate.
func TestSearchMeasuresItsVariantsOnce(t *testing.T) {
	db := openVariantStore(t)
	ctx := context.Background()
	saveVariantKnowledge(t, db)
	for i := 0; i < 30; i++ {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: fmt.Sprintf("old-%02d", i), Content: fmt.Sprintf("Wer legt das Gehalt der Richter im Bund fest? Akte %d.", i), ExpiresAt: strPtr(pastInstant())}); err != nil {
			t.Fatalf("save old-%02d: %v", i, err)
		}
	}
	alternates := []string{salaryVariant}
	sctx, stages := observability.WithStages(ctx)
	if _, err := db.SearchKnowledge(sctx, KnowledgeSearchRequest{Query: salaryQuestion, AlternateQueries: alternates, RetrievalMode: RetrievalModeLexical, TopK: 5}); err != nil {
		t.Fatalf("SearchKnowledge: %v", err)
	}
	if n := stageCount(stages, "variant-words"); n != 1 {
		t.Errorf("SearchKnowledge measured its variants %d times (%s), want once", n, stages)
	}
	// The 30 expired copies of the variant come first for "Gehalt Richter",
	// so the gate asks again until it reaches de-salary.
	sctx, stages = observability.WithStages(ctx)
	results, err := db.SearchTextOnly(sctx, "Gehalt Richter", TextSearchOptions{TopK: 3, AlternateQueries: alternates})
	if err != nil {
		t.Fatalf("SearchTextOnly: %v", err)
	}
	var ids []string
	for _, r := range results {
		ids = append(ids, r.DocID)
	}
	if !slices.Contains(ids, "de-salary") {
		t.Fatalf("SearchTextOnly: %v, want de-salary past the expired rows", ids)
	}
	if n := stageCount(stages, "variant-words"); n != 1 {
		t.Errorf("SearchTextOnly measured its variants %d times (%s), want once", n, stages)
	}
}

// search_text counts the variant's words in the collection it searches, as
// SearchTextOnly does.
func TestSearchTextCountsVariantWordsInTheSearchedCollection(t *testing.T) {
	db := openVariantStore(t)
	ctx := context.Background()
	saveVariantCorpus(t, func(id, content string) {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content, Collection: "de"}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	})
	for i := 0; i < 120; i++ {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: fmt.Sprintf("other-%03d", i), Content: fmt.Sprintf("Richter Nummer %d, Akte geschlossen.", i), Collection: "other"}); err != nil {
			t.Fatalf("save other-%03d: %v", i, err)
		}
	}
	resp, err := db.GraphRAGTools().SearchText(ctx, ToolSearchTextRequest{Query: salaryQuestion, Collection: "de", TopK: 100, AlternateQueries: []string{salaryVariant}})
	if err != nil {
		t.Fatalf("search_text: %v", err)
	}
	var ids []string
	for _, c := range resp.Chunks {
		ids = append(ids, c.DocumentID)
	}
	if !slices.Contains(ids, "de-salary") {
		t.Fatalf("results %v lack de-salary", ids)
	}
	for _, id := range ids {
		if id == "de-other" || strings.HasPrefix(id, "de-filler") {
			t.Fatalf("results %v include %s, which shares only the variant's function words", ids, id)
		}
	}
}

// pathResults saves rows as knowledge and as global memories, and returns
// what each of the four lexical paths answers for question and alternates.
func pathResults(t *testing.T, rows map[string]string, question string, alternates []string) map[string][]string {
	t.Helper()
	ctx := context.Background()
	db := openVariantStore(t)
	for id, content := range rows {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	saveGlobalMemories(t, db, rows)
	got := map[string][]string{
		"SearchKnowledge": variantKnowledgeIDs(t, db, KnowledgeSearchRequest{Query: question, AlternateQueries: alternates}),
	}
	mem, err := db.SearchMemory(ctx, MemorySearchRequest{Query: question, AlternateQueries: alternates, Scope: "global", TopK: 5})
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	got["SearchMemory"] = memoryIDs(mem.Results)
	only, err := db.SearchTextOnly(ctx, question, TextSearchOptions{TopK: 5, AlternateQueries: alternates})
	if err != nil {
		t.Fatalf("SearchTextOnly: %v", err)
	}
	for _, r := range only {
		got["SearchTextOnly"] = append(got["SearchTextOnly"], r.DocID)
	}
	resp, err := db.GraphRAGTools().SearchText(ctx, ToolSearchTextRequest{Query: question, TopK: 5, AlternateQueries: alternates})
	if err != nil {
		t.Fatalf("search_text: %v", err)
	}
	for _, c := range resp.Chunks {
		got["search_text"] = append(got["search_text"], c.DocumentID)
	}
	return got
}

// englishWith is the reviewer's 60 English rows and the German rows named.
func englishWith(german ...string) map[string]string {
	all := map[string]string{
		"de-school": "Die Schule ist im Kanton der Ort, an dem das Kind lernt.",
		"de-other":  "Das Parlament tagt in der Regel viermal im Jahr.",
		"de-salary": "Das Parlament legt das Gehalt der Richter am Bundesgericht fest.",
		"de-law":    "Das Gesetz ist im ganzen Land gültig, sobald der Rat es beschliesst.",
	}
	rows := map[string]string{}
	for i := 0; i < 60; i++ {
		rows[fmt.Sprintf("en-%02d", i)] = fmt.Sprintf("Release %d of the build tool ships with faster caching and a new log format.", i)
	}
	for _, id := range german {
		rows[id] = all[id]
	}
	return rows
}

// With one or two German rows, the variant's other words reach too few rows
// to sort its words: they are undecided and search as on main, which finds
// nothing on every path, for the weather variant and for the salary one.
func TestFewRowsInTheVariantLanguageSearchAsOnMain(t *testing.T) {
	for _, german := range [][]string{{"de-school"}, {"de-school", "de-other"}} {
		for _, c := range []struct{ question, variant string }{{weatherQuestion, weatherVariant}, {salaryQuestion, salaryVariant}} {
			for path, ids := range pathResults(t, englishWith(german...), c.question, []string{c.variant}) {
				if len(ids) != 0 {
					t.Errorf("%v, %q, %s: %v, want none as on main", german, c.variant, path, ids)
				}
			}
		}
	}
}

// With three German rows, das, der and im are decided and common, and ist
// (in de-school, beside the two others) is not: SearchTextOnly and
// search_text, which have no floor, return de-school through it. The floor
// paths return nothing, as on main.
func TestThreeRowsInTheVariantLanguageSortItsWords(t *testing.T) {
	got := pathResults(t, englishWith("de-school", "de-other", "de-salary"), weatherQuestion, []string{weatherVariant})
	for path, want := range map[string][]string{"SearchKnowledge": nil, "SearchMemory": nil, "SearchTextOnly": {"de-school"}, "search_text": {"de-school"}} {
		if !slices.Equal(got[path], want) {
			t.Errorf("%s: %v, want %v", path, got[path], want)
		}
	}
}

// "Gehalt der Richter" over the PR's store: der has two rows beside it, so
// it is undecided and counts as a word, as main counts it. de-pay shares
// Gehalt and der with the variant on every path, as on main with the same
// words given as keywords.
func TestShortVariantSearchesItsUndecidedWordAsOnMain(t *testing.T) {
	rows := map[string]string{}
	saveVariantCorpus(t, func(id, content string) { rows[id] = content })
	for path, ids := range pathResults(t, rows, "salaire juges", []string{"Gehalt der Richter"}) {
		if !slices.Equal(ids, []string{"de-salary", "de-pay"}) {
			t.Errorf("%s: %v, want [de-salary de-pay]", path, ids)
		}
	}
}

// The memory path counts in the bucket searched: another user's memories
// name a Richter 120 times and hold neither das nor der.
func TestMemoryVariantWordsCountOnlyTheBucketSearched(t *testing.T) {
	db := openVariantStore(t)
	ctx := context.Background()
	memories := map[string]string{}
	saveVariantCorpus(t, func(id, content string) { memories[id] = content })
	saveGlobalMemories(t, db, memories)
	for i := 0; i < 120; i++ {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: fmt.Sprintf("u-%03d", i), Scope: "user", UserID: "other", Content: fmt.Sprintf("Richter Nummer %d, Akte geschlossen.", i)}); err != nil {
			t.Fatalf("save u-%03d: %v", i, err)
		}
	}
	_, bucketID, err := resolveMemoryBucket("global", "", "", "")
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	if v := db.memoryVariantWords(ctx, bucketID, []string{salaryVariant}); !v.common["das"] || !v.common["der"] {
		t.Errorf("common %v, want das and der, as in the global bucket alone", v.common)
	}
}

// The cap is 1,000 rows. Of 1,100 rows in rowid order, the first 500 hold
// Gehalt alone, the next 500 Gehalt and der, the last 100 Gehalt alone. Read
// to 1,000 rows, der is in 500 of the 1,000 rows Gehalt reaches, half, and is
// common; read to 999, it is in 499 of 999, and read to 1,001 or more, in 500
// of more than 1,000: not common.
func TestVariantMatchReadsAThousandRows(t *testing.T) {
	db := openVariantStore(t)
	ctx := context.Background()
	for i := 0; i < 1100; i++ {
		content := fmt.Sprintf("Ein Gehalt Nummer %d steigt.", i)
		if i >= 500 && i < 1000 {
			content = fmt.Sprintf("Das Gehalt Nummer %d der Angestellten.", i)
		}
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: fmt.Sprintf("r-%04d", i), Content: content}); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	if v := db.chunkVariantWords(ctx, "", []string{"Gehalt der Richter"}); !v.common["der"] {
		t.Errorf("%+v, want der common", v)
	}
}

// A search measures at most variantMaxMeasured alternate queries, each one
// match query of up to variantMatchCap rows. The words of the ones past the
// cap are undecided, and do not join the OR query.
func TestVariantWordsMeasureAtMostFourAlternates(t *testing.T) {
	// omega is in one of the four rows the rest of its variant reaches:
	// measured, it would join the OR query.
	rows := []string{"alpha beta gamma", "alpha beta", "beta gamma", "gamma alpha", "omega delta theta", "delta one", "delta two", "theta three"}
	asked := 0
	match := func(string) ([]string, error) { asked++; return rows, nil }
	alternates := []string{"k8s", "alpha beta gamma", "alpha gamma beta", "beta alpha gamma", "gamma beta alpha", "omega delta theta"}
	v := variantWordsOf(alternates, match)
	if asked != variantMaxMeasured {
		t.Errorf("measured %d alternates, want %d", asked, variantMaxMeasured)
	}
	if slices.Contains(v.kept, "omega") {
		t.Errorf("kept %q holds omega, a word of the fifth alternate of three words or more", v.kept)
	}
	if v := variantWordsOf(alternates[5:], match); !slices.Contains(v.kept, "omega") {
		t.Errorf("measured alone, kept %q, want omega: the test needs it to join", v.kept)
	}
}

// The measure reads a word as the word index and the coverage floor do,
// without its accents: "université" typed with them is the "universite" of
// the rows, and decides as it does typed without.
func TestVariantWordsCompareWithoutAccents(t *testing.T) {
	rows := []string{"La universite de Paris.", "Universite et professeurs.", "Professeurs de Paris.", "Universite, Paris, professeurs.", "Paris en hiver."}
	match := func(string) ([]string, error) { return rows, nil }
	plain := variantWordsOf([]string{"universite paris professeurs"}, match)
	accented := variantWordsOf([]string{"université paris professeurs"}, match)
	if len(plain.kept) == 0 && len(plain.common) == 0 {
		t.Fatalf("without accents: %+v, the test needs a decided word", plain)
	}
	fold := func(ws []string) []string {
		out := make([]string, len(ws))
		for i, w := range ws {
			out[i] = foldAccents(w)
		}
		return out
	}
	if !slices.Equal(fold(accented.kept), plain.kept) || fmt.Sprint(accented.common) != fmt.Sprint(plain.common) {
		t.Errorf("with accents: kept %q common %v; without: kept %q common %v", accented.kept, accented.common, plain.kept, plain.common)
	}
	// And rows that spell it with accents hold the word typed without.
	var spelled []string
	for _, r := range rows {
		spelled = append(spelled, strings.ReplaceAll(strings.ReplaceAll(r, "universite", "université"), "Universite", "Université"))
	}
	fromSpelled := variantWordsOf([]string{"universite paris professeurs"}, func(string) ([]string, error) { return spelled, nil })
	if !slices.Equal(fromSpelled.kept, plain.kept) || fmt.Sprint(fromSpelled.common) != fmt.Sprint(plain.common) {
		t.Errorf("rows with accents: kept %q common %v; without: kept %q common %v", fromSpelled.kept, fromSpelled.common, plain.kept, plain.common)
	}
	// A common word typed with accents is common to the coverage floor too.
	v := variantWords{common: map[string]bool{"universite": true}}
	if c := v.commonBeside([]string{"Université"}); c["universite"] {
		t.Errorf("common beside the keyword Université: %v, want none", c)
	}
	q := newRarityQuery("paris", []string{"université"})
	q.common = v.common
	s := newRarityScorer(q, map[string]int{"paris": 1, "université": 1}, 10)
	if m := s.coverage("Paris, université."); m.words != 1 {
		t.Errorf("a row with paris and the common université: %d words, want 1", m.words)
	}
}
