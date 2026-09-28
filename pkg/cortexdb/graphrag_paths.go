package cortexdb

// Path retrieval: the evidence for a multi-hop answer as chains of facts.
//
// Chunk retrieval ranks passages by how much they look like the question, and
// for a question whose answer is a chain that is the wrong signal twice over:
// the passage stating the middle link ("Borealis Labs built Kestrel") shares
// few words with "how is Alice Chen connected to Kestrel?", while a passage
// that shares many ("Alice Chen reviewed a paper on the Kestrel launch")
// states nothing the answer needs. search_paths walks the graph between the
// entities the question names and returns the chains that connect them, each
// edge carrying the chunk it was extracted from — so what comes back is the
// evidence, link by link, and the text behind every link.

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// ToolSearchPathsRequest asks for paths between seed entities.
type ToolSearchPathsRequest struct {
	// EntityNames are resolved to nodes: the entity id a writer would have
	// derived from the name first, then find_nodes' exact and case-folded
	// matches. A containment match is never used as a seed.
	EntityNames []string `json:"entity_names,omitempty"`
	// NodeIDs are used as given.
	NodeIDs []string `json:"node_ids,omitempty"`
	// MaxDepth is the longest path in edges. Default 3, capped at 6.
	MaxDepth int `json:"max_depth,omitempty"`
	// Direction: "both" (default), "out", "in".
	Direction string `json:"direction,omitempty"`
	// EdgeTypes restricts paths to these relation types.
	EdgeTypes []string `json:"edge_types,omitempty"`
	// RelationPolicies weights and depth-caps relation types; the key "*"
	// covers every unlisted type.
	RelationPolicies graph.RelationPolicies `json:"relation_policies,omitempty"`
	// Decay is the per-hop score factor. Default 0.5.
	Decay float64 `json:"decay,omitempty"`
	// UseEdgeWeights multiplies in each edge's stored weight.
	UseEdgeWeights bool `json:"use_edge_weights,omitempty"`
	// MaxPaths caps the paths returned. Default 10.
	MaxPaths int `json:"max_paths,omitempty"`
	// MaxExpansions caps the edges examined. Default 5000.
	MaxExpansions int `json:"max_expansions,omitempty"`
	// IncludeBookkeeping lets paths pass through chunk/document nodes.
	IncludeBookkeeping bool `json:"include_bookkeeping,omitempty"`
	// WithText loads the text of every cited chunk.
	WithText bool `json:"with_text,omitempty"`
}

// PathSeed is one seed and how it was resolved.
type PathSeed struct {
	Name   string `json:"name,omitempty"`
	NodeID string `json:"node_id"`
	// Match is "id" (given as an id), "derived" (the writer's id for this
	// name), "exact" or "fold" (find_nodes).
	Match string `json:"match"`
}

// PathEdge is one link of a retrieved path, with where it came from.
type PathEdge struct {
	ID         string   `json:"id"`
	From       string   `json:"from"`
	FromName   string   `json:"from_name"`
	To         string   `json:"to"`
	ToName     string   `json:"to_name"`
	Type       string   `json:"type"`
	Weight     float64  `json:"weight"`
	ChunkIDs   []string `json:"chunk_ids,omitempty"`
	DocumentID string   `json:"document_id,omitempty"`
	Inferred   bool     `json:"inferred,omitempty"`
}

// RetrievedPath is one chain of facts, best first in its response.
type RetrievedPath struct {
	From  string  `json:"from"`
	To    string  `json:"to"`
	Hops  int     `json:"hops"`
	Score float64 `json:"score"`
	// Sentence reads the path out, one clause per edge.
	Sentence string     `json:"sentence"`
	Edges    []PathEdge `json:"edges"`
}

// ToolSearchPathsResponse is what search_paths returns.
type ToolSearchPathsResponse struct {
	Seeds []PathSeed `json:"seeds"`
	// Unresolved names found no node. A path search with one resolved seed
	// returns open paths from it; with none it returns nothing.
	Unresolved []string        `json:"unresolved,omitempty"`
	Paths      []RetrievedPath `json:"paths"`
	// ChunkIDs is every chunk cited by the returned paths, in path order,
	// deduplicated — the evidence set, ready for get_chunks or build_context.
	ChunkIDs []string    `json:"chunk_ids,omitempty"`
	Chunks   []ToolChunk `json:"chunks,omitempty"`
	// Truncated means max_expansions stopped the search: a missing path is
	// then not evidence that none exists.
	Truncated  bool `json:"truncated,omitempty"`
	Expansions int  `json:"expansions"`
}

