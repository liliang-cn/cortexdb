package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// runCaptureSession turns a finished session into fact-scoped memories.
//
// The brain only knew what an agent remembered to tell it: forget to call
// memory_save and the session never happened. This is the write-side loop —
// the SessionEnd hook hands over the transcript, an LLM distils the durable
// facts, and each fact becomes its own memory. One fact per memory on purpose:
// a long mixed note embeds to mush, and its one interesting fact drowns; a
// focused memory is findable by exactly the question it answers.
//
// `--capture-session [transcript.jsonl] [--dry-run]`. Without a path it reads
// the hook payload from stdin. Requires CORTEXDB_LLM_* (exits quietly
// otherwise — capture is opt-in by configuration, not by flag).
func runCaptureSession(args []string) {
	transcriptPath := ""
	sessionID := ""
	dryRun := false
	for _, a := range args {
		switch {
		case a == "--dry-run":
			dryRun = true
		case !strings.HasPrefix(a, "-") && transcriptPath == "":
			transcriptPath = a
		}
	}
	if transcriptPath == "" {
		var payload struct {
			SessionID      string `json:"session_id"`
			TranscriptPath string `json:"transcript_path"`
		}
		if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
			return // no payload, nothing to do — a hook must never fail the session
		}
		transcriptPath = payload.TranscriptPath
		sessionID = payload.SessionID
	}
	if transcriptPath == "" {
		return
	}
	if sessionID == "" {
		sessionID = strings.TrimSuffix(filepath.Base(transcriptPath), filepath.Ext(transcriptPath))
	}

	llm := newOrganizeLLM()
	if llm == nil {
		fmt.Println("capture: no CORTEXDB_LLM_* configured, skipping")
		return
	}

	digest, userTurns, err := digestTranscript(transcriptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: capture: %v\n", err)
		os.Exit(1)
	}
	// A short exchange holds nothing worth keeping; capturing it would write
	// noise on every quick question.
	if userTurns < 3 {
		fmt.Printf("capture: only %d user turns, skipping\n", userTurns)
		return
	}

	facts, err := extractSessionFacts(context.Background(), llm, digest, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: capture: %v\n", err)
		os.Exit(1)
	}
	if len(facts) == 0 {
		fmt.Println("capture: nothing durable in this session")
		return
	}

	ctx := context.Background()
	brain, _, _, closeBrain, err := openBrainWriter()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: capture: %v\n", err)
		os.Exit(1)
	}
	defer closeBrain()
	now := time.Now()
	plan, err := reconcileFacts(ctx, llm, brain, facts, capturedIDPrefix(sessionID), now)
	if err != nil {
		// Saving without retiring anything is what capture always did.
		fmt.Fprintf(os.Stderr, "cortexdb: capture: checking for outdated memories: %v\n", err)
	}

	date := now.Format("2006-01-02")
	saved, retired := 0, 0
	for _, f := range facts {
		req := capturedRequest(sessionID, date, f)
		if req.Content == "" {
			continue
		}
		if !plan.apply(f.Slug, &req) {
			fmt.Printf("outdated %s (contradicted by %s): %s\n", req.MemoryID, plan.Outdated[f.Slug], clip(req.Content, 120))
			continue
		}
		if dryRun {
			fmt.Printf("would save %s (importance %.2f): %s\n", req.MemoryID, req.Importance, clip(req.Content, 160))
			for _, id := range req.Supersedes {
				fmt.Printf("  would retire %s\n", id)
			}
			continue
		}
		if err := brain.SaveMemory(ctx, req); err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: capture: save %s: %v\n", req.MemoryID, err)
			os.Exit(1)
		}
		fmt.Printf("saved %s: %s\n", req.MemoryID, clip(req.Content, 120))
		for _, id := range req.Supersedes {
			fmt.Printf("  retired %s\n", id)
		}
		saved++
		retired += len(req.Supersedes)
	}
	if !dryRun {
		fmt.Printf("capture: %d memories from %d user turns, %d older ones retired\n", saved, userTurns, retired)
	}
}

// capturedRequest is the memory one captured fact becomes. Every capture path —
// the SessionEnd hook, the cortexdb-live mod, an import of past sessions —
// writes this same record, so a fact reads the same whichever wrote it.
func capturedRequest(sessionID, date string, f capturedFact) cortexdb.MemorySaveRequest {
	req := cortexdb.MemorySaveRequest{
		// Stable per session+slug, so re-capturing a session overwrites its
		// own memories instead of stacking duplicates.
		MemoryID:   capturedIDPrefix(sessionID) + f.Slug,
		Scope:      "global",
		Content:    strings.TrimSpace(f.Content),
		Importance: clamp01(f.Importance),
		Metadata: map[string]any{
			"source":  "auto-capture",
			"session": sessionID,
			"date":    date,
			"type":    firstNonEmptyStr(f.Type, "fact"),
		},
	}
	for _, e := range f.Entities {
		if strings.TrimSpace(e.Name) == "" {
			continue
		}
		req.Entities = append(req.Entities, cortexdb.ToolEntityInput{Name: e.Name, Type: e.Type})
	}
	return req
}

// capturedIDPrefix is what every memory captured from one session starts with.
func capturedIDPrefix(sessionID string) string {
	short := sessionID
	if len(short) > 8 {
		short = short[:8]
	}
	return "auto:" + short + ":"
}

// codexIDPrefix is capturedIDPrefix for a Codex session. Codex ids are UUIDv7:
// their first eight characters are a clock that sessions a minute apart share,
// so the tail tells them apart.
func codexIDPrefix(sessionID string) string {
	return "auto:codex-" + sessionID[max(0, len(sessionID)-8):] + ":"
}

