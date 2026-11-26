package cli

import (
	"os"
	"strings"
)

// flagSpec is one flag a command takes. A flag with a value names it in
// value ("N", "PATH"); a boolean flag leaves it empty.
type flagSpec struct {
	name  string
	alias string
	value string
	help  string
}

// command is one thing the CLI does. The table below is the only list of
// them: dispatch, the flag check, help and shell completion all read it, so
// a mode cannot be reachable and undocumented, or documented and missing.
type command struct {
	// path is the command as `cortexdb` spells it: {"graph", "html"}.
	path []string
	// legacy is the `cortexdb-mcp-stdio --<mode>` spelling the plugins,
	// hooks and integrations call, kept working unchanged. Empty for
	// commands that are new with `cortexdb`.
	legacy  string
	args    string
	summary string
	flags   []flagSpec
	// minArgs and maxArgs bound the positional arguments; maxArgs -1 is
	// unbounded.
	minArgs, maxArgs int
	// jsonArgs says how `--format json` reaches the command: nil, it has no
	// JSON output and the flag is refused; otherwise it rewrites the
	// command's arguments for JSON output (identity when it only prints
	// JSON).
	jsonArgs func(args []string) []string
	// anyArgs: the arguments are not held to the flag table (help, which
	// takes another command's words).
	anyArgs bool
	// group heads the help section the command is listed under.
	group string
	run   func(inv *invocation, args []string) int
}

func (c *command) name() string { return strings.Join(c.path, " ") }

func (c *command) flag(name string) (flagSpec, bool) {
	for _, f := range c.flags {
		if f.name == name || (f.alias != "" && f.alias == name) {
			return f, true
		}
	}
	return flagSpec{}, false
}

// void adapts a mode that exits the process on failure and returns on
// success.
func void(fn func(args []string)) func(*invocation, []string) int {
	return func(_ *invocation, args []string) int {
		fn(args)
		return 0
	}
}

func jsonAsIs(args []string) []string { return args }

var (
	dryRunFlag = flagSpec{name: "--dry-run", help: "report what would change, change nothing"}
	limitFlag  = flagSpec{name: "--limit", value: "N", help: "stop after N items"}
)

const (
	groupBrain  = "Brain"
	groupTools  = "Tools"
	groupGraph  = "Graph"
	groupMemory = "Memory"
	groupImport = "Import"
	groupLearn  = "Learning graph"
	groupAsk    = "Questions"
	groupHooks  = "Agent hooks"
	groupShell  = "Shell"
)

var groupOrder = []string{groupBrain, groupTools, groupAsk, groupGraph, groupMemory, groupImport, groupLearn, groupHooks, groupShell}

// commands is filled in init: several entries' run functions read the table
// itself (help, completion), which a package-level initializer cannot.
var commands []*command