// SearchPaths finds scored paths between the entities a question names and
// returns them as edge chains that cite their source chunks.
func (db *DB) SearchPaths(ctx context.Context, req ToolSearchPathsRequest) (*ToolSearchPathsResponse, error) {
	return db.GraphRAGTools().SearchPaths(ctx, req)
}

// SearchPaths is the toolbox form of DB.SearchPaths.
func (t *GraphRAGToolbox) SearchPaths(ctx context.Context, req ToolSearchPathsRequest) (*ToolSearchPathsResponse, error) {
	seeds, unresolved, err := t.resolvePathSeeds(ctx, req.EntityNames, req.NodeIDs)
	if err != nil {
		return nil, err
	}
	resp := &ToolSearchPathsResponse{Seeds: seeds, Unresolved: unresolved, Paths: []RetrievedPath{}}
	if len(seeds) == 0 {
		return resp, nil
	}
	ids := make([]string, 0, len(seeds))
	for _, s := range seeds {
		ids = append(ids, s.NodeID)
	}
	found, err := t.db.graph.SearchPaths(ctx, ids, graph.PathSearchOptions{
		MaxDepth:           req.MaxDepth,
		Direction:          req.Direction,
		EdgeTypes:          req.EdgeTypes,
		Relations:          req.RelationPolicies,
		Decay:              req.Decay,
		UseEdgeWeights:     req.UseEdgeWeights,
		MaxPaths:           req.MaxPaths,
		MaxExpansions:      req.MaxExpansions,
		IncludeBookkeeping: req.IncludeBookkeeping,
	})
	if err != nil {
		return nil, fmt.Errorf("search paths: %w", err)
	}
	resp.Truncated = found.Truncated
	resp.Expansions = found.Expansions

	seenChunk := map[string]bool{}
	for _, p := range found.Paths {
		rp := RetrievedPath{
			From:     p.From,
			To:       p.To,
			Hops:     p.Hops,
			Score:    p.Score,
			Sentence: renderPathSentence(&graph.PathResult{Nodes: p.Nodes, Edges: p.Edges}, p.From),
			Edges:    make([]PathEdge, 0, len(p.Edges)),
		}
		names := make(map[string]string, len(p.Nodes))
		for _, n := range p.Nodes {
			name := strings.TrimSpace(n.Content)
			if name == "" {
				name = n.ID
			}
			names[n.ID] = name
		}
		for _, e := range p.Edges {
			pe := pathEdgeFromGraph(e, names)
			for _, id := range pe.ChunkIDs {
				if !seenChunk[id] {
					seenChunk[id] = true
					resp.ChunkIDs = append(resp.ChunkIDs, id)
				}
			}
			rp.Edges = append(rp.Edges, pe)
		}
		resp.Paths = append(resp.Paths, rp)
	}

	if req.WithText && len(resp.ChunkIDs) > 0 {
		chunks, err := t.GetChunks(ctx, ToolGetChunksRequest{ChunkIDs: resp.ChunkIDs, DisableGraph: true})
		if err != nil {
			return nil, fmt.Errorf("search paths: load chunks: %w", err)
		}
		resp.Chunks = chunks.Chunks
	}
	return resp, nil
}

// pathEdgeFromGraph reads an edge's provenance. Two writers, two spellings:
// upsert_relations stores chunk_ids, InsertGraphDocument source_chunk_id.
func pathEdgeFromGraph(e *graph.GraphEdge, names map[string]string) PathEdge {
	pe := PathEdge{
		ID:       e.ID,
		From:     e.FromNodeID,
		FromName: names[e.FromNodeID],
		To:       e.ToNodeID,
		ToName:   names[e.ToNodeID],
		Type:     e.EdgeType,
		Weight:   e.Weight,
	}
	if e.Properties != nil {
		pe.ChunkIDs = stringsFromAny(e.Properties["chunk_ids"])
		if src, ok := e.Properties["source_chunk_id"].(string); ok && src != "" && !slices.Contains(pe.ChunkIDs, src) {
			pe.ChunkIDs = append(pe.ChunkIDs, src)
		}
		pe.DocumentID, _ = e.Properties["document_id"].(string)
		pe.Inferred, _ = e.Properties["inferred"].(bool)
	}
	return pe
}

