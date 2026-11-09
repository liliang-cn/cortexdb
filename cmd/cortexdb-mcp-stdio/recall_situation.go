package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// recallSituation is what a prompt is said against: the project the session
// works in and the start of the last reply. "这个修复前后可以验证吗" names
// nothing by itself; asked bare it recalls whatever other project once fixed
// and verified something. The cortexdb-live mod asks the same way when its
// planner does not answer (hooks/register.tsx, situated).
//
// Both ride in the query. v2.148.0 took the last reply out of it, so that a
// short prompt on a new subject would not be answered with the old one
// (#142); on real prompts that cost more than it saved. "？", "做吧",
// "测试通过了吗？" are most of the short prompts, they point at the last reply,
// and asked bare they recalled noise (#144).
type recallSituation struct {
	Project  string
	Previous string
	// Image reports that the prompt being answered came with a picture.
	Image bool
}

// recallContextRunes is how much of the last reply rides along with the
// prompt: enough to name the subject, not so much that it outweighs the prompt.
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
		s.Previous, s.Image = readTranscriptTail(in.TranscriptPath, strings.TrimSpace(in.Prompt))
	}
	s.Image = s.Image || carriesImage(in.Prompt)
	return s
}

// imagePlaceholder is how the Claude Code CLI writes an attached image into
// the prompt text: "[Image #1] what happened？". The desktop app leaves the
// text alone and stores the image as a block of its own, which only the
// transcript shows (readTranscriptTail).
var imagePlaceholder = regexp.MustCompile(`\[Image #\d+\]`)

// carriesImage reports a prompt whose text says it comes with a picture.
func carriesImage(prompt string) bool {
	return imagePlaceholder.MatchString(prompt)
}

// query is the prompt with the project and the start of the last reply after
// it. The server embeds the query alone, so the context has to be in it.
func (s recallSituation) query(prompt string) string {
	var lines []string
	if s.Project != "" {
		lines = append(lines, "Project: "+s.Project)
	}
	if s.Previous != "" {
		lines = append(lines, "Context: "+clip(strings.Join(strings.Fields(s.Previous), " "), recallContextRunes))
	}
	if len(lines) == 0 {
		return prompt
	}
	return prompt + "\n\n" + strings.Join(lines, "\n")
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

// readTranscriptTail reads the end of a Claude Code transcript: the text of
// the last assistant entry before the prompt being answered, and whether that
// prompt came with an image. The prompt is known by its text: when Claude Code
// has already written it, it is the last user entry and nothing has answered
// it, and its image blocks say what the desktop app's prompt text does not. An
// older message is never taken for this one: something came after it.
// "" and false when the file cannot be read.
func readTranscriptTail(path, prompt string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > transcriptTailBytes {
		if _, err := f.Seek(info.Size()-transcriptTailBytes, io.SeekStart); err != nil {
			return "", false
		}
	}
	// pending is the prompt's own entry while nothing has come after it.
	last := ""
	var pending struct {
		on     bool
		before string
		image  bool
	}
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
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		switch entry.Type {
		case "assistant":
			if text := strings.TrimSpace(textFromContent(entry.Message.Content)); text != "" {
				last = text
				pending.on = false
			}
		case "user":
			text := strings.TrimSpace(stripTranscriptNoise(textFromContent(entry.Message.Content)))
			if text == "" {
				continue // a tool result, or machinery
			}
			pending.on = prompt != "" && text == prompt
			pending.before, pending.image = last, contentHasImage(entry.Message.Content)
		}
	}
	if pending.on {
		return pending.before, pending.image
	}
	return last, false
}

// contentHasImage reports a message content that holds an image block.
func contentHasImage(raw json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "image" {
			return true
		}
	}
	return false
}
