package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// sweepExpiredCommand removes every document and memory whose expires_at has
// passed, or with --dry-run only lists them. Meant for a timer (cron,
// launchd) on the machine that holds the brain: searches already leave the
// expired rows out, so the sweep can run at any hour. It reads the brain
// named by CORTEXDB_PATH, writes the report, and returns the exit code
// (0 done, 1 failed, 2 bad usage). With CORTEXDB_REMOTE set it sweeps the
// shared brain instead, through the sweep_expired tool.
func sweepExpiredCommand(args []string, stdout, stderr io.Writer) int {
	dryRun, asJSON := false, false
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		case "--json":
			asJSON = true
		default:
			fmt.Fprintln(stderr, "usage: cortexdb-mcp-stdio --sweep-expired [--dry-run] [--json]")
			return 2
		}
	}

	// A client of a shared brain sweeps that brain, through the sweep_expired
	// tool over gRPC: the local file, if any, is not what the server serves.
	if addr, token, ok := remoteConfigured(); ok {
		return sweepExpiredRemote(addr, token, dryRun, asJSON, stdout, stderr)
	}
	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	db, err := openBrainDB(dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "cortexdb: open %s: %v\n", dbPath, err)
		return 1
	}
	defer func() { _ = db.Close() }()

	report, err := db.SweepExpired(context.Background(), time.Now(), dryRun)
	if report != nil {
		writeSweepReport(stdout, report, asJSON)
	}
	if err != nil {
		fmt.Fprintf(stderr, "cortexdb: sweep expired: %v\n", err)
		return 1
	}
	return 0
}

// sweepExpiredRemote runs the sweep on the shared brain at addr and writes
// its report, with the same exit codes as the local sweep.
func sweepExpiredRemote(addr, token string, dryRun, asJSON bool, stdout, stderr io.Writer) int {
	conn, err := dialCortexDB(addr, token)
	if err != nil {
		fmt.Fprintf(stderr, "cortexdb: %v\n", err)
		return 1
	}
	defer func() { _ = conn.Close() }()
	args, _ := json.Marshal(cortexdb.SweepExpiredToolRequest{DryRun: dryRun})
	ctx, cancel := context.WithTimeout(context.Background(), remoteSweepTimeout)
	defer cancel()
	resp, err := rpcv1.NewToolsServiceClient(conn).CallTool(ctx, &rpcv1.CallToolRequest{Name: "sweep_expired", ArgsJson: string(args)})
	if err != nil {
		fmt.Fprintf(stderr, "cortexdb: sweep expired on %s: %v\n", addr, err)
		return 1
	}
	var report cortexdb.ExpiredSweepReport
	if err := json.Unmarshal([]byte(resp.GetResultJson()), &report); err != nil {
		fmt.Fprintf(stderr, "cortexdb: sweep expired on %s: read report: %v\n", addr, err)
		return 1
	}
	writeSweepReport(stdout, &report, asJSON)
	return 0
}

// remoteSweepTimeout bounds one sweep over gRPC: a brain with many expired
// rows deletes them one by one, each with its chunks and nodes.
const remoteSweepTimeout = 10 * time.Minute

// writeSweepReport prints a sweep report, as JSON or as text.
func writeSweepReport(stdout io.Writer, report *cortexdb.ExpiredSweepReport, asJSON bool) {
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(report)
		return
	}
	fmt.Fprintf(stdout, "sweep expired: documents=%d memories=%d dryRun=%v\n",
		len(report.Documents), len(report.Memories), report.DryRun)
	for _, id := range report.Documents {
		fmt.Fprintf(stdout, "  document %s\n", id)
	}
	for _, id := range report.Memories {
		fmt.Fprintf(stdout, "  memory %s\n", id)
	}
}
