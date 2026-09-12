package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/liveview"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// The one-shot views (--memory-html, --export-memory) render every stored
// memory. On a machine using a shared brain the local file is the wrong
// database — frozen at whenever that machine switched over — so these fetch
// from the remote instead when CORTEXDB_REMOTE is set.

// remoteConfigured reports whether this process should read the shared brain
// rather than a local file.
func remoteConfigured() (addr, token string, ok bool) {
	addr = strings.TrimSpace(os.Getenv("CORTEXDB_REMOTE"))
	return addr, os.Getenv("CORTEXDB_GRPC_TOKEN"), addr != ""
}

// fetchAllMemoriesRemote pulls every memory from the shared brain, one page
// at a time. A single call cannot do it: a brain past a few thousand records
// exceeds the 4 MiB gRPC message limit, and the failure is total — this is
// the mode that produces a backup file, so returning part of one silently
// would be worse than an error.
//
// Unlike the recall hook, a failure here is loud: these modes are invoked by
// hand and produce a file, so quietly rendering an empty or stale dashboard
// would be worse than an error the caller can read.
func fetchAllMemoriesRemote(ctx context.Context, addr, token string, limit int) ([]cortexdb.MemoryRecord, error) {
	conn, err := dialCortexDB(addr, token)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	const pageSize = 500
	var (
		all    []cortexdb.MemoryRecord
		cursor string
	)
	for page := 0; ; page++ {
		if page > 10_000 {
			return nil, fmt.Errorf("memory export did not terminate after %d pages", page)
		}
		args, err := json.Marshal(cortexdb.MemoryListAllRequest{Limit: pageSize, Cursor: cursor})
		if err != nil {
			return nil, err
		}

		callCtx, cancel := context.WithTimeout(ctx, remoteDialTimeout)
		resp, err := rpcv1.NewToolsServiceClient(conn).CallTool(callCtx, &rpcv1.CallToolRequest{
			Name:     "memory_list_all",
			ArgsJson: string(args),
		})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("memory_list_all on %s: %w", addr, err)
		}

		var out cortexdb.MemoryListAllResponse
		if err := json.Unmarshal([]byte(resp.GetResultJson()), &out); err != nil {
			return nil, fmt.Errorf("decode memory_list_all from %s: %w", addr, err)
		}
		all = append(all, out.Memories...)

		if limit > 0 && len(all) >= limit {
			return all[:limit], nil
		}
		if !out.Truncated {
			return all, nil
		}
		if out.NextCursor == "" {
			// An older server: it truncated and cannot say where to resume.
			return nil, fmt.Errorf(
				"memory_list_all on %s stopped at %d records without a cursor; "+
					"this server predates paged listings and cannot export a brain this size",
				addr, len(all))
		}
		cursor = out.NextCursor
	}
}

// loadAllMemories returns every memory, from the shared brain when one is
// configured and from the local database otherwise, plus a label naming which
// one it read so the mode's own output can say where the data came from.
//
// Exits on failure: these modes are run by hand and write a file, so rendering
// an empty or stale view would be worse than an error the caller can read.
func loadAllMemories(ctx context.Context) ([]cortexdb.MemoryRecord, string) {
	if addr, token, ok := remoteConfigured(); ok {
		memories, err := fetchAllMemoriesRemote(ctx, addr, token, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: %v\n", err)
			os.Exit(1)
		}
		return memories, "shared brain " + addr
	}

	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	db, err := openBrainDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	memories, err := db.ListAllMemories(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: list memories: %v\n", err)
		os.Exit(1)
	}
	return memories, dbPath
}

// defaultViewDir picks where a one-shot view writes when the caller gave no
// path. It cannot be derived from the database's directory any more: in shared
// mode there is no local database file.
func defaultViewDir(name string) string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cortexdb", name)
	}
	return filepath.Join(".", name)
}

// fetchGraphRemote and clipLabel live with the live view, which polls them; the
// static renderer calls through so both read the shared brain the same way.
func fetchGraphRemote(ctx context.Context, addr, token string, limit int, quiet bool) ([]graphNodeView, []graphEdgeView, error) {
	return liveview.LoadRemote(ctx, addr, token, limit, quiet)
}

func clipLabel(s string) string { return liveview.ClipLabel(s) }
