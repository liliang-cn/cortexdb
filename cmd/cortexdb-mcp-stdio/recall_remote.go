package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// recallHit is the one shape both recall paths reduce to: local search returns
// Go structs, the remote tool returns JSON, and the injected text must be
// identical either way.
type recallHit struct {
	KnowledgeID string `json:"knowledge_id"`
	Title       string `json:"title"`
	Snippet     string `json:"snippet"`
	// Trail is where the hit came from and why it came back, written into
	// the line so the agent can weigh it: see trailOf.
	Trail string `json:"-"`
}

// recallToolName is the shared-brain tool the hook asks. It fuses episodic
// memory, durable knowledge and graph expansion.
//
// It used to be `knowledge_search`, which reads durable knowledge ONLY — so
// everything written with memory_save was invisible to auto-recall, no matter
// how well it matched the prompt. Memories did get injected, they were just
// always knowledge documents, which is the kind of gap you only notice by
// searching for something you saved an hour ago and watching it not come back.
const recallToolName = "knowledge_memory_recall"

// recallSnippetRunes caps how much of a memory is injected. Knowledge hits
// arrive pre-snippetted; memories arrive whole, and some of them are a page
// long. This is a per-prompt hook — it owes the agent a pointer, not the file.
const recallSnippetRunes = 220

// runRecallRemote answers the UserPromptSubmit hook from the shared brain
// instead of a local database file.
//
// Without this, `--recall` returns from main before the CORTEXDB_REMOTE branch
// is ever reached, so a machine using a shared brain still injected memories
// read from its own local file. That failure is silent and looks like working
// software: memories appear, they are just the wrong ones — frozen at whenever
// the machine switched over.
//
// Silent on every failure, like the local path: the hook must never block or
// delay the user's prompt with an error.
func runRecallRemote(addr, token, prompt string, situation recallSituation, topK int) string {
	conn, err := dialCortexDB(addr, token)
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	request := map[string]any{
		"query":           situation.query(prompt),
		"keywords":        situation.keywords(prompt),
		"top_k_memories":  topK,
		"top_k_knowledge": topK,
		// Graph expansion earns its keep on relational prompts ("who uses X"),
		// but this runs before every single message: light traversal only.
		"graph_light": true,
		"planner":     "keywords: shell hook",
	}
	if situation.Project != "" {
		request["entity_names"] = []string{situation.Project}
	}
	args, err := json.Marshal(request)
	if err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), remoteDialTimeout)
	defer cancel()
	// An id the brain's trace record will carry, so a recall that fails here
	// — the hook stays silent on stdout and says why on stderr — can be
	// matched to what the brain did with it.
	ctx, call := observability.WithCall(ctx, "")
	call.SetClient(cortexdb.LocalClientInfo())

	resp, err := rpcv1.NewToolsServiceClient(conn).CallTool(ctx, &rpcv1.CallToolRequest{
		Name:     recallToolName,
		ArgsJson: string(args),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb recall: %s failed (request_id %s): %v\n", recallToolName, call.ID(), err)
		return ""
	}

	hits, err := parseRecallPayload(resp.GetResultJson())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb recall: %v (request_id %s, received %s)\n", err, call.ID(), describeReply(resp.GetResultJson()))
		return ""
	}
	return formatRecallHits(hits)
}

// describeReply is what a reply that could not be read was: its size and its
// first bytes, which say more than a decoder's complaint about them.
func describeReply(body string) string {
	head := strings.Join(strings.Fields(body), " ")
	if r := []rune(head); len(r) > 200 {
		head = string(r[:200]) + "…"
	}
	return fmt.Sprintf("%d bytes: %q", len(body), head)
}

