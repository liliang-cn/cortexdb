package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/liveview"
)

// Side graphs — a place to put a graph that is not the brain.
//
// The brain is what is known. A run's steps, the plan behind them and what each
// one was expected to do are none of that: they are what happened once, they are
// mostly wrong by tomorrow, and there are thousands of them. Writing them into
// shared knowledge buries the knowledge — a few thousand step nodes on top of a
// few thousand real ones and nobody can read the graph any more.
//
// So this is the mechanism and not the policy. CortexDB does not know what a
// "step" is, and nothing here mentions runs, plans or expectations except in
// this comment. It gives a caller more than one graph and gets out of the way;
// what belongs in which is the caller's decision.
//
// Two deliberate choices:
//
//   - A side graph is a separate database file, because graph_nodes and
//     graph_edges have no tenancy column. There is no namespace to scope by, so
//     "another graph" can only honestly mean another store. That also makes a
//     side graph disposable by deleting one file, which is most of the point.
//   - Side graphs are always LOCAL, even when the brain is shared. These tools
//     are registered here rather than proxied, so a scratch graph never travels
//     to the machine everyone else is reading. The brain is shared; your
//     working notes are yours.

// sideGraphNamePattern is an allowlist, not a blocklist. The name becomes a
// filename, so the question is not "which characters are dangerous" — that list
// is never finished — but "which characters are a name".
var sideGraphNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func validSideGraphName(name string) error {
	if !sideGraphNamePattern.MatchString(name) {
		return fmt.Errorf("graph name %q must be 1-64 characters of lowercase letters, digits, - and _, starting with a letter or digit", name)
	}
	return nil
}

// sideGraphPath is where a named graph lives.
func sideGraphPath(root, name string) (string, error) {
	if err := validSideGraphName(name); err != nil {
		return "", err
	}
	return filepath.Join(root, "graphs", name+".db"), nil
}

// sideNode and sideEdge are the write shapes. They are deliberately thinner
// than the brain's entity input: a side graph is written by code that already
// knows what it means, and every extra field is a decision this package would
// be making on that code's behalf.
type sideNode struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	// Note is free text carried on the node — what a step did, what an
	// expectation was, whatever the caller wants back when it reads.
	Note string `json:"note,omitempty"`
}

type sideEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type,omitempty"`
}

type sideGraphWriteIn struct {
	Graph string     `json:"graph"`
	Nodes []sideNode `json:"nodes,omitempty"`
	Edges []sideEdge `json:"edges,omitempty"`
}

type sideGraphWriteOut struct {
	Graph string `json:"graph"`
	Nodes int    `json:"nodes"`
	Edges int    `json:"edges"`
	Path  string `json:"path"`
}

type sideGraphReadOut struct {
	Graph string          `json:"graph"`
	Nodes []liveview.Node `json:"nodes"`
	Edges []liveview.Edge `json:"edges"`
}

// sideGraphs holds the open side graphs for one process.
//
// Opened lazily and kept open: a caller writing a trace writes many times, and
// reopening a SQLite file per call would be the slowest part of the operation.
type sideGraphs struct {
	root string
	mu   sync.Mutex
	open map[string]*cortexdb.DB
}

func newSideGraphs(root string) *sideGraphs {
	return &sideGraphs{root: root, open: make(map[string]*cortexdb.DB)}
}

// get opens a side graph, creating it on first write.
//
// No embedder is attached. CortexDB runs lexically without one, and a side
// graph is read by walking it rather than by similarity — wiring up a model
// here would add a network dependency to writing down what just happened.
func (s *sideGraphs) get(ctx context.Context, name string) (*cortexdb.DB, error) {
	path, err := sideGraphPath(s.root, name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if db, ok := s.open[name]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create graphs directory: %w", err)
	}
	db, err := cortexdb.Open(cortexdb.DefaultConfig(path))
	if err != nil {
		return nil, fmt.Errorf("open graph %q: %w", name, err)
	}
	s.open[name] = db
	return db, nil
}