// resolvePathSeeds turns names and ids into seed nodes, in the order given.
func (t *GraphRAGToolbox) resolvePathSeeds(ctx context.Context, names, nodeIDs []string) ([]PathSeed, []string, error) {
	var seeds []PathSeed
	seen := map[string]bool{}
	add := func(s PathSeed) {
		if !seen[s.NodeID] {
			seen[s.NodeID] = true
			seeds = append(seeds, s)
		}
	}
	var unresolved []string

	wanted := make([]string, 0, len(nodeIDs)+len(names))
	for _, id := range nodeIDs {
		if id = strings.TrimSpace(id); id != "" {
			wanted = append(wanted, id)
		}
	}
	derived := make(map[string]string, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			id := resolveEntityNodeID("", name)
			derived[name] = id
			wanted = append(wanted, id)
		}
	}
	existing := map[string]bool{}
	if len(wanted) > 0 {
		nodes, err := t.db.graph.GetNodesBatch(ctx, wanted)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve path seeds: %w", err)
		}
		for _, n := range nodes {
			existing[n.ID] = true
		}
	}
	for _, id := range nodeIDs {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		if existing[id] {
			add(PathSeed{NodeID: id, Match: "id"})
		} else {
			unresolved = append(unresolved, id)
		}
	}

	var lookup []string
	for _, name := range names {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		if id := derived[name]; existing[id] {
			add(PathSeed{Name: name, NodeID: id, Match: "derived"})
			continue
		}
		lookup = append(lookup, name)
	}
	if len(lookup) > 0 {
		found, err := t.FindNodes(ctx, ToolFindNodesRequest{Names: lookup, Limit: 1})
		if err != nil {
			return nil, nil, fmt.Errorf("resolve path seeds: %w", err)
		}
		byName := map[string]ToolNodeNameMatch{}
		for _, m := range found.Matches {
			byName[m.Name] = m
		}
		for _, name := range lookup {
			m, ok := byName[name]
			if !ok || len(m.Nodes) == 0 || (m.Match != "exact" && m.Match != "fold") {
				unresolved = append(unresolved, name)
				continue
			}
			add(PathSeed{Name: name, NodeID: m.Nodes[0].ID, Match: m.Match})
		}
	}
	return seeds, unresolved, nil
}

// attachGraphRAGPaths fills result.Paths when the query asked for them.
//
// The seeds are the plan's entity names. With none, they are the entities the
// top-ranked chunks mention — the "linked seeds" of the hits the query
// already found — so a query that names nothing still gets the chains among
// the things its best passages are about.
func (db *DB) attachGraphRAGPaths(ctx context.Context, result *GraphRAGQueryResult, opts GraphRAGQueryOptions) error {
	if result == nil || !opts.ReturnPaths {
		return nil
	}
	var names []string
	if result.Plan.EntityNames != nil {
		names = append(names, result.Plan.EntityNames...)
	}
	if len(names) == 0 {
		const linkedSeedChunks = 3
		seen := map[string]bool{}
		for i, c := range result.Chunks {
			if i >= linkedSeedChunks {
				break
			}
			for _, e := range c.Entities {
				if !seen[e] {
					seen[e] = true
					names = append(names, e)
				}
			}
		}
		sort.Strings(names)
	}
	if len(names) == 0 {
		return nil
	}
	resp, err := db.GraphRAGTools().SearchPaths(ctx, ToolSearchPathsRequest{
		EntityNames:      names,
		MaxDepth:         opts.PathMaxDepth,
		RelationPolicies: opts.RelationPolicies,
		MaxPaths:         opts.MaxPaths,
	})
	if err != nil {
		return err
	}
	result.Paths = resp.Paths
	return nil
}
