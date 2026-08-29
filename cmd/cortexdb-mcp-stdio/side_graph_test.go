package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A graph name becomes a filename. Everything about that is dangerous, so the
// rule is an allowlist rather than a blocklist: a caller cannot reach out of the
// graphs directory, and cannot name a graph something a shell or a filesystem
// will read as anything but a name.
func TestSideGraphNameRejectsAnythingThatIsNotAName(t *testing.T) {
	for _, bad := range []string{
		"", " ", "..", "../escape", "a/b", "a\\b", "a.db", ".hidden",
		"UPPER", "has space", "has\x00null", strings.Repeat("x", 65),
		"-leading", "_leading",
	} {
		if err := validSideGraphName(bad); err == nil {
			t.Errorf("%q was accepted as a graph name", bad)
		}
	}
}

func TestSideGraphNameAcceptsPlainNames(t *testing.T) {
	for _, ok := range []string{"runs", "run-2026-08-29", "plan_a", "r1", strings.Repeat("x", 64)} {
		if err := validSideGraphName(ok); err != nil {
			t.Errorf("%q was rejected: %v", ok, err)
		}
	}
}

// The path must land inside the graphs directory for every name that passes,
// which is the property the allowlist exists to guarantee.
func TestSideGraphPathStaysInsideItsDirectory(t *testing.T) {
	root := t.TempDir()
	p, err := sideGraphPath(root, "run-1")
	if err != nil {
		t.Fatalf("sideGraphPath: %v", err)
	}
	if filepath.Dir(p) != filepath.Join(root, "graphs") {
		t.Errorf("path %s is not in %s/graphs", p, root)
	}
	if !strings.HasSuffix(p, "run-1.db") {
		t.Errorf("path %s does not name the graph", p)
	}
}

// The whole point of a side graph: writing to it must not touch the brain, and
// reading it back must return exactly what went in — including a "next" edge,
// which is how a sequence of steps is written.
func TestSideGraphRoundTripsStepsAndTheirOrder(t *testing.T) {
	root := t.TempDir()
	reg := newSideGraphs(root)
	t.Cleanup(func() { reg.closeAll() })
	ctx := context.Background()

	written, err := reg.write(ctx, "run-1", sideGraphWriteIn{
		Graph: "run-1",
		Nodes: []sideNode{
			{Name: "read config", Type: "step"},
			{Name: "dial gateway", Type: "step"},
			{Name: "gateway answers in under 200ms", Type: "expectation"},
		},
		Edges: []sideEdge{
			{From: "read config", To: "dial gateway", Type: "next"},
			{From: "dial gateway", To: "gateway answers in under 200ms", Type: "expected"},
		},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if written.Nodes != 3 || written.Edges != 2 {
		t.Fatalf("wrote %d nodes / %d edges, want 3 / 2", written.Nodes, written.Edges)
	}

	got, err := reg.read(ctx, "run-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Nodes) != 3 {
		t.Errorf("read back %d nodes, want 3: %+v", len(got.Nodes), got.Nodes)
	}
	var sawNext bool
	for _, e := range got.Edges {
		if e.Label == "next" {
			sawNext = true
		}
	}
	if !sawNext {
		t.Errorf("the next edge did not survive the round trip: %+v", got.Edges)
	}
}

// Two graphs must not see each other; that is the only reason this exists.
func TestSideGraphsAreIsolatedFromEachOther(t *testing.T) {
	root := t.TempDir()
	reg := newSideGraphs(root)
	t.Cleanup(func() { reg.closeAll() })
	ctx := context.Background()

	if _, err := reg.write(ctx, "run-a", sideGraphWriteIn{
		Nodes: []sideNode{{Name: "only in a", Type: "step"}}}); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if _, err := reg.write(ctx, "run-b", sideGraphWriteIn{
		Nodes: []sideNode{{Name: "only in b", Type: "step"}}}); err != nil {
		t.Fatalf("write b: %v", err)
	}

	a, err := reg.read(ctx, "run-a")
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	for _, n := range a.Nodes {
		if strings.Contains(n.Label, "only in b") {
			t.Fatalf("run-a can see run-b's nodes: %+v", a.Nodes)
		}
	}

	names, err := reg.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("list = %v, want both graphs", names)
	}
}

// A run trace is disposable — that is most of why it is not in the brain.
func TestSideGraphDropRemovesIt(t *testing.T) {
	root := t.TempDir()
	reg := newSideGraphs(root)
	t.Cleanup(func() { reg.closeAll() })
	ctx := context.Background()

	if _, err := reg.write(ctx, "scratch", sideGraphWriteIn{
		Nodes: []sideNode{{Name: "x", Type: "step"}}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := reg.drop("scratch"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	names, err := reg.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("list = %v after drop, want empty", names)
	}
	// Dropping something that is not there is not an error worth failing a run
	// over; it is the state the caller asked for.
	if err := reg.drop("scratch"); err != nil {
		t.Errorf("dropping an absent graph errored: %v", err)
	}
}
