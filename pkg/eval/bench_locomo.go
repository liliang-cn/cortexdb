package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// LoCoMoProtocol is what a LoCoMo number from this harness means.
const LoCoMoProtocol = "per conversation, index every dialog turn as one document; query with each QA question; " +
	"relevant = evidence dialog ids (dialog level) and their sessions (session level); category 5 (adversarial) is skipped"

// locomoSample is one conversation of locomo10.json. The conversation object
// mixes speaker names, session_N turn arrays and session_N_date_time strings
// under one map, so it is decoded raw and picked apart by key.
type locomoSample struct {
	SampleID     string                     `json:"sample_id"`
	Conversation map[string]json.RawMessage `json:"conversation"`
	QA           []locomoQA                 `json:"qa"`
}

type locomoTurn struct {
	Speaker     string `json:"speaker"`
	DiaID       string `json:"dia_id"`
	Text        string `json:"text"`
	BlipCaption string `json:"blip_caption"`
}

// locomoQA leaves the answer out: it is a string, a number or absent depending
// on the category, and retrieval scoring never reads it.
type locomoQA struct {
	Question string   `json:"question"`
	Evidence []string `json:"evidence"`
	Category int      `json:"category"`
}

var (
	locomoSessionKey = regexp.MustCompile(`^session_(\d+)$`)
	// locomoDiaRef finds dialog references inside an evidence string. Most
	// strings are one clean "D3:5", but a few hold several ("D8:6; D9:17",
	// "D9:1 D4:4 D4:6") and two are malformed ("D", "D:11:26"), so references
	// are found rather than assumed, and a string with none adds nothing.
	locomoDiaRef = regexp.MustCompile(`D(\d+):(\d+)`)
)

// canonicalDiaID rewrites a dialog reference as D<session>:<turn> without
// zero padding, so evidence and turn ids written differently still match.
func canonicalDiaID(session, turn string) string {
	s, _ := strconv.Atoi(session)
	t, _ := strconv.Atoi(turn)
	return fmt.Sprintf("D%d:%d", s, t)
}

// LoCoMoSessionOf returns the session group of a LoCoMo dialog id ("D3:5" is in
// "session_3"), or "" when the id is not a dialog reference.
func LoCoMoSessionOf(diaID string) string {
	m := locomoDiaRef.FindStringSubmatch(diaID)
	if m == nil {
		return ""
	}
	n, _ := strconv.Atoi(m[1])
	return "session_" + strconv.Itoa(n)
}

// LoadLoCoMo reads locomo10.json into one corpus per conversation, with every
// dialog turn a document and every QA question a query whose relevant ids are
// its evidence turns. Each turn carries its session as Group, so the same
// ranking also scores at session level.
//
// A turn is indexed as "[date] Speaker: text", plus the caption of the image
// when the speaker shared one, because that is what an agent that saved the
// turn would have: the date for questions about when, the caption because
// some questions are answered only by the picture.
//
// Category 5 is skipped. Those questions are adversarial — they ask about
// something the conversation never says — and evaluations of LoCoMo leave them
// out of retrieval scoring for that reason. Questions whose evidence names no
// turn present in the conversation are skipped too, and counted.
func LoadLoCoMo(r io.Reader) (*Suite, error) {
	var samples []locomoSample
	if err := json.NewDecoder(r).Decode(&samples); err != nil {
		return nil, fmt.Errorf("eval: locomo: %w", err)
	}
	s := &Suite{Name: "locomo", Protocol: LoCoMoProtocol}
	for _, sample := range samples {
		c, err := locomoCorpus(s, sample)
		if err != nil {
			return nil, err
		}
		if len(c.Queries) > 0 {
			s.Corpora = append(s.Corpora, c)
		}
	}
	return s, nil
}

func locomoCorpus(s *Suite, sample locomoSample) (Corpus, error) {
	if sample.SampleID == "" {
		return Corpus{}, fmt.Errorf("eval: locomo: conversation with empty sample_id")
	}
	c := Corpus{ID: sample.SampleID}

	var sessions []int
	for key := range sample.Conversation {
		if m := locomoSessionKey.FindStringSubmatch(key); m != nil {
			n, _ := strconv.Atoi(m[1])
			sessions = append(sessions, n)
		}
	}
	sort.Ints(sessions)

	docs := make(map[string]struct{})
	for _, n := range sessions {
		key := "session_" + strconv.Itoa(n)
		var turns []locomoTurn
		if err := json.Unmarshal(sample.Conversation[key], &turns); err != nil {
			return Corpus{}, fmt.Errorf("eval: locomo %s %s: %w", sample.SampleID, key, err)
		}
		var date string
		if raw, ok := sample.Conversation[key+"_date_time"]; ok {
			_ = json.Unmarshal(raw, &date)
		}
		for _, t := range turns {
			m := locomoDiaRef.FindStringSubmatch(t.DiaID)
			if m == nil || strings.TrimSpace(t.Text) == "" {
				continue
			}
			id := canonicalDiaID(m[1], m[2])
			if _, dup := docs[id]; dup {
				continue
			}
			docs[id] = struct{}{}
			var b strings.Builder
			if date != "" {
				fmt.Fprintf(&b, "[%s] ", date)
			}
			fmt.Fprintf(&b, "%s: %s", t.Speaker, t.Text)
			if t.BlipCaption != "" {
				fmt.Fprintf(&b, " (shared an image: %s)", t.BlipCaption)
			}
			c.Documents = append(c.Documents, Document{ID: id, Content: b.String(), Group: key})
		}
	}

	for i, qa := range sample.QA {
		if qa.Category == 5 {
			s.skip("adversarial (category 5)")
			continue
		}
		var relevant []string
		seen := make(map[string]struct{})
		for _, ev := range qa.Evidence {
			for _, m := range locomoDiaRef.FindAllStringSubmatch(ev, -1) {
				id := canonicalDiaID(m[1], m[2])
				if _, ok := docs[id]; !ok {
					continue
				}
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
				relevant = append(relevant, id)
			}
		}
		if len(relevant) == 0 {
			s.skip("no evidence turn in conversation")
			continue
		}
		c.Queries = append(c.Queries, Query{
			ID:       fmt.Sprintf("%s/q%d", sample.SampleID, i),
			Text:     qa.Question,
			Relevant: relevant,
			Category: "category-" + strconv.Itoa(qa.Category),
		})
	}
	return c, nil
}
