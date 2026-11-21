package cortexdb

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
)

// The fact-change eval: when a fact changed, does recall answer with the
// current value, say that it changed, from what and when, and answer as of an
// instant between two versions with the version valid then?
//
// A fixture brain holds sixteen facts, each saved once and superseded once or
// twice by a later memory (a budget, a port, an on-call owner, a proxy, two
// in Chinese), with thirty distractors written in the same words about other
// things. Saves are spread over three months by backdating them, as a real
// brain's are. Three kinds of question, asked the way the hooks ask (the
// question and its words, top 3), through the tool surface so the same
// harness runs against a build from before the feature:
//
//   - current: the top memory hit states the current value;
//   - change ("did X change / what was X before"): the context pack the agent
//     reads carries the previous value and the date it changed;
//   - as_of: pinned to the midpoint between two versions, the top memory hit
//     states the version valid then.
//
// Cost is recall latency (p50/p95 over three rounds of every question) and
// the context pack's size in tokens (runes/4) for the current questions.
//
// Optional, off by default and never in CI:
//
//	CORTEXDB_CHANGE_EVAL_SNAPSHOT=<copy of a brain>  latency and pack size on that copy
//	CORTEXDB_CHANGE_EVAL_QUERIES=<file.jsonl>         its queries, one {"query": ...} per line
//	CORTEXDB_CHANGE_EVAL_REPLACERS=1                  ask instead about the memories that superseded others
//	CORTEXDB_CHANGE_EVAL_OUT=<file.json>              write the reports

type changeEvalFixture struct {
	Cases []struct {
		ID       string `json:"id"`
		Versions []struct {
			Day   int    `json:"day"`
			Token string `json:"token"`
			Text  string `json:"text"`
		} `json:"versions"`
		CurrentQ string `json:"current_q"`
		ChangeQ  string `json:"change_q"`
	} `json:"cases"`
	Distractors []struct {
		Day  int    `json:"day"`
		Text string `json:"text"`
	} `json:"distractors"`
}

// changeEvalBase is day 0 of the fixture.
var changeEvalBase = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

func changeEvalDay(day int) time.Time { return changeEvalBase.AddDate(0, 0, day) }

func loadChangeEvalFixture(t *testing.T) changeEvalFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "fact_changes", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx changeEvalFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	// A previous value the current text also states would be found without
	// the chain; the fixture must not hold one.
	for _, c := range fx.Cases {
		last := c.Versions[len(c.Versions)-1]
		for _, v := range c.Versions[:len(c.Versions)-1] {
			if strings.Contains(last.Text, v.Token) {
				t.Fatalf("%s: the current text states the old value %q", c.ID, v.Token)
			}
			for _, d := range fx.Distractors {
				if strings.Contains(d.Text, v.Token) {
					t.Fatalf("%s: distractor %q states the old value %q", c.ID, d.Text, v.Token)
				}
			}
		}
	}
	return fx
}

// backdateMemory sets when a memory was saved and, when superseded is not
// zero, when it was replaced: a test cannot wait days between saves.
func backdateMemory(t *testing.T, db *DB, id string, saved, superseded time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.exec(ctx, `UPDATE messages SET created_at = ? WHERE id = ?`, saved.UTC(), id); err != nil {
		t.Fatalf("backdate %s: %v", id, err)
	}
	if superseded.IsZero() {
		return
	}
	row, err := db.loadMemoryRow(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	md := cloneAnyMap(row.record.Metadata)
	md["superseded_at"] = superseded.UTC().Format(time.RFC3339)
	raw, _ := json.Marshal(md)
	if _, err := db.exec(ctx, `UPDATE messages SET metadata = ? WHERE id = ?`, raw, id); err != nil {
		t.Fatal(err)
	}
}

func seedChangeEvalBrain(t *testing.T, db *DB, fx changeEvalFixture) {
	t.Helper()
	ctx := context.Background()
	for i, d := range fx.Distractors {
		id := fmt.Sprintf("distractor-%02d", i)
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Scope: MemoryScopeGlobal, Content: d.Text}); err != nil {
			t.Fatal(err)
		}
		backdateMemory(t, db, id, changeEvalDay(d.Day), time.Time{})
	}
	for _, c := range fx.Cases {
		for i, v := range c.Versions {
			req := MemorySaveRequest{MemoryID: fmt.Sprintf("%s-v%d", c.ID, i+1), Scope: MemoryScopeGlobal, Content: v.Text,
				Metadata: map[string]any{"source": "eval"}}
			if i > 0 {
				req.Supersedes = []string{fmt.Sprintf("%s-v%d", c.ID, i)}
			}
			if _, err := db.SaveMemory(WithWriter(ctx, "eval-writer"), req); err != nil {
				t.Fatal(err)
			}
		}
		for i, v := range c.Versions {
			var replaced time.Time
			if i+1 < len(c.Versions) {
				replaced = changeEvalDay(c.Versions[i+1].Day)
			}
			backdateMemory(t, db, fmt.Sprintf("%s-v%d", c.ID, i+1), changeEvalDay(v.Day), replaced)
		}
	}
}