func init() {
	commands = []*command{
		{path: []string{"serve"}, group: groupBrain, summary: "serve MCP on stdio (what cortexdb-mcp-stdio does with no arguments)",
			run: func(*invocation, []string) int { return serve() }},
		{path: []string{"version"}, legacy: "--version", group: groupBrain, summary: "print the version", jsonArgs: jsonAsIs,
			run: runVersion},
		{path: []string{"doctor"}, legacy: "--doctor", group: groupBrain, summary: "check the brain this machine is configured for and print a JSON report",
			flags:    []flagSpec{{name: "--self-test", help: "also write, read back and delete a probe memory"}},
			jsonArgs: jsonAsIs, run: func(_ *invocation, args []string) int {
				if err := runDoctor(args); err != nil {
					os.Stderr.WriteString("cortexdb doctor: " + err.Error() + "\n")
					return 1
				}
				return 0
			}},
		{path: []string{"sweep-expired"}, legacy: "--sweep-expired", group: groupBrain, summary: "delete the documents and memories whose expires_at has passed",
			flags:    []flagSpec{dryRunFlag, {name: "--json", help: "print the report as JSON"}},
			jsonArgs: func(args []string) []string { return append(args, "--json") },
			run: func(_ *invocation, args []string) int {
				return sweepExpiredCommand(args, os.Stdout, os.Stderr)
			}},

		{path: []string{"tool", "list"}, group: groupTools, summary: "list the tools serve exposes, with their effect",
			flags:    []flagSpec{{name: "--read-only", help: "only the tools that change nothing"}},
			jsonArgs: jsonAsIs, run: runToolList},
		{path: []string{"tool", "describe"}, group: groupTools, args: "<tool>", minArgs: 1, maxArgs: 1, summary: "print a tool's description and input schema",
			jsonArgs: jsonAsIs, run: runToolDescribe},
		{path: []string{"tool", "call"}, group: groupTools, args: "<tool>", minArgs: 1, maxArgs: 1, summary: "call a tool and print its result",
			flags: []flagSpec{
				{name: "--json", value: "OBJECT", help: "the arguments as a JSON object; - reads it from stdin"},
				{name: "--arg", value: "KEY=VALUE", help: "one argument, typed by the tool's schema; repeatable, wins over --json"},
				{name: "--yes", alias: "-y", help: "call a destructive tool without asking"},
			},
			jsonArgs: jsonAsIs, run: runToolCall},
		{path: []string{"search"}, group: groupTools, args: "<query>", minArgs: 1, maxArgs: -1, summary: "recall memory, knowledge and graph for a query (knowledge_memory_recall)",
			flags: []flagSpec{
				{name: "--top-k", value: "N", help: "at most N memory hits and N knowledge hits"},
				{name: "--retrieval-mode", value: "MODE", help: "auto, lexical, graph or ppr"},
			},
			jsonArgs: jsonAsIs, run: runSearch},
		{path: []string{"remember"}, group: groupTools, args: "<text>", minArgs: 1, maxArgs: -1, summary: "save a memory (memory_save)",
			flags: []flagSpec{
				{name: "--id", value: "ID", help: "the memory's id; an existing one is replaced (default: a new id)"},
				{name: "--scope", value: "SCOPE", help: "global, user or session"},
				{name: "--yes", alias: "-y", help: "replace an existing memory without asking"},
			},
			jsonArgs: jsonAsIs, run: runRemember},
		{path: []string{"get"}, group: groupTools, args: "<memory-id>...", minArgs: 1, maxArgs: -1, summary: "print memories by id (memory_get)",
			jsonArgs: jsonAsIs, run: runGet},

		{path: []string{"multi-hop"}, legacy: "--multi-hop", group: groupAsk, args: "<question>", minArgs: 1, maxArgs: -1,
			summary: "answer a multi-step question by retrieving and reasoning in turns (needs CORTEXDB_LLM_*)", run: void(runMultiHop)},
		{path: []string{"global-search"}, legacy: "--global-search", group: groupAsk, args: "<question>", minArgs: 1, maxArgs: -1,
			summary: "answer a whole-corpus question from community summaries",
			flags:   []flagSpec{{name: "--level", value: "N", help: "community level to summarise at"}},
			run:     void(runGlobalSearch)},
		{path: []string{"facts-as-of"}, legacy: "--facts-as-of", group: groupAsk, args: "[RFC3339]", maxArgs: 1,
			summary: "print the temporal facts valid at an instant (default now)",
			flags: []flagSpec{
				{name: "--from", value: "NAME", help: "only facts about this subject"},
				{name: "--type", value: "TYPE", help: "only this relation type"},
			},
			run: void(runFactsAsOf)},
		{path: []string{"recall-eval"}, legacy: "--recall-eval", group: groupAsk, args: "<golden.jsonl>", minArgs: 1, maxArgs: 1,
			summary: "measure recall against a golden set of queries",
			flags: []flagSpec{
				{name: "--top-k", value: "N", help: "hits per query"},
				{name: "--retrieval-mode", value: "MODE", help: "auto, lexical, graph or ppr"},
				{name: "--out", value: "PATH", help: "write per-case results as JSON"},
			},
			run: void(runRecallEval)},

		{path: []string{"graph", "html"}, legacy: "--graph-html", group: groupGraph, args: "[out-dir]", maxArgs: 1,
			summary: "render the knowledge graph to an interactive HTML file",
			flags:   []flagSpec{{name: "--organize", help: "group nodes with the configured LLM first"}},
			run: func(_ *invocation, args []string) int {
				out, organize := "", false
				for _, a := range args {
					if a == "--organize" {
						organize = true
					} else if out == "" {
						out = a
					}
				}
				runGraphHTML(out, organize)
				return 0
			}},
		{path: []string{"graph", "3d"}, legacy: "--graph-3d", group: groupGraph,
			summary: "serve the graph as a live 3D view until interrupted",
			flags: []flagSpec{
				{name: "--port", value: "N", help: "port on 127.0.0.1"},
				{name: "--listen", value: "HOST:PORT", help: "address to listen on (needs --token-file off loopback)"},
				{name: "--token-file", value: "PATH", help: "file holding the view's access token"},
				{name: "--interval", value: "DURATION", help: "refresh interval, e.g. 2s"},
				{name: "--no-open", help: "do not open a browser"},
			},
			run: void(runGraphLive)},
		{path: []string{"graph", "update"}, legacy: "--graph-update", group: groupGraph, args: "[file|-]", maxArgs: 1,
			summary: "reconcile new text against the graph with an LLM (stdin by default)",
			flags:   []flagSpec{{name: "--dry-run", alias: "-n", help: dryRunFlag.help}, {name: "--allow-delete", help: "apply the deletions the model proposes"}},
			run:     void(runGraphUpdate)},
		{path: []string{"graph", "cleanup"}, legacy: "--graph-cleanup", group: groupGraph,
			summary: "prune junk entity nodes and backfill graph presence for memories",
			flags: []flagSpec{dryRunFlag,
				{name: "--prune-only", help: "only prune"},
				{name: "--reindex-only", help: "only backfill"},
				{name: "--dangling-only", help: "only remove edges to missing nodes"},
				limitFlag},
			run: void(runGraphCleanup)},
		{path: []string{"graph", "resolve-entities"}, legacy: "--resolve-entities", group: groupGraph,
			summary: "merge duplicate entities and link uncertain ones for review",
			flags: []flagSpec{{name: "--dry-run", alias: "-n", help: dryRunFlag.help},
				{name: "--merge-all", help: "merge the uncertain candidates too"},
				{name: "--types", value: "T1,T2", help: "only these node types"}},
			run: void(runResolveEntities)},

		{path: []string{"memory", "html"}, legacy: "--memory-html", group: groupMemory, args: "[out-dir]", maxArgs: 1,
			summary: "render every memory to an HTML dashboard",
			run: func(_ *invocation, args []string) int {
				out := ""
				if len(args) > 0 {
					out = args[0]
				}
				runMemoryHTML(out)
				return 0
			}},
		{path: []string{"memory", "export"}, legacy: "--export-memory", group: groupMemory, args: "[out-dir]", maxArgs: 1,
			summary: "write every memory to Markdown files with a MEMORY.md index",
			flags:   []flagSpec{{name: "--min-grade", value: "GRADE", help: "only memories graded at least this well"}},
			run:     void(runExportMemory)},
		{path: []string{"memory", "sync"}, legacy: "--sync-memory", group: groupMemory, args: "[dir]", maxArgs: 1,
			summary: "write an exported directory back into the brain",
			flags:   []flagSpec{{name: "--prune", help: "delete memories whose file is gone"}, dryRunFlag},
			run:     void(runSyncMemory)},
		{path: []string{"memory", "usage"}, legacy: "--memory-usage", group: groupMemory,
			summary: "report which memories recall surfaces and which it never has",
			flags:   []flagSpec{{name: "--top", value: "N", help: "rows per list"}, {name: "--stale-days", value: "D", help: "unrecalled for this many days counts as stale"}},
			run:     void(runMemoryUsage)},
		{path: []string{"memory", "reembed"}, legacy: "--reembed-memories", group: groupMemory,
			summary: "embed memories whose vector is missing or the wrong size",
			flags:   []flagSpec{dryRunFlag, limitFlag},
			run:     void(runReembedMemories)},

		{path: []string{"import", "agent-memory"}, legacy: "--import-agent-memory", group: groupImport, args: "[root...]", maxArgs: -1,
			summary: "import Claude Code, Codex and dsh memory",
			flags: []flagSpec{{name: "--sessions", help: "also import session transcripts"}, dryRunFlag,
				{name: "--since", value: "AGE", help: "only sessions newer than this, e.g. 30d"},
				{name: "--max", value: "N", help: "at most N sessions"}},
			run: void(runImportAgentMemory)},
		{path: []string{"import", "code-graph"}, legacy: "--import-code-graph", group: groupImport, args: "[file.json]", maxArgs: 1,
			summary: "ingest a code-graph JSON (stdin by default)", run: void(runImportCodeGraph)},
		{path: []string{"import", "learning-graph"}, legacy: "--import-learning-graph", group: groupImport, args: "[file.json]", maxArgs: 1,
			summary: "ingest concepts and prerequisites (stdin by default)", run: void(runImportLearningGraph)},

		{path: []string{"learn", "path"}, legacy: "--learn-path", group: groupLearn, args: "<concept>", minArgs: 1, maxArgs: -1,
			summary: "print the ordered prerequisites of a concept", run: void(runLearnPath)},
		{path: []string{"learn", "next"}, legacy: "--learn-next", group: groupLearn, args: "[limit]", maxArgs: 1,
			summary: "print what is ready to study now", run: void(runLearnNext)},
		{path: []string{"learn", "mastered"}, legacy: "--learn-mastered", group: groupLearn, args: "<concept>...", minArgs: 1, maxArgs: -1,
			summary: "record concepts as mastered", run: void(runLearnMastered)},

		{path: []string{"hook", "recall"}, legacy: "--recall", group: groupHooks,
			summary: "UserPromptSubmit: read the hook payload on stdin, print recalled context",
			run:     func(*invocation, []string) int { runRecall(); return 0 }},
		{path: []string{"hook", "session-context"}, legacy: "--session-context", group: groupHooks,
			summary: "SessionStart: print the standing directive and recent project memories",
			run:     func(*invocation, []string) int { runSessionContext(); return 0 }},
		{path: []string{"hook", "file-context"}, legacy: "--file-context", group: groupHooks,
			summary: "PreToolUse on Read/Edit/Write: print the memories about that file",
			run:     func(*invocation, []string) int { runFileContext(); return 0 }},
		{path: []string{"hook", "capture-session"}, legacy: "--capture-session", group: groupHooks, args: "[transcript.jsonl]", maxArgs: 1,
			summary: "SessionEnd: distil a finished session into memories (needs CORTEXDB_LLM_*)",
			flags:   []flagSpec{dryRunFlag},
			run:     void(runCaptureSession)},

		{path: []string{"completion"}, group: groupShell, args: "bash|zsh|fish", minArgs: 1, maxArgs: 1,
			summary: "print a shell completion script", run: runCompletion},
		{path: []string{"help"}, group: groupShell, args: "[command]", maxArgs: -1,
			summary: "show help for a command", anyArgs: true, run: runHelp},
	}
}

// lookupCommand finds the command args start with, longest path first, and
// returns the arguments after it.
func lookupCommand(args []string) (*command, []string) {
	var best *command
	for _, c := range commands {
		if len(c.path) > len(args) {
			continue
		}
		match := true
		for i, p := range c.path {
			if args[i] != p {
				match = false
				break
			}
		}
		if match && (best == nil || len(c.path) > len(best.path)) {
			best = c
		}
	}
	if best == nil {
		return nil, args
	}
	return best, args[len(best.path):]
}

func lookupLegacy(flag string) *command {
	if flag == "-version" {
		flag = "--version"
	}
	for _, c := range commands {
		if c.legacy != "" && c.legacy == flag {
			return c
		}
	}
	return nil
}

// groupNames are the first words that only head subcommands (graph, memory
// ...), so `cortexdb graph` can list them.
func subcommandsOf(word string) []*command {
	var out []*command
	for _, c := range commands {
		if len(c.path) > 1 && c.path[0] == word {
			out = append(out, c)
		}
	}
	return out
}
