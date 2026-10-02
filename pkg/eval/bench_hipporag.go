package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// HippoRAGProtocol is what a MuSiQue or 2WikiMultiHopQA number from this
// harness means.
const HippoRAGProtocol = "HippoRAG 2 retrieval protocol: index the released corpus file (the passage pools of all released questions, deduplicated) once; " +
	"query with each question; relevant = its supporting passages; passages are indexed as title + newline + text"

// hippoPassage is one line of a HippoRAG 2 *_corpus.json file.
type hippoPassage struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// musiqueQuestion is one question of HippoRAG 2's musique.json, which keeps
// MuSiQue's own layout: a pool of paragraphs, some flagged as supporting.
type musiqueQuestion struct {
	ID         string `json:"id"`
	Question   string `json:"question"`
	Paragraphs []struct {
		Title        string `json:"title"`
		Text         string `json:"paragraph_text"`
		IsSupporting bool   `json:"is_supporting"`
	} `json:"paragraphs"`
}

// twoWikiQuestion is one question of HippoRAG 2's 2wikimultihopqa.json, which
// keeps the original layout: context is a list of [title, sentences] pairs and
// supporting_facts a list of [title, sentence index] pairs.
type twoWikiQuestion struct {
	ID              string              `json:"_id"`
	Type            string              `json:"type"`
	Question        string              `json:"question"`
	Context         [][]json.RawMessage `json:"context"`
	SupportingFacts [][]json.RawMessage `json:"supporting_facts"`
}

// loadHippoCorpus reads a HippoRAG 2 corpus file into documents keyed by
// content, returning them with a title+text index for resolving gold passages.
func loadHippoCorpus(r io.Reader) ([]Document, map[string]string, error) {
	var passages []hippoPassage
	if err := json.NewDecoder(r).Decode(&passages); err != nil {
		return nil, nil, fmt.Errorf("eval: corpus: %w", err)
	}
	docs := make([]Document, 0, len(passages))
	byKey := make(map[string]string, len(passages))
	for _, p := range passages {
		if strings.TrimSpace(p.Text) == "" {
			continue
		}
		key := p.Title + "\n" + p.Text
		if _, dup := byKey[key]; dup {
			continue
		}
		id := passageID(p.Title, p.Text)
		byKey[key] = id
		// The title goes into the content because HippoRAG 2 indexes it that
		// way, and because for Wikipedia passages the title is often the only
		// place the subject is named: "He was born in 1950" is about the title.
		docs = append(docs, Document{ID: id, Title: p.Title, Content: key})
	}
	return docs, byKey, nil
}

// LoadMuSiQue reads HippoRAG 2's musique.json and musique_corpus.json. A
// question's relevant passages are the paragraphs its pool flags as
// supporting; its category is the hop count from the id prefix ("2hop").
func LoadMuSiQue(questions, corpus io.Reader) (*Suite, error) {
	docs, byKey, err := loadHippoCorpus(corpus)
	if err != nil {
		return nil, fmt.Errorf("eval: musique: %w", err)
	}
	var qs []musiqueQuestion
	if err := json.NewDecoder(questions).Decode(&qs); err != nil {
		return nil, fmt.Errorf("eval: musique: questions: %w", err)
	}
	s := &Suite{Name: "musique", Protocol: HippoRAGProtocol}
	c := Corpus{ID: "musique", Documents: docs}
	for _, q := range qs {
		var relevant []string
		for _, p := range q.Paragraphs {
			if !p.IsSupporting {
				continue
			}
			id, ok := byKey[p.Title+"\n"+p.Text]
			if !ok {
				return nil, fmt.Errorf("eval: musique %s: supporting paragraph %q is not in the corpus", q.ID, p.Title)
			}
			relevant = appendUnique(relevant, id)
		}
		if len(relevant) == 0 {
			s.skip("no supporting passage")
			continue
		}
		c.Queries = append(c.Queries, Query{ID: q.ID, Text: q.Question, Relevant: relevant, Category: musiqueHops.FindString(q.ID)})
	}
	s.Corpora = []Corpus{c}
	return s, nil
}

// musiqueHops reads the hop count off a MuSiQue id. Ids are "2hop__…",
// "3hop1__…", "4hop3__…": the digit after "hop" names a composition shape, and
// the hop count alone is how MuSiQue results are usually broken down.
var musiqueHops = regexp.MustCompile(`^\d+hop`)

// Load2WikiMultiHopQA reads HippoRAG 2's 2wikimultihopqa.json and
// 2wikimultihopqa_corpus.json. A question's relevant passages are the context
// passages whose titles its supporting_facts name; the corpus stores each as
// the passage's sentences joined by single spaces, which is how they are
// matched. The category is the dataset's question type.
func Load2WikiMultiHopQA(questions, corpus io.Reader) (*Suite, error) {
	docs, byKey, err := loadHippoCorpus(corpus)
	if err != nil {
		return nil, fmt.Errorf("eval: 2wikimultihopqa: %w", err)
	}
	var qs []twoWikiQuestion
	if err := json.NewDecoder(questions).Decode(&qs); err != nil {
		return nil, fmt.Errorf("eval: 2wikimultihopqa: questions: %w", err)
	}
	s := &Suite{Name: "2wikimultihopqa", Protocol: HippoRAGProtocol}
	c := Corpus{ID: "2wikimultihopqa", Documents: docs}
	for _, q := range qs {
		supporting := make(map[string]struct{})
		for _, sf := range q.SupportingFacts {
			if len(sf) == 0 {
				continue
			}
			var title string
			if err := json.Unmarshal(sf[0], &title); err != nil {
				return nil, fmt.Errorf("eval: 2wikimultihopqa %s: supporting fact: %w", q.ID, err)
			}
			supporting[title] = struct{}{}
		}
		var relevant []string
		for _, ctx := range q.Context {
			if len(ctx) != 2 {
				return nil, fmt.Errorf("eval: 2wikimultihopqa %s: context entry has %d fields, want 2", q.ID, len(ctx))
			}
			var title string
			var sentences []string
			if err := json.Unmarshal(ctx[0], &title); err != nil {
				return nil, fmt.Errorf("eval: 2wikimultihopqa %s: context title: %w", q.ID, err)
			}
			if _, ok := supporting[title]; !ok {
				continue
			}
			if err := json.Unmarshal(ctx[1], &sentences); err != nil {
				return nil, fmt.Errorf("eval: 2wikimultihopqa %s: context sentences: %w", q.ID, err)
			}
			id, ok := byKey[title+"\n"+strings.Join(sentences, " ")]
			if !ok {
				return nil, fmt.Errorf("eval: 2wikimultihopqa %s: supporting passage %q is not in the corpus", q.ID, title)
			}
			relevant = appendUnique(relevant, id)
		}
		if len(relevant) == 0 {
			s.skip("no supporting passage")
			continue
		}
		c.Queries = append(c.Queries, Query{ID: q.ID, Text: q.Question, Relevant: relevant, Category: q.Type})
	}
	s.Corpora = []Corpus{c}
	return s, nil
}

func appendUnique(ids []string, id string) []string {
	for _, have := range ids {
		if have == id {
			return ids
		}
	}
	return append(ids, id)
}