// changeEvalEmbedder is a deterministic stand-in for a model: words (and
// character bigrams of Chinese) hashed into 256 dimensions. Texts that share
// words are near; it knows no synonyms.
type changeEvalEmbedder struct{}

func (changeEvalEmbedder) Dim() int { return 256 }
func (changeEvalEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, 256)
	add := func(term string) {
		h := fnv.New32a()
		_, _ = io.WriteString(h, term)
		v[h.Sum32()%256]++
	}
	var han []rune
	flush := func() {
		for i := 0; i+1 < len(han); i++ {
			add(string(han[i : i+2]))
		}
		han = han[:0]
	}
	for _, f := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-' && r != ':'
	}) {
		var word []rune
		for _, r := range f {
			if unicode.Is(unicode.Han, r) {
				han = append(han, r)
				continue
			}
			flush()
			word = append(word, r)
		}
		flush()
		if len(word) > 1 {
			add(strings.Trim(string(word), ".-:"))
		}
	}
	var norm float64
	for _, x := range v {
		norm += float64(x * x)
	}
	if norm > 0 {
		n := float32(math.Sqrt(norm))
		for i := range v {
			v[i] /= n
		}
	}
	return v, nil
}
func (e changeEvalEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i], _ = e.Embed(ctx, text)
	}
	return out, nil
}

