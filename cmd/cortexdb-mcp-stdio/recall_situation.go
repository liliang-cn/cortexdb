package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// recallSituation is what a prompt is said against: the project the session
// works in and the start of the last reply. "这个修复前后可以验证吗" names
// nothing by itself; asked bare it recalls whatever other project once fixed
// and verified something. The cortexdb-live mod asks the same way when its
// planner does not answer (hooks/register.tsx, situated).
//
// The two do different jobs. The project is part of the question: it rides in
// the query, the keywords and the entities. The last reply only decides which
// of the hits the prompt found come first (focus). It used to ride in the query
// too, and on a short prompt its 300 runes outweighed the prompt: a screenshot
// with "what happened？" recalled the previous answer's subject, not the
// picture's (#142).
type recallSituation struct {
	Project  string
	Previous string
}

// recallContextRunes is how much of the last reply is read for its terms.
const recallContextRunes = 300

// transcriptTailBytes is how far back from the end of the transcript the last
// reply is looked for. A turn's reply is near the end; reading the whole file
// before every prompt would cost a long session seconds.
const transcriptTailBytes = 512 * 1024

func situationFor(in hookInput) recallSituation {
	var s recallSituation
	if cwd := filepath.Clean(strings.TrimSpace(in.Cwd)); in.Cwd != "" {
		// A session started in the home directory works in no project.
		if home, err := os.UserHomeDir(); err != nil || cwd != filepath.Clean(home) {
			s.Project = filepath.Base(cwd)
		}
	}
	if in.TranscriptPath != "" {
		s.Previous = lastAssistantText(in.TranscriptPath)
	}
	return s
}

// imagePlaceholder is how Claude Code writes an attached image into the prompt
// text: "[Image #1] what happened？".
var imagePlaceholder = regexp.MustCompile(`\[Image #\d+\]`)

// carriesImage reports a prompt that comes with a picture. The hook cannot see
// the picture, and the words beside it are seldom about anything but it, so
// whatever a recall finds for them comes from somewhere else: the previous
// turn, most often. Such a prompt is not recalled for at all.
func carriesImage(prompt string) bool {
	return imagePlaceholder.MatchString(prompt)
}

// query is the prompt with the project after it. The server embeds the query
// alone, so the project has to be in it.
func (s recallSituation) query(prompt string) string {
	if s.Project == "" {
		return prompt
	}
	return prompt + "\n\nProject: " + s.Project
}

func (s recallSituation) keywords(prompt string) []string {
	kw := keywordsFromPrompt(prompt)
	if s.Project == "" {
		return kw
	}
	for _, k := range kw {
		if k == s.Project {
			return kw
		}
	}
	return append(kw, s.Project)
}

func (s recallSituation) entities() []string {
	if s.Project == "" {
		return nil
	}
	return []string{s.Project}
}

// fetchK is how many hits of each kind to ask for: twice topK when there is a
// last reply to choose among them by, topK otherwise.
func (s recallSituation) fetchK(topK int) int {
	if len(s.contextTerms()) == 0 {
		return topK
	}
	return 2 * topK
}

// contextTerms are the words of the start of the last reply that can tell one
// hit from another. Short ones are left out: "is" and "to" are inside nearly
// every text, and a term is matched as a substring.
func (s recallSituation) contextTerms() []string {
	if s.Previous == "" {
		return nil
	}
	var terms []string
	for _, k := range keywordsFromPrompt(clip(strings.Join(strings.Fields(s.Previous), " "), recallContextRunes)) {
		if len([]rune(k)) >= 4 {
			terms = append(terms, k)
		}
	}
	return terms
}

// focus keeps topK hits of each kind, those that share the most terms with
// the last reply first, and the brain's order among equals. It only chooses
// among what the prompt found: a memory the last reply alone would match is
// never asked for, so a new subject is not answered with the old one.
func (s recallSituation) focus(resp *cortexdb.KnowledgeMemoryRecallResponse, topK int) {
	terms := s.contextTerms()
	if len(terms) > 0 {
		shared := func(text string) int {
			text = strings.ToLower(text)
			n := 0
			for _, t := range terms {
				if strings.Contains(text, t) {
					n++
				}
			}
			return n
		}
		sort.SliceStable(resp.Memories, func(i, j int) bool {
			return shared(resp.Memories[i].Memory.Content) > shared(resp.Memories[j].Memory.Content)
		})
		sort.SliceStable(resp.Knowledge, func(i, j int) bool {
			return shared(resp.Knowledge[i].Title+" "+resp.Knowledge[i].Snippet) > shared(resp.Knowledge[j].Title+" "+resp.Knowledge[j].Snippet)
		})
	}
	if len(resp.Memories) > topK {
		resp.Memories = resp.Memories[:topK]
	}
	if len(resp.Knowledge) > topK {
		resp.Knowledge = resp.Knowledge[:topK]
	}
}

// lastAssistantText is the text of the last assistant entry in a Claude Code
// transcript, or "" when there is none or the file cannot be read.
func lastAssistantText(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > transcriptTailBytes {
		if _, err := f.Seek(info.Size()-transcriptTailBytes, io.SeekStart); err != nil {
			return ""
		}
	}
	last := ""
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	for scanner.Scan() {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		// The first line after a seek is cut mid-entry and fails here.
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.Type != "assistant" {
			continue
		}
		if text := strings.TrimSpace(textFromContent(entry.Message.Content)); text != "" {
			last = text
		}
	}
	return last
}
