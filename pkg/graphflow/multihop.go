package graphflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Multi-hop / iterative agentic retrieval — the retrieve → reason → retrieve
// loop. Complex questions often can't be answered from a single retrieval:
// the first hop surfaces a fact that reveals what to look up next ("who leads
// the team that owns X?" needs X's owning team, then that team's lead). Each
// hop runs a GraphRAG search, accumulates the evidence, and asks the LLM
// whether the evidence is now sufficient — if not, it hands back a refined
// follow-up query to drive the next hop. The loop is bounded and guards against
// repeating a query, so a confused model can never spin forever.

const (
	defaultMultiHopMaxHops    = 4
	defaultMultiHopTopKPerHop = 5
	// multiHopEvidenceCharBudget bounds how much accumulated evidence is sent
	// to the LLM on each decision/synthesis call, matching community.go's
	// char-budget discipline so a large brain never overflows a small model.
	multiHopEvidenceCharBudget = 6000
)

// MultiHopOptions configures MultiHopSearch.
type MultiHopOptions struct {
	LLM        JSONGenerator // required: decides sufficiency and emits the next query
	MaxHops    int           // cap retrieval iterations (default 4)
	TopKPerHop int           // knowledge hits pulled per hop (default 5)
}

// MultiHopStep records one retrieval hop for transparency: the query that drove
// it and the snippets it contributed to the evidence set.
type MultiHopStep struct {
	Query    string   `json:"query"`
	Snippets []string `json:"snippets,omitempty"`
}

// MultiHopResult is the answer to a multi-hop question, with the full hop trace.
type MultiHopResult struct {
	Query  string         `json:"query"`
	Answer string         `json:"answer"`
	Hops   int            `json:"hops"`
	Steps  []MultiHopStep `json:"steps,omitempty"`
}

// MultiHopSearch answers a complex question by iterating retrieve → reason →
// retrieve. Starting from the original question, each hop runs a GraphRAG
// search (auto retrieval mode, graph-light), folds its snippets into a deduped
// evidence set, and asks the LLM whether the evidence now answers the question.
// When the LLM says "enough" (or hands back no next query, or MaxHops is
// reached, or it repeats an earlier query), the loop stops and the answer is
// emitted — either the LLM's own answer or, if it left that blank, a final
// reduce call over all evidence. Best-effort by design: an LLM/parse failure on
// a hop stops the loop and answers from evidence gathered so far rather than
// hard-failing, unless there is neither evidence nor an answer to return.
func MultiHopSearch(ctx context.Context, db *cortexdb.DB, query string, opts MultiHopOptions) (*MultiHopResult, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: multi-hop: nil db")
	}
	if opts.LLM == nil {
		return nil, fmt.Errorf("graphflow: multi-hop search requires an LLM")
	}
	original := strings.TrimSpace(query)
	if original == "" {
		return nil, fmt.Errorf("graphflow: multi-hop: empty query")
	}
	maxHops := opts.MaxHops
	if maxHops <= 0 {
		maxHops = defaultMultiHopMaxHops
	}
	topK := opts.TopKPerHop
	if topK <= 0 {
		topK = defaultMultiHopTopKPerHop
	}

	result := &MultiHopResult{Query: original}
	evidence := make([]string, 0)          // accumulated, deduped snippets
	seenSnippet := make(map[string]struct{})
	seenQuery := make(map[string]struct{}) // guards against a looping model
	answer := ""

	currentQuery := original
	for hop := 0; hop < maxHops; hop++ {
		seenQuery[strings.ToLower(strings.TrimSpace(currentQuery))] = struct{}{}

		// Retrieve for this hop and fold new snippets into the evidence set.
		hopSnippets := retrieveHopSnippets(ctx, db, currentQuery, topK)
		newForStep := make([]string, 0, len(hopSnippets))
		for _, s := range hopSnippets {
			key := strings.ToLower(strings.TrimSpace(s))
			if key == "" {
				continue
			}
			if _, ok := seenSnippet[key]; ok {
				continue
			}
			seenSnippet[key] = struct{}{}
			evidence = append(evidence, s)
			newForStep = append(newForStep, s)
		}
		result.Steps = append(result.Steps, MultiHopStep{Query: currentQuery, Snippets: newForStep})
		result.Hops = hop + 1

		// Reason: ask the LLM whether the evidence now answers the question and,
		// if not, what to retrieve next. A failure here is non-fatal — we stop
		// and synthesize from whatever evidence we already have.
		decision, err := decideMultiHop(ctx, opts.LLM, original, evidence)
		if err != nil {
			break
		}
		if strings.TrimSpace(decision.Answer) != "" {
			answer = decision.Answer
		}
		next := strings.TrimSpace(decision.NextQuery)
		if decision.Enough || next == "" {
			break
		}
		// Loop guard: a repeated query means the model is stuck — stop.
		if _, ok := seenQuery[strings.ToLower(next)]; ok {
			break
		}
		currentQuery = next
	}

	// Emit the final answer: prefer the LLM's own answer; otherwise run one
	// reduce call over all accumulated evidence.
	if strings.TrimSpace(answer) == "" {
		if len(evidence) == 0 {
			result.Answer = ""
			return result, nil
		}
		synth, err := reduceMultiHop(ctx, opts.LLM, original, evidence)
		if err != nil {
			// Nothing usable to synthesize with and no answer — surface it.
			return result, fmt.Errorf("graphflow: multi-hop: could not synthesize an answer: %w", err)
		}
		answer = synth
	}
	result.Answer = answer
	return result, nil
}

