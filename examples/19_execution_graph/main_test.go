package main

import (
	"context"
	"testing"
)

// The example runs end to end, so CI catches it drifting from the library it
// demonstrates, not just failing to compile.

func TestDemoRuns(t *testing.T) {
	if err := runDemo(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("demo: %v", err)
	}
}

func TestBenchRuns(t *testing.T) {
	if err := runBench(context.Background(), t.TempDir(), 40, 16); err != nil {
		t.Fatalf("bench: %v", err)
	}
}
