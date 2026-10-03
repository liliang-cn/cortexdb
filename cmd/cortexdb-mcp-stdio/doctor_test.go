package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func doctorEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("CORTEXDB_REMOTE", "")
	t.Setenv("CORTEXDB_GRPC_TOKEN", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "brain.db")
	t.Setenv("CORTEXDB_PATH", path)
	return path
}

func TestDoctorDoesNotCreateMissingBrain(t *testing.T) {
	path := doctorEnv(t)
	report, err := inspectBrain(context.Background())
	if err != nil || report.Status != "not_initialized" {
		t.Fatalf("report=%+v error=%v", report, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("doctor created a brain: %v", err)
	}
	if err := doctorSelfTest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("self-test touched configured brain")
	}
}

func TestDoctorExistingBrain(t *testing.T) {
	path := doctorEnv(t)
	db, err := cortexdb.Open(cortexdb.DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := inspectBrain(context.Background())
	if err != nil || report.Status != "ready" {
		t.Fatalf("report=%+v error=%v", report, err)
	}
}

func TestDoctorRemoteWithoutLocalBrain(t *testing.T) {
	path := doctorEnv(t)
	addr, _ := startTestBrain(t, "doctor-secret")
	t.Setenv("CORTEXDB_REMOTE", addr)
	t.Setenv("CORTEXDB_GRPC_TOKEN", "doctor-secret")
	report, err := inspectBrain(context.Background())
	if err != nil || report.Tools == 0 || report.Mode != "remote" {
		t.Fatalf("report=%+v error=%v", report, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("remote diagnosis created local brain")
	}
	t.Setenv("CORTEXDB_GRPC_TOKEN", "bad-secret")
	if _, err := inspectBrain(context.Background()); err == nil {
		t.Fatal("wrong token accepted")
	}
}

func TestRecallLauncherRemoteWithoutLocalBrain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher")
	}
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local_missing", true: "remote_missing_local"}[remote], func(t *testing.T) {
			doctorEnv(t)
			bin := filepath.Join(t.TempDir(), "fake-mcp")
			if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'invoked:%s' \"$1\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CORTEXDB_MCP_BIN", bin)
			if remote {
				t.Setenv("CORTEXDB_REMOTE", "127.0.0.1:47821")
			}
			hook := filepath.Join("..", "..", "plugins", "cortexdb", "bin", "cortexdb-recall")
			enable := exec.Command("sh", hook, "--enable")
			if out, err := enable.CombinedOutput(); err != nil {
				t.Fatalf("enable: %s %v", out, err)
			}
			cmd := exec.Command("sh", hook)
			cmd.Stdin = strings.NewReader(`{"prompt":"test"}`)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("hook: %s %v", out, err)
			}
			if remote && string(out) != "invoked:--recall" {
				t.Fatalf("remote skipped: %s", out)
			}
			if !remote && len(out) != 0 {
				t.Fatalf("missing local DB should be silent: %s", out)
			}
		})
	}
}
