package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A local multi-hop yardstick for RetrievalModePPR: supporting-passage
// recall@2/@5 on a HippoRAG 2 release of 2WikiMultiHopQA or MuSiQue, for each
// retrieval mode, with no embedder and no LLM — the corpus graph is built by
// the same heuristic extractor no-embedder ingest uses, plus each passage's
// title, which is what an extractor reading a Wikipedia lead would name first.
//
// It reads the datasets from outside the repository and is skipped unless
// asked for:
//
//	CORTEXDB_PPR_BENCH_DIR=~/.cortexdb/bench \
//	CORTEXDB_PPR_BENCH_SET=2wikimultihopqa   # or musique
//	CORTEXDB_PPR_BENCH_N=300                  # questions (default 300)
//	CORTEXDB_PPR_BENCH_OFFSET=500             # skip the first questions (held-out slice)
//	CORTEXDB_PPR_BENCH_DB=/path/cache.db      # ingested corpus, reused if present
//	CORTEXDB_PPR_BENCH_MODES=lexical,graph,ppr,ppr-hipporag
//	CORTEXDB_PPR_BENCH_OPTS='{"damping":0.5}' # PPRRetrievalOptions as JSON
//	go test ./pkg/cortexdb -run TestPPRMultiHopBench -v -timeout 2h
//
// Recall is HippoRAG's: the fraction of a question's gold supporting
// passages among the top k, averaged over questions.

type pprBenchQuestion struct {
	ID       string
	Type     string
	Question string
	Gold     []string // knowledge ids
}

type pprBenchPassage struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

func pprBenchKnowledgeID(i int) string { return fmt.Sprintf("p%05d", i) }

func loadPPRBench(t *testing.T, dir, set string, offset, n int) ([]pprBenchPassage, []pprBenchQuestion) {
	t.Helper()
	// musique-1hop is MuSiQue's corpus asked single-hop questions: the first
	// step of each question's decomposition, which names its subject outright
	// and has one gold paragraph. It is the check that a mode built for
	// multi-hop questions does not cost the plain ones.
	files := strings.TrimSuffix(set, "-1hop")
	read := func(name string, v any) {
		raw, err := os.ReadFile(filepath.Join(dir, files, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
	}
	var corpus []pprBenchPassage
	read(files+"_corpus.json", &corpus)
	byTitle := make(map[string]int, len(corpus))
	byTitleText := make(map[string]int, len(corpus))
	for i, p := range corpus {
		byTitle[p.Title] = i
		byTitleText[p.Title+"\x00"+p.Text] = i
	}

	var questions []pprBenchQuestion
	switch set {
	case "2wikimultihopqa":
		var raw []struct {
			ID         string  `json:"_id"`
			Type       string  `json:"type"`
			Question   string  `json:"question"`
			Supporting [][]any `json:"supporting_facts"`
		}
		read(set+".json", &raw)
		for _, q := range raw {
			seen := map[string]bool{}
			var gold []string
			for _, sf := range q.Supporting {
				title, _ := sf[0].(string)
				if i, ok := byTitle[title]; ok && !seen[title] {
					seen[title] = true
					gold = append(gold, pprBenchKnowledgeID(i))
				}
			}
			questions = append(questions, pprBenchQuestion{ID: q.ID, Type: q.Type, Question: q.Question, Gold: gold})
		}
	case "musique", "musique-1hop":
		var raw []struct {
			ID         string `json:"id"`
			Question   string `json:"question"`
			Paragraphs []struct {
				Title      string `json:"title"`
				Text       string `json:"paragraph_text"`
				Supporting bool   `json:"is_supporting"`
			} `json:"paragraphs"`
			Decomposition []struct {
				Question string `json:"question"`
				Support  int    `json:"paragraph_support_idx"`
			} `json:"question_decomposition"`
		}
		read(files+".json", &raw)
		for _, q := range raw {
			if set == "musique-1hop" {
				if len(q.Decomposition) == 0 {
					continue
				}
				step := q.Decomposition[0]
				if step.Support < 0 || step.Support >= len(q.Paragraphs) {
					continue
				}
				p := q.Paragraphs[step.Support]
				i, ok := byTitleText[p.Title+"\x00"+p.Text]
				if !ok {
					continue
				}
				questions = append(questions, pprBenchQuestion{ID: q.ID, Type: "1hop",
					Question: strings.ReplaceAll(step.Question, ">>", " "), Gold: []string{pprBenchKnowledgeID(i)}})
				continue
			}
			var gold []string
			for _, p := range q.Paragraphs {
				if !p.Supporting {
					continue
				}
				if i, ok := byTitleText[p.Title+"\x00"+p.Text]; ok {
					gold = append(gold, pprBenchKnowledgeID(i))
				}
			}
			hops := strings.SplitN(q.ID, "__", 2)[0]
			questions = append(questions, pprBenchQuestion{ID: q.ID, Type: hops, Question: q.Question, Gold: gold})
		}
	default:
		t.Fatalf("unknown set %q", set)
	}
	if offset > 0 && offset < len(questions) {
		questions = questions[offset:]
	}
	if n > 0 && n < len(questions) {
		questions = questions[:n]
	}
	return corpus, questions
}

func ingestPPRBench(t *testing.T, ctx context.Context, db *DB, corpus []pprBenchPassage) {
	t.Helper()
	start := time.Now()
	for i, p := range corpus {
		id := pprBenchKnowledgeID(i)
		chunk := graphChunkNodeID(id, 0)
		names := []string{p.Title}
		for _, e := range extractCorpusEntities(p.Text) {
			names = append(names, e.Name)
		}
		seen := map[string]bool{}
		var entities []ToolEntityInput
		for _, name := range names {
			key := graphEntityNodeID(name)
			if strings.TrimSpace(name) == "" || seen[key] {
				continue
			}
			seen[key] = true
			entities = append(entities, ToolEntityInput{Name: name, ChunkIDs: []string{chunk}})
		}
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
			KnowledgeID: id,
			Title:       p.Title,
			Content:     p.Title + ". " + p.Text,
			// One passage, one chunk: the benchmark scores passages.
			ChunkSize: 100000,
			Entities:  entities,
		}); err != nil {
			t.Fatalf("ingest %s: %v", id, err)
		}
		if (i+1)%1000 == 0 {
			t.Logf("ingested %d/%d passages in %s", i+1, len(corpus), time.Since(start).Round(time.Second))
		}
	}
}

