package cortexdb

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// A question is cut at the words it is made of and kept at the words it is
// about. 为什么, 从来 and 给我 carry nothing a note would contain.
func TestFunctionWordsAreCutAndContentWordsKept(t *testing.T) {
	q := cjkQueryTerms("微信机器人为什么从来不主动给我发提醒")
	for _, want := range []string{"微信", "机器", "主动", "提醒"} {
		if !slices.Contains(q.terms, want) {
			t.Errorf("terms %v lack %q", q.terms, want)
		}
	}
	for _, unwanted := range []string{"什么", "为什", "从来", "给我"} {
		if slices.Contains(q.terms, unwanted) {
			t.Errorf("terms %v keep the function word %q", q.terms, unwanted)
		}
	}
	// 微信机器人 + 主动 + 发提醒: the characters a note could share.
	if q.chars != 10 {
		t.Errorf("content characters = %d, want 10", q.chars)
	}

	if q := cjkQueryTerms("为什么是这样呢"); len(q.terms) != 0 {
		t.Errorf("a question made only of function words produced terms %v", q.terms)
	}
	if q := cjkQueryTerms("recommend a good novel"); len(q.terms) != 0 {
		t.Errorf("text with no CJK in it produced terms %v; the word index handles it", q.terms)
	}
}

// 上 is deliberately not a function word: removing it breaks 上海 into 海.
func TestAPlaceNameSurvivesTheFunctionWordList(t *testing.T) {
	q := cjkQueryTerms("明天上海天气怎么样")
	if !slices.Contains(q.terms, "上海") {
		t.Fatalf("terms %v lost 上海", q.terms)
	}
}

func scoredIDs(q cjkQuery, docs map[string]string, order []string) []string {
	contents := make([]string, len(order))
	for i, id := range order {
		contents[i] = docs[id]
	}
	var out []string
	for _, s := range scoreCJKCandidates(q, contents, len(contents), 40) {
		out = append(out, order[s.index])
	}
	return out
}

// One word shared with a question of several is not an answer. Every negative
// in the golden set that matched anything matched exactly one: 北京, 推荐, 学会,
// and 周末去 — a word plus the commonest verb there is.
func TestOneSharedWordIsNotAnAnswer(t *testing.T) {
	docs := map[string]string{
		"beijing": "北京机房的专线本周迁移，迁移期间延迟会升高。",
		"coffee":  "周末去了一家新开的咖啡店，老板推荐了一款豆子。",
		"guitar":  "学会用 git bisect 以后，定位回归快了很多。",
	}
	order := []string{"beijing", "coffee", "guitar"}
	for _, query := range []string{"北京明天会下雨吗", "推荐几部好看的科幻电影", "如何学会弹吉他", "周末去哪里爬山"} {
		if got := scoredIDs(cjkQueryTerms(query), docs, order); len(got) != 0 {
			t.Errorf("%s returned %v; one shared word is not evidence of an answer", query, got)
		}
	}
}

// The note that answers a question in other words is found, even though the
// question's bigrams include some that straddle word boundaries (信机, 发提)
// and appear nowhere: coverage is counted in characters, and 信 and 机 are
// covered by 微信 and 机器.
func TestAnAnswerInOtherWordsIsFound(t *testing.T) {
	docs := map[string]string{
		"wechat": "微信机器人只能在用户先发消息后回复，没有主动推送能力，所以定时提醒发不出去。",
		"other":  "日志文件没有轮转，单个文件超过五十个吉字节。",
	}
	got := scoredIDs(cjkQueryTerms("微信机器人为什么从来不主动给我发提醒"), docs, []string{"wechat", "other"})
	if len(got) != 1 || got[0] != "wechat" {
		t.Fatalf("got %v, want only the note about the WeChat bot", got)
	}
}

// A question that is one two-character word is answered by that word. The
// "more than one word" rule is about questions of several; a query that is
// only 风控 has nothing else to match.
func TestATwoCharacterQueryIsAnsweredByTheWordItIs(t *testing.T) {
	docs := map[string]string{"risk": "风控规则每周复盘一次。", "other": "日志按天切割。"}
	got := scoredIDs(cjkQueryTerms("风控"), docs, []string{"risk", "other"})
	if len(got) != 1 || got[0] != "risk" {
		t.Fatalf("got %v, want the note about 风控", got)
	}
}

// Memory recall, on both backends: a Chinese sentence finds the memory that
// answers it, and a question the store knows nothing about finds nothing.
func TestAChineseSentenceFindsTheMemoryThatAnswersIt(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			memories := map[string]string{
				"wechat-push": "微信机器人只能在用户先发消息后的一段时间内回复，没有主动推送的能力，所以定时提醒一直发不出去，后来改到 Telegram 发送。",
				"ws-fragment": "浏览器通过 WebSocket 发送大消息时会被分成多个帧，服务端只读了第一帧，所以只收到一半内容；修复是在服务端做分片重组。",
				"coffee":      "周末去了一家新开的咖啡店，手冲很好喝，老板推荐了一款埃塞俄比亚的豆子。",
				"log-rotate":  "日志文件没有轮转，单个文件超过五十个吉字节，改成按天切割并保留七天。",
			}
			for id, content := range memories {
				if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Scope: MemoryScopeGlobal, Content: content}); err != nil {
					t.Fatalf("save %s: %v", id, err)
				}
			}

			for query, want := range map[string]string{
				"微信机器人为什么从来不主动给我发提醒": "wechat-push",
				"浏览器发大消息只收到一半是什么问题":  "ws-fragment",
			} {
				res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: query, Scope: MemoryScopeGlobal, TopK: 3})
				if err != nil {
					t.Fatalf("search %s: %v", query, err)
				}
				if len(res.Results) == 0 || res.Results[0].Memory.ID != want {
					t.Errorf("%s: got %v, want %s first", query, memoryIDs(res.Results), want)
				}
			}

			for _, query := range []string{"推荐几部好看的科幻电影", "周末去哪里爬山"} {
				res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: query, Scope: MemoryScopeGlobal, TopK: 3})
				if err != nil {
					t.Fatalf("search %s: %v", query, err)
				}
				if len(res.Results) != 0 {
					t.Errorf("%s returned %v; nothing stored is about it", query, memoryIDs(res.Results))
				}
			}
		})
	}
}