// parseRecallPayload reads hits out of the tool's JSON answer. Anything that is
// not a search result — an error object, a truncated body, a future response
// shape — yields no hits rather than an injected mess.
//
// `results` is still read: a brain running an older server answers
// knowledge-only, and half a recall beats none.
func parseRecallPayload(body string) ([]recallHit, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, nil
	}
	var payload struct {
		cortexdb.KnowledgeMemoryRecallResponse
		Results []cortexdb.KnowledgeSearchHit `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, fmt.Errorf("recall payload is not search results: %w", err)
	}
	if len(payload.Memories) == 0 && len(payload.Knowledge) == 0 {
		payload.Knowledge = payload.Results
	}
	return hitsFromRecall(&payload.KnowledgeMemoryRecallResponse), nil
}

// hitsFromRecall turns a recall answer into injectable hits, each with its
// trail. Memories first: they are the layer that changes, and the one a stale
// answer hurts most.
func hitsFromRecall(resp *cortexdb.KnowledgeMemoryRecallResponse) []recallHit {
	hits := make([]recallHit, 0, len(resp.Memories)+len(resp.Knowledge))
	for _, m := range resp.Memories {
		meta := m.Memory.Metadata
		date := metaString(meta, "date")
		if date == "" && !m.Memory.CreatedAt.IsZero() {
			date = m.Memory.CreatedAt.UTC().Format("2006-01-02")
		}
		hits = append(hits, recallHit{
			KnowledgeID: m.Memory.ID,
			Snippet:     snippetOf(m.Memory.Content),
			Trail: trailOf(hitOrigin{
				Date:    date,
				Writer:  metaString(meta, "written_by"),
				Source:  metaString(meta, "source"),
				Model:   metaString(meta, "model"),
				Agent:   metaString(meta, "agent"),
				Host:    metaString(meta, "host"),
				Project: metaString(meta, "project"),
				Session: metaString(meta, "session"),
				Grade:   m.Memory.Grade, GradeWhy: m.Memory.GradeWhy,
			}, memoryVia(m, resp.MemoryDecision.EffectiveMode)),
		})
	}
	for _, k := range resp.Knowledge {
		meta := k.Metadata
		date := firstRunes(firstNonEmptyStr(meta["updated"], meta["date"]), 10)
		hits = append(hits, recallHit{
			KnowledgeID: k.KnowledgeID,
			Title:       k.Title,
			Snippet:     k.Snippet,
			Trail: trailOf(hitOrigin{
				Date:    date,
				Writer:  meta["written_by"],
				Source:  firstNonEmptyStr(k.Author, meta["source"]),
				Agent:   meta["agent"],
				Host:    meta["host"],
				Project: meta["project"],
				Session: meta["session"],
				URL:     k.SourceURL,
				Grade:   strings.ToLower(strings.TrimSpace(meta[cortexdb.KeyGrade])), GradeWhy: meta[cortexdb.KeyWhy],
			}, knowledgeVia(k, resp.KnowledgeDecision.EffectiveMode)),
		})
	}
	return hits
}

// hitOrigin is what a hit's writer left on it.
type hitOrigin struct {
	Date, Writer, Source, Model, Agent, Host, Project, Session, URL string
	// Grade and GradeWhy are how the record says it is known.
	Grade, GradeWhy string
}

// gradeMark is what a trail says first about how a hit is known: a guess
// ("UNVERIFIED (asserted)") before anything else, so an agent does not act
// on it as if it were measured, and the check behind a verified hit. An
// untagged hit says nothing. The cortexdb-live mod writes the same words.
func gradeMark(grade, why string) string {
	switch grade {
	case cortexdb.GradeAsserted, cortexdb.GradeHeld:
		return "UNVERIFIED (" + grade + ")"
	case cortexdb.GradeVerified:
		if why = strings.TrimSpace(why); why != "" {
			return "verified: " + firstRunes(why, 80)
		}
		return "verified"
	case "":
		return ""
	}
	return grade
}

// sharedKey is the one token every client of an unkeyed brain speaks with: it
// names nobody in particular, so a trail leaves it out.
const sharedKey = "legacy-token"

// trailOf is a hit's bracket: the date, who wrote it, from which machine,
// project and session, and the score of the retriever that found it. The
// cortexdb-live mod writes the same words (hooks/register.tsx, trailOf).
func trailOf(o hitOrigin, why string) string {
	var by []string
	for _, part := range []string{o.Agent, o.Source} {
		if part != "" {
			by = append(by, part)
		}
	}
	if o.Model != "" {
		by = append(by, "("+o.Model+")")
	}
	parts := []string{gradeMark(o.Grade, o.GradeWhy), o.Date, strings.Join(by, " ")}
	if o.Writer != "" && o.Writer != sharedKey {
		parts = append(parts, "key "+o.Writer)
	}
	if o.Host != "" {
		parts = append(parts, "host "+o.Host)
	}
	if o.Project != "" {
		parts = append(parts, "project "+o.Project)
	}
	if o.Session != "" {
		parts = append(parts, "session "+firstRunes(o.Session, 8))
	}
	parts = append(parts, o.URL, why)
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

// memoryVia names the retrievers that found a memory, with the raw similarity
// when meaning was one of them. The hit's score is that similarity after
// importance and recency boosts, so it is never shown as one. A brain before
// v2.134 names neither: its decision says which mode ran.
func memoryVia(m cortexdb.MemorySearchHit, mode string) string {
	has := func(v string) bool { return slices.Contains(m.Via, v) }
	similarity := ""
	if m.Similarity > 0 {
		similarity = " " + strconv.FormatFloat(m.Similarity, 'f', 2, 64)
	}
	switch {
	case has("semantic") && has("keyword"):
		return "semantic" + similarity + " + keyword"
	case has("semantic"):
		return "semantic" + similarity
	case has("graph"):
		return "graph"
	case has("keyword"):
		return "keyword"
	case mode == cortexdb.RetrievalModeGraph:
		return "graph"
	case mode == cortexdb.RetrievalModeLexical:
		return "keyword"
	}
	return "semantic"
}

// knowledgeVia names the retriever that found a knowledge hit, with the score
// each gave it. The fused score beside them only orders the list.
func knowledgeVia(k cortexdb.KnowledgeSearchHit, mode string) string {
	n := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	switch {
	case k.VectorScore > 0 && k.LexicalScore > 0:
		return "semantic " + n(k.VectorScore) + " + keyword " + n(k.LexicalScore)
	case k.VectorScore > 0:
		return "semantic " + n(k.VectorScore)
	case k.LexicalScore > 0:
		return "keyword " + n(k.LexicalScore)
	case mode == cortexdb.RetrievalModeGraph:
		return "graph"
	case mode == cortexdb.RetrievalModeLexical:
		return "keyword"
	}
	return "semantic"
}

func metaString(meta map[string]any, key string) string {
	s, _ := meta[key].(string)
	return strings.TrimSpace(s)
}

func firstRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// snippetOf flattens a memory into one injectable line: whitespace collapsed
// (memories are written as paragraphs) and cut to a fixed rune budget.
func snippetOf(content string) string {
	flat := strings.Join(strings.Fields(content), " ")
	runes := []rune(flat)
	if len(runes) <= recallSnippetRunes {
		return flat
	}
	return strings.TrimSpace(string(runes[:recallSnippetRunes])) + "…"
}

// formatRecallHits renders hits as the additionalContext block. Shared by both
// paths so the text an agent sees never depends on where the brain lives.
func formatRecallHits(hits []recallHit) string {
	var lines []string
	for _, hit := range hits {
		snippet := strings.TrimSpace(hit.Snippet)
		if snippet == "" {
			continue
		}
		title := hit.Title
		if title == "" {
			title = hit.KnowledgeID
		}
		if hit.Trail != "" {
			title += " [" + hit.Trail + "]"
		}
		lines = append(lines, "- "+title+": "+snippet)
	}
	if len(lines) == 0 {
		return ""
	}
	// Nudge the save side too: recall keeps the brain useful only if new durable
	// facts also get written back.
	return fitHookContext(
		"Relevant CortexDB memories for this prompt (retrieved automatically — verify before relying on them):",
		lines,
		"(If this exchange states a durable preference, decision, or fact, save it with memory_save / knowledge_save.)",
	)
}