// digestTranscript reduces a session transcript to the conversation itself:
// what the user typed and what the assistant said, without the tool noise that
// outweighs it twenty to one.
func digestTranscript(path string) (string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	var b strings.Builder
	userTurns := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4*1024*1024), 4*1024*1024)
	for scanner.Scan() {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		switch entry.Type {
		case "user":
			text := textFromContent(entry.Message.Content)
			text = stripTranscriptNoise(text)
			if text == "" {
				continue
			}
			userTurns++
			fmt.Fprintf(&b, "USER: %s\n", clip(text, 2000))
		case "assistant":
			text := textFromContent(entry.Message.Content)
			if strings.TrimSpace(text) == "" {
				continue
			}
			fmt.Fprintf(&b, "ASSISTANT: %s\n", clip(text, 1200))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", 0, err
	}
	return budgetDigest(b.String()), userTurns, nil
}

// budgetDigest keeps a digest within what one extraction call is given. Long
// sessions conclude late: keep the head for context and spend the budget on
// the tail, where decisions and outcomes live. The cut lands on a rune
// boundary, so a session in Chinese is never handed over mid-character.
func budgetDigest(digest string) string {
	const headBudget, tailBudget = 6000, 22000
	if len(digest) <= headBudget+tailBudget {
		return digest
	}
	head, tail := headBudget, len(digest)-tailBudget
	for head > 0 && !utf8.RuneStart(digest[head]) {
		head--
	}
	for tail < len(digest) && !utf8.RuneStart(digest[tail]) {
		tail++
	}
	return digest[:head] + "\n[...trimmed...]\n" + digest[tail:]
}

// textFromContent extracts the text of a message whose content is either a
// plain string or a block list; tool blocks are skipped.
func textFromContent(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, blk := range blocks {
		if blk.Type == "text" && strings.TrimSpace(blk.Text) != "" {
			parts = append(parts, blk.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// Go's RE2 has no backreferences, so the open and close tags are matched by
// the same alternation rather than by name equality — fine for noise, which is
// never nested.
var transcriptNoisePattern = regexp.MustCompile(`(?s)<(?:system-reminder|local-command-caveat|command-name|command-message|command-args|local-command-stdout|task-notification)>.*?</(?:system-reminder|local-command-caveat|command-name|command-message|command-args|local-command-stdout|task-notification)>|\[SYSTEM NOTIFICATION[^\]]*\].*`)

// stripTranscriptNoise removes injected machinery from a user turn — hook
// context, slash-command wrappers, task notifications — leaving what the person
// actually typed. A turn that was all machinery becomes empty and is dropped.
func stripTranscriptNoise(text string) string {
	return strings.TrimSpace(transcriptNoisePattern.ReplaceAllString(text, ""))
}

// capturedFact is one durable thing a session established.
type capturedFact struct {
	Slug       string  `json:"slug"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
	Type       string  `json:"type"`
	Entities   []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"entities"`
}

const captureSystemPrompt = `You distil a coding-session transcript into durable memories for a long-term store shared across future sessions.

Extract ONLY things worth knowing weeks later: decisions made and why, facts established (deployments, topology, versions, credentials locations — not values), user preferences expressed, outcomes and their root causes, lessons that changed how something is done.

Rules:
- One self-contained fact per memory. Never bundle; a reader sees one memory alone.
- Each content must make sense with zero session context: name the project and subject explicitly, use absolute dates.
- Write each memory in the language the conversation used for that topic.
- Skip: transient debugging steps, anything superseded within the session, tool chatter, politeness, plans that were replaced.
- 0 to 8 memories. An uneventful session yields zero — that is a good answer.
- slug: short kebab-case ascii. importance: 0.3 routine fact, 0.6 useful decision, 0.85+ hard-won lesson or standing preference.
- entities: the concrete named things (projects, hosts, services, people) each memory is about.

Respond with JSON only: {"memories":[{"slug":"...","content":"...","importance":0.6,"type":"fact|decision|preference|lesson","entities":[{"name":"...","type":"..."}]}]}`

func extractSessionFacts(ctx context.Context, llm interface {
	GenerateJSON(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error)
}, digest string, asOf time.Time) ([]capturedFact, error) {
	// asOf is the day the session happened, not the day it is read: a session
	// imported months later must not have its "today" dated to the import.
	userPrompt := fmt.Sprintf("Today is %s.\n\nTranscript digest:\n%s", asOf.Format("2006-01-02"), digest)
	raw, err := llm.GenerateJSON(ctx, captureSystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	var out struct {
		Memories []capturedFact `json:"memories"`
	}
	if err := json.Unmarshal(repairJSON(raw), &out); err != nil {
		return nil, fmt.Errorf("decode llm output: %w (raw: %s)", err, clip(string(raw), 200))
	}
	facts := make([]capturedFact, 0, len(out.Memories))
	for _, f := range out.Memories {
		f.Slug = slugify(firstNonEmptyStr(f.Slug, f.Content))
		if f.Slug == "" || strings.TrimSpace(f.Content) == "" {
			continue
		}
		facts = append(facts, f)
	}
	return facts, nil
}

// repairJSON balances trailing braces and brackets. One deployed proxy returns
// otherwise-valid JSON missing its final closer; retrying does not help, so the
// adapter repairs rather than insists.
func repairJSON(raw []byte) []byte {
	s := strings.TrimSpace(string(raw))
	// Models fence JSON in markdown on a whim — the same model answers bare one
	// run and fenced the next.
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	if json.Valid([]byte(s)) {
		return []byte(s)
	}
	var stack []byte
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 && stack[len(stack)-1] == c {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if inString {
		s += `"`
	}
	for i := len(stack) - 1; i >= 0; i-- {
		s += string(stack[i])
	}
	return []byte(s)
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
