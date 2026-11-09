package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"unicode"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// hookInput is the subset of the UserPromptSubmit hook payload we read from stdin.
type hookInput struct {
	Prompt         string `json:"prompt"`
	Cwd            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

// runRecall reads a UserPromptSubmit hook payload from stdin, searches durable
// knowledge for the prompt, and prints matched memories as additionalContext for
// Claude Code to inject.
//
// It is deliberately silent on every failure path: a missing DB, an empty
// prompt, an open error, or zero results all exit 0 with no output, so the
// user's prompt is never blocked or delayed by an error message.
func runRecall() {
	var in hookInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return
	}
	// What the person typed, without the machinery Claude Code injects: a
	// background task's <task-notification> is a turn of its own, and was
	// recalled for as if it were a question. A turn that is all machinery is
	// not recalled for, as capture drops it (stripTranscriptNoise).
	prompt := stripTranscriptNoise(in.Prompt)
	if prompt == "" {
		return
	}
	in.Prompt = prompt

	topK := envInt("CORTEXDB_RECALL_TOPK", 3)
	situation := situationFor(in)
	// A prompt with a picture is not recalled for: the hook cannot see the
	// picture, so whatever it found for the words beside it would come from
	// the turn before (#142).
	if situation.Image {
		return
	}

	// Shared-brain mode: answer from the one central database, not this
	// machine's local file. Without this branch --recall returns from main
	// before the CORTEXDB_REMOTE check ever runs, and a machine on a shared
	// brain keeps injecting memories from a file nothing writes to any more.
	if remote := strings.TrimSpace(os.Getenv("CORTEXDB_REMOTE")); remote != "" {
		emitRecall(runRecallRemote(remote, os.Getenv("CORTEXDB_GRPC_TOKEN"), prompt, situation, topK))
		return
	}

	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	// Never create a database just to recall from it: if it isn't already
	// there, this project simply has no CortexDB memory yet.
	if info, err := os.Stat(dbPath); err != nil || info.IsDir() {
		return
	}

	db, err := openBrainDB(dbPath)
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()

	// Same question the shared-brain path asks: memory AND knowledge, not
	// knowledge alone. A local brain that only surfaced knowledge documents hid
	// every memory_save behind a tool call the agent had to think to make.
	resp, err := db.KnowledgeMemory().Recall(context.Background(), cortexdb.KnowledgeMemoryRecallRequest{
		Query:         situation.query(prompt),
		Keywords:      situation.keywords(prompt),
		EntityNames:   situation.entities(),
		RetrievalMode: cortexdb.RetrievalModeAuto,
		GraphLight:    true,
		TopKMemories:  topK,
		TopKKnowledge: topK,
	})
	if err != nil || resp == nil {
		return
	}

	hits := hitsFromRecall(resp)
	emitRecall(formatRecallHits(hits))
}

// emitRecall prints the additionalContext block, or nothing at all when there
// is nothing worth injecting.
func emitRecall(context string) {
	if context == "" {
		return
	}
	out := hookOutput{HookSpecificOutput: hookSpecificOutput{
		HookEventName:     "UserPromptSubmit",
		AdditionalContext: strings.TrimRight(context, "\n"),
	}}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

// envInt reads a positive integer from an env var, falling back to def.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// keywordsFromPrompt splits a natural-language prompt into deduped lexical
// terms. CJK runs stay as single tokens, matching the FTS5 unicode61 tokenizer.
func keywordsFromPrompt(prompt string) []string {
	fields := strings.FieldsFunc(prompt, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool, len(fields))
	kw := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ToLower(f)
		if len([]rune(f)) < 2 || seen[f] {
			continue
		}
		seen[f] = true
		kw = append(kw, f)
	}
	return kw
}