func (s *sideGraphs) write(ctx context.Context, name string, in sideGraphWriteIn) (sideGraphWriteOut, error) {
	if strings.TrimSpace(name) == "" {
		name = in.Graph
	}
	db, err := s.get(ctx, name)
	if err != nil {
		return sideGraphWriteOut{}, err
	}
	tools := db.GraphRAGTools()

	out := sideGraphWriteOut{Graph: name}
	if path, perr := sideGraphPath(s.root, name); perr == nil {
		out.Path = path
	}

	if len(in.Nodes) > 0 {
		entities := make([]cortexdb.ToolEntityInput, 0, len(in.Nodes))
		for _, n := range in.Nodes {
			if strings.TrimSpace(n.Name) == "" {
				continue
			}
			entities = append(entities, cortexdb.ToolEntityInput{
				Name:        n.Name,
				Type:        n.Type,
				Description: n.Note,
			})
		}
		resp, uerr := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: entities})
		if uerr != nil {
			return out, fmt.Errorf("write nodes to %q: %w", name, uerr)
		}
		if resp != nil {
			out.Nodes = len(resp.EntityNodeIDs)
		}
	}

	if len(in.Edges) > 0 {
		relations := make([]cortexdb.ToolRelationInput, 0, len(in.Edges))
		for _, e := range in.Edges {
			if strings.TrimSpace(e.From) == "" || strings.TrimSpace(e.To) == "" {
				continue
			}
			relations = append(relations, cortexdb.ToolRelationInput{From: e.From, To: e.To, Type: e.Type})
		}
		resp, rerr := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: relations})
		if rerr != nil {
			return out, fmt.Errorf("write edges to %q: %w", name, rerr)
		}
		if resp != nil {
			// Written, not len(EdgeIDs): an edge whose endpoints do not exist
			// is rejected by the store, and reporting the request size would
			// tell the caller a link was drawn that is not there.
			out.Edges = resp.Written
		}
	}
	return out, nil
}

func (s *sideGraphs) read(ctx context.Context, name string) (sideGraphReadOut, error) {
	db, err := s.get(ctx, name)
	if err != nil {
		return sideGraphReadOut{}, err
	}
	nodes, edges, lerr := liveview.LoadLocal(ctx, db.SQL())
	if lerr != nil {
		return sideGraphReadOut{}, fmt.Errorf("read graph %q: %w", name, lerr)
	}
	return sideGraphReadOut{Graph: name, Nodes: nodes, Edges: edges}, nil
}

// list names the graphs on disk, not the ones this process happens to have
// open — a caller asking what exists means on disk.
func (s *sideGraphs) list() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "graphs"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".db")
		if validSideGraphName(name) == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// drop deletes a side graph. Absent is not an error: the caller asked for the
// graph to be gone, and it is.
func (s *sideGraphs) drop(name string) error {
	path, err := sideGraphPath(s.root, name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if db, ok := s.open[name]; ok {
		_ = db.Close()
		delete(s.open, name)
	}
	s.mu.Unlock()
	// SQLite leaves a -wal and a -shm beside the database; removing only the
	// database leaves the other two to be adopted by the next graph of the
	// same name.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if rerr := os.Remove(path + suffix); rerr != nil && !os.IsNotExist(rerr) {
			return fmt.Errorf("remove %s: %w", path+suffix, rerr)
		}
	}
	return nil
}

func (s *sideGraphs) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, db := range s.open {
		_ = db.Close()
		delete(s.open, name)
	}
}

// source adapts a side graph to what a live view reads.
//
// The database stays owned by the registry rather than the view: several views
// and the write tools all read the same graph, and handing one of them a Close
// that shuts the file would break the others.
func (s *sideGraphs) source(ctx context.Context, name string) (*liveview.Source, error) {
	db, err := s.get(ctx, name)
	if err != nil {
		return nil, err
	}
	return &liveview.Source{
		Describe: "side graph " + name,
		Read: func(ctx context.Context) ([]liveview.Node, []liveview.Edge, error) {
			return liveview.LoadLocal(ctx, db.SQL())
		},
		Close: func() error { return nil },
	}, nil
}

// sideGraphs_ is the process's registry, shared by the tools and the views.
var sideGraphs_ = newSideGraphs(sideGraphRoot())
