package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// longMemEvalEntry is one question of a LongMemEval file (longmemeval_s,
// longmemeval_m, longmemeval_oracle and their _cleaned releases share it). The
// answer is left out: it is a string for most questions and a number for some,
// and retrieval scoring never reads it.
type longMemEvalEntry struct {
	QuestionID         string              `json:"question_id"`
	QuestionType       string              `json:"question_type"`
	Question           string              `json:"question"`
	AnswerSessionIDs   []string            `json:"answer_session_ids"`
	HaystackDates      []string            `json:"haystack_dates"`
	HaystackSessionIDs []string            `json:"haystack_session_ids"`
	HaystackSessions   [][]longMemEvalTurn `json:"haystack_sessions"`
}

type longMemEvalTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// LongMemEvalProtocol is what a LongMemEval number from this harness means.
const LongMemEvalProtocol = "per question, index only that question's haystack sessions (one document per session); " +
	"query with the question; relevant = answer_session_ids; abstention questions (_abs) are skipped as in the official retrieval evaluation"

// LoadLongMemEval reads a LongMemEval JSON file into one corpus per question:
// the question's own haystack sessions are its documents and its
// answer_session_ids are what counts as relevant.
//
// Each session becomes one document because session-level recall is the metric
// the benchmark reports; how a session is chunked is the retriever's business.
// The file is decoded one question at a time, since longmemeval_s is 277 MB and
// the _m variant ten times that.
//
// Abstention questions (ids ending in _abs) are skipped, as the official
// retrieval evaluation skips them: they ask about something the history never
// states, so the right retrieval is nothing and recall has no meaning.
func LoadLongMemEval(r io.Reader) (*Suite, error) {
	s := &Suite{Name: "longmemeval", Protocol: LongMemEvalProtocol}
	dec := json.NewDecoder(r)
	if tok, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("eval: longmemeval: %w", err)
	} else if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("eval: longmemeval: want a JSON array, got %v", tok)
	}
	for dec.More() {
		var e longMemEvalEntry
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("eval: longmemeval: %w", err)
		}
		if strings.HasSuffix(e.QuestionID, "_abs") {
			s.skip("abstention")
			continue
		}
		c, err := longMemEvalCorpus(e)
		if err != nil {
			return nil, err
		}
		s.Corpora = append(s.Corpora, c)
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("eval: longmemeval: %w", err)
	}
	return s, nil
}

func longMemEvalCorpus(e longMemEvalEntry) (Corpus, error) {
	if e.QuestionID == "" {
		return Corpus{}, fmt.Errorf("eval: longmemeval: question with empty id")
	}
	if len(e.HaystackSessionIDs) != len(e.HaystackSessions) {
		return Corpus{}, fmt.Errorf("eval: longmemeval %s: %d session ids for %d sessions",
			e.QuestionID, len(e.HaystackSessionIDs), len(e.HaystackSessions))
	}
	c := Corpus{ID: e.QuestionID}
	seen := make(map[string]struct{}, len(e.HaystackSessionIDs))
	for i, id := range e.HaystackSessionIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		var b strings.Builder
		for _, t := range e.HaystackSessions[i] {
			if strings.TrimSpace(t.Content) == "" {
				continue
			}
			fmt.Fprintf(&b, "%s: %s\n", t.Role, t.Content)
		}
		if b.Len() == 0 {
			continue
		}
		seen[id] = struct{}{}
		title := "Conversation"
		if i < len(e.HaystackDates) && e.HaystackDates[i] != "" {
			title = "Conversation on " + e.HaystackDates[i]
		}
		c.Documents = append(c.Documents, Document{ID: id, Title: title, Content: strings.TrimRight(b.String(), "\n")})
	}
	var relevant []string
	for _, id := range e.AnswerSessionIDs {
		if _, ok := seen[id]; ok {
			relevant = append(relevant, id)
		}
	}
	if len(relevant) == 0 {
		return Corpus{}, fmt.Errorf("eval: longmemeval %s: no answer session is in the haystack", e.QuestionID)
	}
	c.Queries = []Query{{ID: e.QuestionID, Text: e.Question, Relevant: relevant, Category: e.QuestionType}}
	return c, nil
}
