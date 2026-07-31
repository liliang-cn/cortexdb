package main

import (
	"context"
	"log"
	"os"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	// `--recall` is a one-shot mode for the UserPromptSubmit hook: read a hook
	// payload from stdin, retrieve matching memories, print additionalContext,
	// and exit. Everything else launches the long-running MCP stdio server.
	if len(os.Args) > 1 && os.Args[1] == "--recall" {
		runRecall()
		return
	}

	// `--graph-html [out]` is a one-shot mode that renders the brain's knowledge
	// graph to a self-contained, interactive HTML file and prints its path. Used
	// by the /cortexdb-graph command to give the memory a viewable graph.
	if len(os.Args) > 1 && os.Args[1] == "--graph-html" {
		out := ""
		if len(os.Args) > 2 {
			out = os.Args[2]
		}
		runGraphHTML(out)
		return
	}

	// `--memory-html [out]` renders every stored memory to a self-contained,
	// interactive HTML dashboard (cards grouped by scope, live search) and prints
	// its path. Used by /cortexdb-memory-view.
	if len(os.Args) > 1 && os.Args[1] == "--memory-html" {
		out := ""
		if len(os.Args) > 2 {
			out = os.Args[2]
		}
		runMemoryHTML(out)
		return
	}

	// `--import-agent-memory [roots...]` imports Claude Code / Codex memory into
	// the brain. Used by /cortexdb-import-memory.
	if len(os.Args) > 1 && os.Args[1] == "--import-agent-memory" {
		runImportAgentMemory(os.Args[2:])
		return
	}

	// Learning-graph modes: study material as a prerequisite graph.
	// `--import-learning-graph [file.json]` ingests concepts + prerequisites;
	// `--learn-path "<concept>"` prints an ordered study plan;
	// `--learn-next [limit]` prints what is ready to study now;
	// `--learn-mastered <concept>...` records mastery.
	if len(os.Args) > 1 && os.Args[1] == "--import-learning-graph" {
		runImportLearningGraph(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--learn-path" {
		runLearnPath(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--learn-next" {
		runLearnNext(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--learn-mastered" {
		runLearnMastered(os.Args[2:])
		return
	}

	// `--graph-update [file] [--dry-run] [--allow-delete]` reconciles new text
	// against the existing graph with an LLM, applying add/update/DELETE edits.
	// Needs only CORTEXDB_LLM_* (no embedder). Used by /cortexdb-graph-update.
	if len(os.Args) > 1 && os.Args[1] == "--graph-update" {
		runGraphUpdate(os.Args[2:])
		return
	}

	// `--multi-hop "<question>"` answers a complex, multi-step question via
	// iterative agentic retrieval (retrieve → reason → retrieve). Requires an
	// LLM (CORTEXDB_LLM_*). Used by /cortexdb-multi-hop.
	if len(os.Args) > 1 && os.Args[1] == "--multi-hop" {
		runMultiHop(os.Args[2:])
		return
	}

	// `--facts-as-of [RFC3339] [--from NAME] [--type TYPE]` prints the temporal
	// facts valid at an instant (default now) — an as-of view of the bitemporal
	// knowledge graph. Used by /cortexdb-facts-asof.
	if len(os.Args) > 1 && os.Args[1] == "--facts-as-of" {
		runFactsAsOf(os.Args[2:])
		return
	}

	// `--resolve-entities [--dry-run]` merges duplicate/alias entity nodes into
	// canonical ones (deterministic; LLM acronym/synonym detection when
	// CORTEXDB_LLM_* is set). Used by /cortexdb-resolve-entities.
	if len(os.Args) > 1 && os.Args[1] == "--resolve-entities" {
		runResolveEntities(os.Args[2:])
		return
	}

	// `--global-search "<question>"` answers a whole-corpus question via
	// community detection + summaries + map-reduce (GraphRAG global search).
	// Used by /cortexdb-global-search.
	if len(os.Args) > 1 && os.Args[1] == "--global-search" {
		runGlobalSearch(os.Args[2:])
		return
	}

	// `--export-memory [outdir]` writes every stored memory to Markdown files
	// (one per memory + a MEMORY.md index), mirroring Claude Code's memory
	// layout. Used by /cortexdb-export-memory.
	if len(os.Args) > 1 && os.Args[1] == "--export-memory" {
		runExportMemory(os.Args[2:])
		return
	}

	// `--import-code-graph [file.json]` ingests a language-agnostic code-graph
	// JSON (from stdin when no path) into the knowledge graph. The extractor is
	// an LLM/agent (e.g. Claude Code), so it works for any language. Used by
	// /cortexdb-import-code.
	if len(os.Args) > 1 && os.Args[1] == "--import-code-graph" {
		runImportCodeGraph(os.Args[2:])
		return
	}

	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}

	db, err := openBrainDB(dbPath)
	if err != nil {
		log.Fatalf("open cortexdb: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close cortexdb: %v", closeErr)
		}
	}()

	if err := db.RunMCPStdio(context.Background(), cortexdb.MCPServerOptions{}); err != nil {
		log.Fatalf("run mcp stdio server: %v", err)
	}
}