// changeEvalRecall is what the harness reads of a recall answer: JSON, so a
// build without the replaces field reads as one that sent none.
type changeEvalRecall struct {
	Memories []struct {
		Memory struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"memory"`
		Replaces []struct {
			ID           string `json:"id"`
			Excerpt      string `json:"excerpt"`
			SupersededAt string `json:"superseded_at"`
		} `json:"replaces"`
	} `json:"memories"`
	ContextPack struct {
		Text string `json:"text"`
	} `json:"context_pack"`
}

func changeEvalAsk(t *testing.T, db *DB, query, asOf string) (changeEvalRecall, float64) {
	t.Helper()
	args := map[string]any{
		"query":           query,
		"keywords":        rulesEvalWords(query),
		"top_k_memories":  3,
		"top_k_knowledge": 3,
		"graph_light":     true,
	}
	if asOf != "" {
		args["as_of"] = asOf
	}
	raw, _ := json.Marshal(args)
	start := time.Now()
	out, err := db.GraphRAGTools().Call(context.Background(), "knowledge_memory_recall", raw)
	ms := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		t.Fatalf("recall %q: %v", query, err)
	}
	body, _ := json.Marshal(out)
	var resp changeEvalRecall
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	return resp, ms
}

type changeEvalRow struct {
	Case     string `json:"case"`
	Kind     string `json:"kind"`
	Query    string `json:"query"`
	AsOf     string `json:"as_of,omitempty"`
	Want     string `json:"want"`
	Top      string `json:"top_memory_id,omitempty"`
	Pass     bool   `json:"pass"`
	InHits   bool   `json:"in_hits_json,omitempty"`
	PackToks int    `json:"pack_tokens,omitempty"`
}

type changeEvalReport struct {
	Mode          string          `json:"mode"`
	Current       int             `json:"current_pass"`
	CurrentN      int             `json:"current_total"`
	Change        int             `json:"change_pass"`
	ChangeHits    int             `json:"change_pass_hits_json"`
	ChangeN       int             `json:"change_total"`
	AsOf          int             `json:"as_of_pass"`
	AsOfN         int             `json:"as_of_total"`
	RecallP50     float64         `json:"recall_p50_ms"`
	RecallP95     float64         `json:"recall_p95_ms"`
	PackTokensAvg float64         `json:"pack_tokens_avg"`
	PackTokensMax int             `json:"pack_tokens_max"`
	Rows          []changeEvalRow `json:"rows"`
}

func (r changeEvalReport) table() string {
	pct := func(n, d int) string {
		if d == 0 {
			return "-"
		}
		return fmt.Sprintf("%.1f%% (%d/%d)", 100*float64(n)/float64(d), n, d)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n[%s]\n", r.Mode)
	fmt.Fprintf(&b, "current-value accuracy   %s\n", pct(r.Current, r.CurrentN))
	fmt.Fprintf(&b, "change awareness (pack)  %s   (hits JSON %s)\n", pct(r.Change, r.ChangeN), pct(r.ChangeHits, r.ChangeN))
	fmt.Fprintf(&b, "as_of accuracy           %s\n", pct(r.AsOf, r.AsOfN))
	fmt.Fprintf(&b, "recall latency           p50 %.2f ms   p95 %.2f ms\n", r.RecallP50, r.RecallP95)
	fmt.Fprintf(&b, "context pack (current q) avg %.1f tokens   max %d\n", r.PackTokensAvg, r.PackTokensMax)
	return b.String()
}

func packTokens(text string) int { return (len([]rune(text)) + 3) / 4 }

func topMemory(resp changeEvalRecall) (string, string) {
	if len(resp.Memories) == 0 {
		return "", ""
	}
	return resp.Memories[0].Memory.ID, resp.Memories[0].Memory.Content
}

func runChangeEval(t *testing.T, db *DB, mode string, fx changeEvalFixture) changeEvalReport {
	t.Helper()
	rep := changeEvalReport{Mode: mode}
	var latencies []float64
	tokensSum := 0
	const rounds = 3
	for round := 0; round < rounds; round++ {
		score := round == 0
		for _, c := range fx.Cases {
			last := c.Versions[len(c.Versions)-1]

			resp, ms := changeEvalAsk(t, db, c.CurrentQ, "")
			latencies = append(latencies, ms)
			if score {
				id, content := topMemory(resp)
				row := changeEvalRow{Case: c.ID, Kind: "current", Query: c.CurrentQ, Want: last.Token, Top: id,
					Pass: strings.Contains(content, last.Token), PackToks: packTokens(resp.ContextPack.Text)}
				rep.CurrentN++
				if row.Pass {
					rep.Current++
				}
				tokensSum += row.PackToks
				rep.PackTokensMax = max(rep.PackTokensMax, row.PackToks)
				rep.Rows = append(rep.Rows, row)
			}

			// The previous value and the date it stopped being current.
			prev := c.Versions[len(c.Versions)-2]
			date := changeEvalDay(last.Day).Format("2006-01-02")
			resp, ms = changeEvalAsk(t, db, c.ChangeQ, "")
			latencies = append(latencies, ms)
			if score {
				id, _ := topMemory(resp)
				row := changeEvalRow{Case: c.ID, Kind: "change", Query: c.ChangeQ, Want: prev.Token + " @ " + date, Top: id,
					Pass: strings.Contains(resp.ContextPack.Text, prev.Token) && strings.Contains(resp.ContextPack.Text, date)}
				for _, m := range resp.Memories {
					for _, r := range m.Replaces {
						if strings.Contains(r.Excerpt, prev.Token) && strings.HasPrefix(r.SupersededAt, date) {
							row.InHits = true
						}
					}
				}
				rep.ChangeN++
				if row.Pass {
					rep.Change++
				}
				if row.InHits {
					rep.ChangeHits++
				}
				rep.Rows = append(rep.Rows, row)
			}

			// Midway between each version and the next, the earlier one holds.
			for i := 0; i+1 < len(c.Versions); i++ {
				from, to := changeEvalDay(c.Versions[i].Day), changeEvalDay(c.Versions[i+1].Day)
				at := from.Add(to.Sub(from) / 2).Format(time.RFC3339)
				resp, ms := changeEvalAsk(t, db, c.CurrentQ, at)
				latencies = append(latencies, ms)
				if score {
					id, content := topMemory(resp)
					row := changeEvalRow{Case: c.ID, Kind: "as_of", Query: c.CurrentQ, AsOf: at, Want: c.Versions[i].Token, Top: id,
						Pass: strings.Contains(content, c.Versions[i].Token)}
					rep.AsOfN++
					if row.Pass {
						rep.AsOf++
					}
					rep.Rows = append(rep.Rows, row)
				}
			}
		}
	}
	rep.RecallP50, rep.RecallP95 = percentile(latencies, 0.5), percentile(latencies, 0.95)
	if rep.CurrentN > 0 {
		rep.PackTokensAvg = float64(tokensSum) / float64(rep.CurrentN)
	}
	return rep
}

func writeChangeEvalOut(t *testing.T, reports ...changeEvalReport) {
	path := os.Getenv("CORTEXDB_CHANGE_EVAL_OUT")
	if path == "" {
		return
	}
	raw, _ := json.MarshalIndent(reports, "", "  ")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Errorf("write %s: %v", path, err)
	}
}

func TestFactChangeRecallEval(t *testing.T) {
	if os.Getenv("CORTEXDB_CHANGE_EVAL_SNAPSHOT") != "" {
		t.Skip("snapshot run: see TestFactChangeRecallEvalSnapshot")
	}
	fx := loadChangeEvalFixture(t)
	var reports []changeEvalReport
	for _, mode := range []struct {
		name string
		opts []Option
	}{
		{"fixture, no embedder", nil},
		{"fixture, hashed-word embedder", []Option{WithEmbedder(changeEvalEmbedder{}), WithVectorHealing(VectorHealOptions{Disabled: true})}},
	} {
		db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "change.db")), mode.opts...)
		if err != nil {
			t.Fatal(err)
		}
		seedChangeEvalBrain(t, db, fx)
		rep := runChangeEval(t, db, mode.name, fx)
		_ = db.Close()
		t.Log(rep.table())
		for _, row := range rep.Rows {
			if !row.Pass {
				t.Logf("miss %s %-7s %q as_of=%s want %q top %s", row.Case, row.Kind, row.Query, row.AsOf, row.Want, row.Top)
			}
		}
		reports = append(reports, rep)

		// Floors under what was measured when this was written (README,
		// CHANGELOG): 100% on all three in both modes. Before, main scored
		// 0% on change awareness and as_of.
		ratio := func(n, d int) float64 { return float64(n) / float64(d) }
		if got := ratio(rep.Current, rep.CurrentN); got < 0.9 {
			t.Errorf("%s: current-value accuracy %.3f, want >= 0.9", mode.name, got)
		}
		if got := ratio(rep.Change, rep.ChangeN); got < 0.9 {
			t.Errorf("%s: change awareness %.3f, want >= 0.9", mode.name, got)
		}
		if got := ratio(rep.AsOf, rep.AsOfN); got < 0.9 {
			t.Errorf("%s: as_of accuracy %.3f, want >= 0.9", mode.name, got)
		}
	}
	writeChangeEvalOut(t, reports...)
}

// TestFactChangeRecallEvalSnapshot measures cost on a copy of a real brain:
// recall latency and context-pack size over its queries, and how many hits
// carried a replaced chain. Recall writes recall counts, so it must be a copy.
func TestFactChangeRecallEvalSnapshot(t *testing.T) {
	path, queries := os.Getenv("CORTEXDB_CHANGE_EVAL_SNAPSHOT"), os.Getenv("CORTEXDB_CHANGE_EVAL_QUERIES")
	if path == "" || queries == "" {
		t.Skip("set CORTEXDB_CHANGE_EVAL_SNAPSHOT and CORTEXDB_CHANGE_EVAL_QUERIES")
	}
	f, err := os.Open(queries)
	if err != nil {
		t.Fatal(err)
	}
	var qs []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var q struct {
			Query string `json:"query"`
		}
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") && json.Unmarshal([]byte(line), &q) == nil && q.Query != "" {
			qs = append(qs, q.Query)
		}
	}
	_ = f.Close()
	db, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The memories that superseded others, asked about by their own first
	// words: the recalls whose pack and latency the chain changes.
	if os.Getenv("CORTEXDB_CHANGE_EVAL_REPLACERS") != "" {
		qs = nil
		rows, err := db.query(context.Background(), `
			SELECT DISTINCT r.content FROM messages m JOIN messages r
			  ON r.id = json_extract(m.metadata, '$.superseded_by')
			WHERE m.session_id LIKE 'memory:%' AND json_valid(m.metadata) = 1
			ORDER BY r.id`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var content string
			if err := rows.Scan(&content); err != nil {
				t.Fatal(err)
			}
			if q := firstLine(content, 60); q != "" {
				qs = append(qs, q)
			}
		}
		_ = rows.Close()
	}
	rep := changeEvalReport{Mode: "snapshot, no embedder"}
	var latencies []float64
	tokens, chained := 0, 0
	const rounds = 3
	for round := 0; round < rounds; round++ {
		for _, q := range qs {
			resp, ms := changeEvalAsk(t, db, q, "")
			latencies = append(latencies, ms)
			if round == 0 {
				n := packTokens(resp.ContextPack.Text)
				tokens += n
				rep.PackTokensMax = max(rep.PackTokensMax, n)
				for _, m := range resp.Memories {
					if len(m.Replaces) > 0 {
						chained++
					}
				}
			}
		}
	}
	rep.RecallP50, rep.RecallP95 = percentile(latencies, 0.5), percentile(latencies, 0.95)
	if len(qs) > 0 {
		rep.PackTokensAvg = float64(tokens) / float64(len(qs))
	}
	t.Log(rep.table())
	t.Logf("%d queries x %d rounds; %d hits carried replaces", len(qs), rounds, chained)
	writeChangeEvalOut(t, rep)
}
