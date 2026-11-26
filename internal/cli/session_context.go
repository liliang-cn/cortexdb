package cli

// `--session-context` answers the SessionStart hook with what the brain knows
// about the project the session starts in.
//
// The UserPromptSubmit recall answers each prompt on its own words, so the
// first prompt of a session — "carry on", "what's left?" — finds nothing, and
// the agent starts every session blind to the decisions it took yesterday in
// the same repository. This hands it the index at the door: the project's most
// recent memories, one line each, newest first. It is an index on purpose: a
// line per memory costs a few dozen tokens, and the agent fetches the ones it
// needs whole with memory_get memory_ids.
//
// The project is the one the library shelves memories under: a graph node of
// type "project" that memories mention. The session's working directory names
// it — its own name or its git root's — compared without case or punctuation.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// sessionContextMax is how many memories the index lists by default.
const sessionContextMax = 30

// toolCaller calls one CortexDB tool by name, on whichever brain this process
// uses, and returns its JSON answer.
type toolCaller func(ctx context.Context, name string, args any) ([]byte, error)

func localToolCaller(db *cortexdb.DB) toolCaller {
	tools := db.GraphRAGTools()
	return func(ctx context.Context, name string, args any) ([]byte, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		out, err := tools.Call(ctx, name, raw)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
}

func remoteToolCaller(client rpcv1.ToolsServiceClient) toolCaller {
	brain := remoteBrain{client: client}
	return func(ctx context.Context, name string, args any) ([]byte, error) {
		out, err := brain.call(ctx, name, args)
		return []byte(out), err
	}
}

func runSessionContext() {
	var in struct {
		Cwd       string `json:"cwd"`
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(os.Stdin).Decode(&in)

	// The plugin's standing instruction, handed over by the hook so both
	// arrive as one context block. It is written as a JSON string body there.
	directive := strings.ReplaceAll(os.Getenv("CORTEXDB_SESSION_DIRECTIVE"), `\n`, "\n")

	rules, index := "", ""
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if call, closeFn, ok := sessionToolCaller(); ok {
		defer closeFn()
		var names []string
		if in.Cwd != "" {
			names = projectNamesFor(in.Cwd)
		}
		budget := hookContextBudget - len([]rune(directive)) - 2
		// The standing rules come before the project's index and whatever the
		// prompt: they hold for every task in the session.
		var ruleIDs []string
		rules, ruleIDs = sessionRules(ctx, call, names, min(envInt("CORTEXDB_SESSION_RULES_CHARS", sessionRulesChars), budget))
		if rules != "" {
			budget -= len([]rune(rules)) + 2
			// The per-prompt recall does not hand them over again.
			markSeen(in.SessionID, ruleIDs)
		}
		if len(names) > 0 {
			index = sessionIndex(ctx, call, names, envInt("CORTEXDB_SESSION_CONTEXT_MAX", sessionContextMax), budget)
		}
	}

	text := strings.TrimSpace(strings.Join(nonEmpty(directive, rules, index), "\n\n"))
	if text == "" {
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(hookOutput{HookSpecificOutput: hookSpecificOutput{
		HookEventName:     "SessionStart",
		AdditionalContext: text,
	}})
}

// sessionRulesChars bounds the standing rules block at session start.
const sessionRulesChars = 2400

// sessionRules is the standing rules block (memory_standing_rules) and the ids
// it lists, or "" when the brain has none or is too old to answer.
func sessionRules(ctx context.Context, call toolCaller, names []string, budget int) (string, []string) {
	if budget <= 0 {
		return "", nil
	}
	args := map[string]any{"max_chars": budget}
	if len(names) > 0 {
		// The repository's name when the session is in one: that is what
		// memories name as their project.
		args["project"] = names[len(names)-1]
	}
	raw, err := call(ctx, "memory_standing_rules", args)
	if err != nil {
		return "", nil
	}
	var got cortexdb.StandingRulesResponse
	if json.Unmarshal(raw, &got) != nil {
		return "", nil
	}
	ids := make([]string, 0, len(got.Rules))
	for _, r := range got.Rules {
		ids = append(ids, r.ID)
	}
	return strings.TrimSpace(got.Text), ids
}

func nonEmpty(parts ...string) []string {
	out := parts[:0:0]
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// sessionToolCaller opens the brain this process uses: the shared one when
// CORTEXDB_REMOTE names it, else the local file if there is one. A session
// start never creates a database.
func sessionToolCaller() (toolCaller, func(), bool) {
	if addr, token, ok := remoteConfigured(); ok {
		conn, err := dialCortexDB(addr, token)
		if err != nil {
			return nil, nil, false
		}
		return remoteToolCaller(rpcv1.NewToolsServiceClient(conn)), func() { _ = conn.Close() }, true
	}
	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	if info, err := os.Stat(dbPath); err != nil || info.IsDir() {
		return nil, nil, false
	}
	db, err := openBrainDB(dbPath)
	if err != nil {
		return nil, nil, false
	}
	return localToolCaller(db), func() { _ = db.Close() }, true
}

// projectNamesFor is what a working directory may call its project: its own
// name, then the name of the git repository it is in.
func projectNamesFor(cwd string) []string {
	names := []string{filepath.Base(cwd)}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			if base := filepath.Base(dir); base != names[0] {
				names = append(names, base)
			}
			break
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	return names
}

// projectKey compares project names without case or punctuation:
// "cortex-db", "CortexDB" and "cortex_db" are one project.
func projectKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type cypherRows struct {
	Rows [][]any `json:"rows"`
}

func cypherQuery(ctx context.Context, call toolCaller, query string, params map[string]any, maxRows int) ([][]any, error) {
	raw, err := call(ctx, "graph_cypher_query", map[string]any{"query": query, "params": params, "max_rows": maxRows})
	if err != nil {
		return nil, err
	}
	var out cypherRows
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Rows, nil
}

// sessionIndex is the index block for the first project in names the graph
// knows, or "" when it knows none of them or has nothing on it.
func sessionIndex(ctx context.Context, call toolCaller, names []string, max, budget int) string {
	projects, err := cypherQuery(ctx, call, "MATCH (p:project) RETURN id(p), p.name", nil, 5000)
	if err != nil {
		return ""
	}
	var projectID, projectName string
	for _, want := range names {
		key := projectKey(want)
		for _, row := range projects {
			if len(row) < 2 {
				continue
			}
			name, _ := row[1].(string)
			if key != "" && projectKey(name) == key {
				projectID, _ = row[0].(string)
				projectName = name
				break
			}
		}
		if projectID != "" {
			break
		}
	}
	if projectID == "" {
		return ""
	}

	memories := currentMemoriesAbout(ctx, call, projectID)
	if len(memories) == 0 {
		return ""
	}
	lines := indexLines(memories, max)
	header := fmt.Sprintf("CortexDB memories about %s, newest first (%s of %d). This is an index: fetch any in full with memory_get memory_ids, and search beyond it with knowledge_memory_recall.",
		projectName, strconv.Itoa(len(lines)), len(memories))
	return fitContext(header, lines, "", budget)
}

// currentMemoriesAbout is every memory that mentions the graph node, newest
// first, leaving out the ones a newer memory superseded.
func currentMemoriesAbout(ctx context.Context, call toolCaller, nodeID string) []cortexdb.MemoryRecord {
	rows, err := cypherQuery(ctx, call, "MATCH (m:memory)-[:mentions]->(p) WHERE id(p) = $node RETURN id(m)", map[string]any{"node": nodeID}, 2000)
	if err != nil || len(rows) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if node, _ := row[0].(string); strings.HasPrefix(node, "memory:") {
			ids = append(ids, strings.TrimPrefix(node, "memory:"))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	raw, err := call(ctx, "memory_get", map[string]any{"memory_ids": ids})
	if err != nil {
		return nil
	}
	var got cortexdb.MemoryGetResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil
	}
	memories := got.Memories[:0]
	for _, m := range got.Memories {
		// A superseded memory stays stored but is no longer the truth.
		if s, _ := m.Metadata["superseded_by"].(string); strings.TrimSpace(s) != "" {
			continue
		}
		memories = append(memories, m)
	}
	sort.SliceStable(memories, func(i, j int) bool { return memories[i].CreatedAt.After(memories[j].CreatedAt) })
	return memories
}

// indexLines is one line per memory, at most max of them: id, date, first line.
func indexLines(memories []cortexdb.MemoryRecord, max int) []string {
	if max > 0 && len(memories) > max {
		memories = memories[:max]
	}
	lines := make([]string, 0, len(memories))
	for _, m := range memories {
		date := ""
		if !m.CreatedAt.IsZero() {
			date = m.CreatedAt.Format("2006-01-02") + " "
		}
		lines = append(lines, "- "+m.ID+" "+date+indexLine(m.Content))
	}
	return lines
}

// indexLine is a memory's first line, flattened and cut.
func indexLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			if r := []rune(line); len(r) > 110 {
				return strings.TrimSpace(string(r[:110])) + "…"
			}
			return line
		}
	}
	return ""
}