func memoryIDs(hits []MemorySearchHit) []string {
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.Memory.ID
	}
	return ids
}

// Knowledge recall reaches the same path from all three doors a caller uses:
// SearchKnowledge, SearchTextOnly with planner keywords, and the search_text
// tool an agent calls.
func TestAChineseSentenceFindsKnowledgeThroughEveryDoor(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			docs := map[string]string{
				"restart-failover": "在主节点上直接执行 systemctl restart 会让高可用管理器认为资源故障，从而触发一次计划外的故障转移；升级时应该先驱逐资源再替换。",
				"tls-expire":       "证书过期导致所有请求握手失败，现在用自动续期脚本并在到期前十四天告警。",
				"coffee":           "周末去了一家新开的咖啡店，手冲很好喝，老板推荐了一款埃塞俄比亚的豆子。",
			}
			for id, content := range docs {
				if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Title: id, Content: content}); err != nil {
					t.Fatalf("save %s: %v", id, err)
				}
			}
			const query, want = "能不能直接重启主节点上的服务", "restart-failover"

			kr, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: query, RetrievalMode: RetrievalModeLexical, TopK: 3})
			if err != nil {
				t.Fatalf("SearchKnowledge: %v", err)
			}
			if len(kr.Results) == 0 || kr.Results[0].KnowledgeID != want {
				t.Errorf("SearchKnowledge: got %+v, want %s first", kr.Results, want)
			}

			tr, err := db.SearchTextOnly(ctx, query, TextSearchOptions{TopK: 3, Keywords: []string{"重启"}})
			if err != nil {
				t.Fatalf("SearchTextOnly: %v", err)
			}
			if len(tr) == 0 || tr[0].DocID != want {
				t.Errorf("SearchTextOnly with keywords: got %v, want %s first", docIDs(tr), want)
			}

			args, _ := json.Marshal(ToolSearchTextRequest{Query: query, TopK: 3, RetrievalMode: RetrievalModeLexical})
			raw, err := db.GraphRAGTools().Call(ctx, "search_text", args)
			if err != nil {
				t.Fatalf("search_text: %v", err)
			}
			resp, ok := raw.(*ToolSearchTextResponse)
			if !ok {
				t.Fatalf("search_text returned %T", raw)
			}
			if len(resp.Chunks) == 0 || resp.Chunks[0].DocumentID != want {
				t.Errorf("search_text: got %+v, want %s first", resp.Chunks, want)
			}

			none, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "推荐几部好看的科幻电影", RetrievalMode: RetrievalModeLexical, TopK: 3})
			if err != nil {
				t.Fatalf("negative: %v", err)
			}
			if len(none.Results) != 0 {
				t.Errorf("a question about films returned %+v", none.Results)
			}
		})
	}
}

func docIDs(res []core.ScoredEmbedding) []string {
	ids := make([]string, len(res))
	for i, r := range res {
		ids[i] = r.DocID
	}
	return ids
}

// Rows the bigram path finds go through the same authorization gate as every
// other row. The path is a second way into the chunks table; a second way in
// that skipped the gate would hand a caller text it may not read.
func TestAChineseSearchStillAppliesAuthorization(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
				KnowledgeID: "restricted",
				Title:       "restricted",
				Content:     "在主节点上直接执行 systemctl restart 会触发一次计划外的故障转移。",
			}); err != nil {
				t.Fatalf("save: %v", err)
			}
			query := "能不能直接重启主节点上的服务"

			open, err := db.SearchTextOnly(ctx, query, TextSearchOptions{TopK: 3})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if !slices.Contains(docIDs(open), "restricted") {
				t.Fatalf("without a gate the note must be found, got %v; otherwise this test proves nothing", docIDs(open))
			}

			gated, err := db.SearchTextOnly(ctx, query, TextSearchOptions{
				TopK:      3,
				Authorize: func(e core.ScoredEmbedding) bool { return e.DocID != "restricted" },
			})
			if err != nil {
				t.Fatalf("gated search: %v", err)
			}
			if slices.Contains(docIDs(gated), "restricted") {
				t.Fatalf("the gate refused the note and it was returned anyway: %v", docIDs(gated))
			}
		})
	}
}

// Two words in separate places is more than one word, but a long question can
// share two common ones with a note that answers none of it. The coverage floor
// is what holds that back: 日志 and 报错 are four characters of a question with
// more than twenty.
func TestTwoCommonWordsOfALongQuestionAreNotAnAnswer(t *testing.T) {
	docs := map[string]string{
		"generic": "日志里出现报错时先看时间戳。",
		"answer":  "微信机器人没有主动推送能力，定时提醒发不出去，日志里也不会有报错。",
	}
	q := cjkQueryTerms("微信机器人为什么从来不主动给我发提醒，而且日志里也看不到任何报错信息")
	got := scoredIDs(q, docs, []string{"generic", "answer"})
	if slices.Contains(got, "generic") {
		t.Errorf("got %v; a note sharing only 日志 and 报错 with a long question is not its answer", got)
	}
	if !slices.Contains(got, "answer") {
		t.Errorf("got %v; the note that answers the question must still be found", got)
	}
}
