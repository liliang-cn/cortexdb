package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type doctorReport struct {
	Version         string `json:"version"`
	Mode            string `json:"mode"`
	Database        string `json:"database,omitempty"`
	Remote          string `json:"remote,omitempty"`
	TokenConfigured bool   `json:"token_configured"`
	AutoRecall      string `json:"auto_recall"`
	HookTrust       string `json:"hook_trust"`
	Status          string `json:"status"`
	Tools           int    `json:"tools,omitempty"`
	SelfTest        string `json:"self_test,omitempty"`
	Error           string `json:"error,omitempty"`
}

func runDoctor(args []string) error {
	selfTest := false
	for _, arg := range args {
		if arg != "--self-test" {
			return fmt.Errorf("unknown doctor option %q", arg)
		}
		selfTest = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteDialTimeout)
	defer cancel()
	report, err := inspectBrain(ctx)
	if selfTest {
		testCtx, testCancel := context.WithTimeout(context.Background(), remoteDialTimeout)
		defer testCancel()
		if testErr := doctorSelfTest(testCtx); testErr != nil {
			report.SelfTest = "failed"
			if err == nil {
				err = testErr
			}
		} else {
			report.SelfTest = "passed"
		}
	}
	if err != nil {
		report.Status = "failed"
		report.Error = err.Error()
	}
	if encodeErr := json.NewEncoder(os.Stdout).Encode(report); encodeErr != nil {
		return encodeErr
	}
	return err
}

func inspectBrain(ctx context.Context) (doctorReport, error) {
	report := doctorReport{Version: cortexdbroot.Version, Mode: "local", Status: "ready", AutoRecall: "not_configured", HookTrust: "review in Codex /hooks; not detectable by the server"}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, _ := os.UserHomeDir()
		cache = filepath.Join(home, ".cache")
	}
	if data, err := os.ReadFile(filepath.Join(cache, "cortexdb", "autorecall")); err == nil {
		report.AutoRecall = "disabled"
		if strings.HasPrefix(string(data), "on") {
			report.AutoRecall = "enabled"
		}
	}
	if remote := strings.TrimSpace(os.Getenv("CORTEXDB_REMOTE")); remote != "" {
		report.Mode, report.Remote = "remote", remote
		token := os.Getenv("CORTEXDB_GRPC_TOKEN")
		report.TokenConfigured = token != ""
		conn, err := dialCortexDB(remote, token)
		if err != nil {
			return report, err
		}
		defer conn.Close()
		list, err := rpcv1.NewToolsServiceClient(conn).ListTools(ctx, &rpcv1.ListToolsRequest{})
		if err != nil {
			return report, fmt.Errorf("remote tool discovery failed: %w", err)
		}
		report.Tools = len(list.GetTools())
		if report.Tools == 0 {
			return report, fmt.Errorf("remote server exposed no tools")
		}
		return report, nil
	}
	path := os.Getenv("CORTEXDB_PATH")
	if path == "" {
		path = cortexdb.DefaultDBPath()
	}
	if strings.HasPrefix(path, "postgres://") || strings.HasPrefix(path, "postgresql://") {
		report.Mode = "postgres"
		return report, fmt.Errorf("direct PostgreSQL diagnostics are not supported; configure CORTEXDB_REMOTE to diagnose a shared-brain server")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return report, err
	}
	report.Database = absolute
	if _, err := os.Stat(absolute); os.IsNotExist(err) {
		report.Status = "not_initialized"
		return report, nil
	} else if err != nil {
		return report, err
	}
	// Read-only URI prevents diagnostics from creating or migrating a brain.
	uri := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return report, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return report, err
	}
	if integrity != "ok" {
		return report, fmt.Errorf("SQLite integrity check: %s", integrity)
	}
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('messages', 'embeddings')").Scan(&tables); err != nil {
		return report, err
	}
	if tables != 2 {
		return report, fmt.Errorf("database is missing CortexDB memory/storage tables")
	}
	return report, nil
}

func doctorSelfTest(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "cortexdb-doctor-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "brain.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(path))
	if err != nil {
		return err
	}
	_, err = db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: "doctor", Content: "CortexDB diagnostic editor is Neovim."})
	closeErr := db.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	db, err = cortexdb.Open(cortexdb.DefaultConfig(path))
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := db.SearchMemory(ctx, cortexdb.MemorySearchRequest{Query: "diagnostic editor Neovim", RetrievalMode: cortexdb.RetrievalModeLexical})
	if err != nil {
		return err
	}
	if len(result.Results) == 0 || result.Results[0].Memory.ID != "doctor" || result.Results[0].Memory.Content != "CortexDB diagnostic editor is Neovim." {
		return fmt.Errorf("self-test memory was not recalled after reopening")
	}
	return nil
}
