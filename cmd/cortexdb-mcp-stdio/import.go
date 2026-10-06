package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// openBrainWriter opens whichever brain this process is configured for: the
// shared brain when CORTEXDB_REMOTE is set, the local file otherwise. db is
// nil for the shared brain.
func openBrainWriter() (w brainClient, label string, db *cortexdb.DB, closeFn func(), err error) {
	if addr, token, ok := remoteConfigured(); ok {
		conn, derr := dialCortexDB(addr, token)
		if derr != nil {
			return nil, "", nil, nil, fmt.Errorf("connect to %s: %w", addr, derr)
		}
		return remoteBrain{rpcv1.NewToolsServiceClient(conn)}, addr, nil, func() { _ = conn.Close() }, nil
	}
	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	db, err = openBrainDB(dbPath)
	if err != nil {
		return nil, "", nil, nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	return localBrain{db}, dbPath, db, func() { _ = db.Close() }, nil
}

// runImportAgentMemory is `--import-agent-memory`, used by
// /cortexdb-import-memory: the import_agent_memory tool from a shell, into the
// brain CORTEXDB_REMOTE / CORTEXDB_PATH names.
//
//	--import-agent-memory [--sessions] [--since 30d] [--max N] [--dry-run] [roots...]
//
// --sessions adds past Claude Code and Codex transcripts to the default
// sources. Bare arguments are roots to scan instead of ~/.claude and ~/.codex.
func runImportAgentMemory(args []string) {
	var in importAgentMemoryIn
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--sessions":
			in.Sources = append(append([]string{}, defaultImportSources...), srcClaudeSessions, srcCodexSessions)
		case "--dry-run":
			in.DryRun = true
		case "--since", "--max":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "cortexdb: %s needs a value\n", a)
				os.Exit(2)
			}
			i++
			if a == "--since" {
				in.Since = args[i]
			} else if n, err := strconv.Atoi(args[i]); err == nil {
				in.MaxSessions = n
			}
		default:
			in.Roots = append(in.Roots, a)
		}
	}

	w, label, db, closeFn, err := openBrainWriter()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: %v\n", err)
		os.Exit(1)
	}
	defer closeFn()
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: resolve home: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	run := agentImport{w: w, brain: label, llm: newOrganizeLLM(), home: home, state: loadSessionState(sessionStatePath(home)), now: time.Now()}
	out, err := run.run(ctx, in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: import agent memory: %v\n", err)
		os.Exit(1)
	}
	verb := "imported"
	if in.DryRun {
		verb = "would import"
	}
	fmt.Printf("%s into %s:\n", verb, out.Brain)
	for _, s := range out.Sources {
		n := s.Imported
		if in.DryRun {
			n = s.Found
		}
		fmt.Printf("  %-16s %d", s.Source, n)
		if s.Skipped > 0 {
			fmt.Printf("  (skipped %d)", s.Skipped)
		}
		if s.Pending > 0 && !in.DryRun {
			fmt.Printf("  (%d sessions still to go)", s.Pending)
		}
		fmt.Println()
		if s.Note != "" {
			fmt.Printf("    %s\n", s.Note)
		}
	}
	if out.Next != "" {
		fmt.Println("run it again to distil the next sessions")
	}
	if in.DryRun || db == nil {
		// The shared brain organizes what it is given on its own host.
		return
	}

	// Organize the imported memory into the knowledge graph (entities +
	// relations) so it is graph-queryable, not just lexically searchable. With
	// CORTEXDB_LLM_* set, an LLM distills clean, typed entities and relations;
	// otherwise it is deterministic (no LLM). Non-fatal.
	llm := newOrganizeLLM()
	if llm != nil {
		fmt.Fprintln(os.Stderr, "cortexdb: organizing imported memory with LLM distillation (CORTEXDB_LLM_*)")
	}
	if org, oerr := graphflow.OrganizeFromBrain(ctx, db, graphflow.OrganizeOptions{
		IncludeMemories:  true,
		IncludeKnowledge: true,
		LLM:              llm,
	}); oerr != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: organize into graph: %v\n", oerr)
	} else if org != nil {
		fmt.Printf("organized into graph: %d new entities, %d relations (from %d texts)\n",
			org.EntityCount, org.RelationCount, org.DocumentsScanned)
	}
}