// retrieveHopSnippets runs one GraphRAG search and returns its hit snippets
// (falling back to titles when a hit has no snippet). Retrieval failures yield
// no snippets — the caller treats an empty hop as merely uninformative.
func retrieveHopSnippets(ctx context.Context, db *cortexdb.DB, query string, topK int) []string {
	resp, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
		Query:         query,
		TopK:          topK,
		RetrievalMode: cortexdb.RetrievalModeAuto,
		GraphLight:    true,
	})
	if err != nil || resp == nil {
		return nil
	}
	out := make([]string, 0, len(resp.Results))
	for _, hit := range resp.Results {
		text := strings.TrimSpace(hit.Snippet)
		if text == "" {
			text = strings.TrimSpace(hit.Title)
		}
		if text == "" {
			continue
		}
		if title := strings.TrimSpace(hit.Title); title != "" && text != title {
			out = append(out, title+": "+text)
		} else {
			out = append(out, text)
		}
	}
	return out
}

// --- LLM steps ---

const multiHopDecideSystemPrompt = "You are running an iterative research loop to answer a question. " +
	"Given the ORIGINAL question and the EVIDENCE gathered so far, decide whether the evidence is enough to answer it fully. " +
	"If it is enough, set enough=true and write the final answer. " +
	"If it is not, set enough=false and give ONE focused follow-up search query that would retrieve the missing piece " +
	"(a different angle or the next entity in the chain — do not repeat an earlier query). " +
	"Return JSON only: {\"enough\":true|false,\"answer\":\"final answer or empty\",\"next_query\":\"follow-up query or empty\",\"reasoning\":\"short why\"}."

type multiHopDecision struct {
	Enough    bool   `json:"enough"`
	Answer    string `json:"answer"`
	NextQuery string `json:"next_query"`
	Reasoning string `json:"reasoning"`
}

func decideMultiHop(ctx context.Context, llm JSONGenerator, question string, evidence []string) (*multiHopDecision, error) {
	user := "ORIGINAL question: " + question + "\n\nEVIDENCE so far:\n" + renderMultiHopEvidence(evidence) +
		"\n\nDecide if this is enough and respond as specified. JSON only."
	raw, err := llm.GenerateJSON(ctx, multiHopDecideSystemPrompt, user)
	if err != nil {
		return nil, err
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		return nil, err
	}
	var parsed multiHopDecision
	if err := json.Unmarshal(obj, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

const multiHopReduceSystemPrompt = "You answer the user's question using only the provided evidence gathered across several retrieval hops. " +
	"Synthesize a clear, well-structured answer; do not invent facts beyond the evidence. " +
	"Return JSON only: {\"answer\":\"…\"}."

func reduceMultiHop(ctx context.Context, llm JSONGenerator, question string, evidence []string) (string, error) {
	user := "Question: " + question + "\n\nEvidence:\n" + renderMultiHopEvidence(evidence) +
		"\n\nWrite the final answer. JSON only."
	raw, err := llm.GenerateJSON(ctx, multiHopReduceSystemPrompt, user)
	if err != nil {
		return "", err
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(obj, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Answer) == "" {
		return "", fmt.Errorf("empty multi-hop answer")
	}
	return parsed.Answer, nil
}

// renderMultiHopEvidence formats the evidence set as a char-bounded bulleted
// list so the prompt stays inside a small model's context window.
func renderMultiHopEvidence(evidence []string) string {
	if len(evidence) == 0 {
		return "(none yet)"
	}
	var b strings.Builder
	for _, e := range evidence {
		line := "- " + strings.TrimSpace(e) + "\n"
		if b.Len() > 0 && b.Len()+len(line) > multiHopEvidenceCharBudget {
			break
		}
		b.WriteString(line)
	}
	return strings.TrimRight(b.String(), "\n")
}