func TestPPRMultiHopBench(t *testing.T) {
	dir := os.Getenv("CORTEXDB_PPR_BENCH_DIR")
	if dir == "" {
		t.Skip("CORTEXDB_PPR_BENCH_DIR unset — the multi-hop benchmark reads datasets from outside the repository")
	}
	set := firstNonEmpty(os.Getenv("CORTEXDB_PPR_BENCH_SET"), "2wikimultihopqa")
	n, _ := strconv.Atoi(os.Getenv("CORTEXDB_PPR_BENCH_N"))
	if n == 0 {
		n = 300
	}
	modes := strings.Split(firstNonEmpty(os.Getenv("CORTEXDB_PPR_BENCH_MODES"), "lexical,graph,ppr,ppr-hipporag"), ",")
	var pprOpts PPRRetrievalOptions
	if raw := os.Getenv("CORTEXDB_PPR_BENCH_OPTS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &pprOpts); err != nil {
			t.Fatalf("CORTEXDB_PPR_BENCH_OPTS: %v", err)
		}
	}

	ctx := context.Background()
	offset, _ := strconv.Atoi(os.Getenv("CORTEXDB_PPR_BENCH_OFFSET"))
	corpus, questions := loadPPRBench(t, dir, set, offset, n)
	dbPath := os.Getenv("CORTEXDB_PPR_BENCH_DB")
	if dbPath == "" {
		dbPath = filepath.Join(t.TempDir(), "bench.db")
	}
	_, statErr := os.Stat(dbPath)
	fresh := os.IsNotExist(statErr)
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if fresh {
		ingestPPRBench(t, ctx, db, corpus)
	}

	type stat struct {
		r2, r5    float64
		latencies []time.Duration
		byType    map[string][2]float64 // sum r5, count
	}
	results := map[string]*stat{}
	for _, mode := range modes {
		results[mode] = &stat{byType: map[string][2]float64{}}
	}
	// Modes take turns on each question rather than each running the whole
	// set, so load from anything else on the machine lands on all of them
	// alike and the latency columns stay comparable.
	for _, q := range questions {
		for _, mode := range modes {
			st := results[mode]
			req := KnowledgeSearchRequest{Query: q.Question, TopK: 5, MaxContextChars: 1 << 30}
			switch mode {
			case "ppr": // the default fusion, RRF
				o := pprOpts
				req.RetrievalMode, req.PPR = RetrievalModePPR, &o
			case "ppr-hipporag":
				o := pprOpts
				o.Fusion = PPRFusionPPR
				req.RetrievalMode, req.PPR = RetrievalModePPR, &o
			default:
				req.RetrievalMode = mode
			}
			start := time.Now()
			resp, err := db.SearchKnowledge(ctx, req)
			st.latencies = append(st.latencies, time.Since(start))
			if err != nil {
				t.Fatalf("%s %s: %v", mode, q.ID, err)
			}
			var ranked []string
			seen := map[string]bool{}
			for _, c := range resp.Chunks {
				if !seen[c.DocumentID] {
					seen[c.DocumentID] = true
					ranked = append(ranked, c.DocumentID)
				}
			}
			recall := func(k int) float64 {
				if len(q.Gold) == 0 {
					return 0
				}
				hit := 0
				for _, g := range q.Gold {
					for i, id := range ranked {
						if i >= k {
							break
						}
						if id == g {
							hit++
							break
						}
					}
				}
				return float64(hit) / float64(len(q.Gold))
			}
			st.r2 += recall(2)
			r5 := recall(5)
			st.r5 += r5
			bt := st.byType[q.Type]
			st.byType[q.Type] = [2]float64{bt[0] + r5, bt[1] + 1}
		}
	}

	pct := func(ds []time.Duration, p float64) time.Duration {
		s := append([]time.Duration(nil), ds...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		return s[int(math.Ceil(p*float64(len(s))))-1]
	}
	t.Logf("%s, %d questions from #%d, %d passages", set, len(questions), offset, len(corpus))
	t.Logf("%-8s %9s %9s %9s %9s  %s", "mode", "recall@2", "recall@5", "p50", "p95", "recall@5 by type")
	for _, mode := range modes {
		st := results[mode]
		var types []string
		for k := range st.byType {
			types = append(types, k)
		}
		sort.Strings(types)
		var parts []string
		for _, k := range types {
			v := st.byType[k]
			parts = append(parts, fmt.Sprintf("%s=%.3f(n=%d)", k, v[0]/v[1], int(v[1])))
		}
		qn := float64(len(questions))
		t.Logf("%-8s %9.3f %9.3f %9s %9s  %s", mode, st.r2/qn, st.r5/qn,
			pct(st.latencies, 0.5).Round(100*time.Microsecond), pct(st.latencies, 0.95).Round(100*time.Microsecond), strings.Join(parts, " "))
	}
}
