package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// runSyncMemory writes a directory of memory Markdown files back into the brain,
// completing the loop --export-memory opens.
//
// `--sync-memory [dir] [--prune] [--dry-run]` (default ~/.cortexdb/memory-export).
// Without --prune it only adds and edits, so a sync can never lose a memory; with
// it the directory becomes the source of truth and deleting a memory is deleting
// a file. Applies to the shared brain when CORTEXDB_REMOTE is set, because that
// is where the memories being edited actually live.
func runSyncMemory(args []string) {
	dir := ""
	prune := false
	dryRun := false
	for _, a := range args {
		switch {
		case a == "--prune":
			prune = true
		case a == "--dry-run":
			dryRun = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "cortexdb: --sync-memory: unknown flag %q\n", a)
			os.Exit(2)
		case strings.TrimSpace(a) != "":
			dir = a
		}
	}
	if dir == "" {
		dir = defaultViewDir("memory-export")
	}

	ctx := context.Background()
	current, source := loadAllMemories(ctx)

	plan, err := cortexdb.PlanMemorySync(current, cortexdb.MemorySyncOptions{Dir: dir, Prune: prune})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s -> %s\n", dir, source)
	fmt.Printf("scanned %d, create %d, update %d, delete %d, unchanged %d\n",
		plan.Scanned, len(plan.Create), len(plan.Update), len(plan.Delete), plan.Unchanged)
	// Name what would go, always. A count is not enough to approve a deletion by.
	for _, id := range plan.Delete {
		fmt.Printf("  delete %s\n", id)
	}
	for _, r := range plan.Create {
		fmt.Printf("  create %s\n", r.MemoryID)
	}
	for _, r := range plan.Update {
		fmt.Printf("  update %s\n", r.MemoryID)
	}
	if !prune {
		fmt.Println("note: --prune is off, so memories missing from the directory were left alone")
	}
	if dryRun {
		fmt.Println("dry run: nothing was written")
		return
	}
	if plan.Empty() {
		fmt.Println("already in sync")
		return
	}

	var report *cortexdb.MemorySyncReport
	if addr, token, ok := remoteConfigured(); ok {
		report, err = applyMemorySyncRemote(ctx, addr, token, plan)
	} else {
		report, err = applyMemorySyncLocal(ctx, plan)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("created %d, updated %d, deleted %d, unchanged %d\n",
		report.Created, report.Updated, report.Deleted, report.Unchanged)
}

func applyMemorySyncLocal(ctx context.Context, plan *cortexdb.MemorySyncPlan) (*cortexdb.MemorySyncReport, error) {
	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	db, err := openBrainDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()
	return cortexdb.ApplyMemorySync(ctx, db, plan)
}

// applyMemorySyncRemote replays the same plan against the shared brain through
// the memory_save / memory_delete tools.
func applyMemorySyncRemote(ctx context.Context, addr, token string, plan *cortexdb.MemorySyncPlan) (*cortexdb.MemorySyncReport, error) {
	conn, err := dialCortexDB(addr, token)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	client := rpcv1.NewToolsServiceClient(conn)

	call := func(name string, args any) error {
		payload, err := json.Marshal(args)
		if err != nil {
			return err
		}
		callCtx, cancel := context.WithTimeout(ctx, remoteDialTimeout)
		defer cancel()
		if _, err := client.CallTool(callCtx, &rpcv1.CallToolRequest{Name: name, ArgsJson: string(payload)}); err != nil {
			return fmt.Errorf("%s on %s: %w", name, addr, err)
		}
		return nil
	}

	report := &cortexdb.MemorySyncReport{Unchanged: plan.Unchanged}
	for _, req := range plan.Create {
		if err := call("memory_save", req); err != nil {
			return report, err
		}
		report.Created++
		report.IDs = append(report.IDs, req.MemoryID)
	}
	for _, req := range plan.Update {
		if err := call("memory_save", req); err != nil {
			return report, err
		}
		report.Updated++
		report.IDs = append(report.IDs, req.MemoryID)
	}
	for _, id := range plan.Delete {
		if err := call("memory_delete", cortexdb.MemoryDeleteRequest{MemoryID: id}); err != nil {
			return report, err
		}
		report.Deleted++
	}
	return report, nil
}
