package cortexdb

// The graph describing itself, rather than answering a query about it.
//
// Everything else that touches the graph on this facade starts from something
// the caller already named — a node, an entity, a question. The three methods
// here start from nothing: which nodes carry the most of the graph's structure,
// which pairs of nodes the structure says should be joined and are not, and how
// big and how connected the whole thing is.
//
// All three were already implemented in pkg/graph and none of them were
// reachable from here, which by this repository's own rule meant they were not
// reachable at all.
//
// Two things are added on the way past, and they are the reason this is not a
// one-line passthrough. First, every answer is made readable: a ranking of
// opaque node ids tells an agent nothing it can act on, so each id is resolved
// to its label and type before it leaves. Second, every answer is made
// deterministic: the engine sorts by score alone with an unstable sort, and
// ties are the normal case — an edgeless graph gives every node the same
// PageRank score — so identical calls could otherwise return differently
// ordered lists.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const (
	// defaultPageRankToolTopN caps what rank_graph_nodes returns when the
	// caller names no cap of its own.
	//
	// The Go API defaults to the whole ranking, because the engine computes
	// every node's score whatever we ask for and a program that wants all of
	// them can hold all of them. A tool answer travels back into a model's
	// context window, where a ranking of every node in a large brain is not an
	// answer but a denial of service against the conversation.
	defaultPageRankToolTopN = 20

	// maxPageRankToolTopN is the ceiling a tool caller cannot raise. A model
	// that asks for ten thousand ranked nodes has misunderstood the question
	// PageRank answers, which is "which few of these matter".
	maxPageRankToolTopN = 200

	// defaultPredictEdgesToolLimit and maxPredictEdgesToolLimit play the same
	// role for predicted edges. The engine's own default when asked for zero is
	// 10; the tool states it rather than inheriting it silently, because the
	// number appears in the tool description a model reads.
	defaultPredictEdgesToolLimit = 10
	maxPredictEdgesToolLimit     = 100

	// maxGraphNodeLabelRunes trims a node label to something that reads as a
	// name.
	//
	// A label is whatever the node stored as its content, and for an entity
	// node that is its name — short. For a chunk node it is a paragraph of
	// prose, and PageRank over an ingested corpus ranks chunk nodes freely,
	// because has_chunk and mentions edges are most of the topology. Without a
	// trim, one well-connected chunk turns a twenty-row ranking into a wall of
	// text.
	maxGraphNodeLabelRunes = 160
)

// RankedGraphNode is one node's structural importance, named.
type RankedGraphNode struct {
	ID string `json:"id"`
	// Label is the node's content, trimmed to a readable length, falling back
	// to the id when the node stored no content. It is what makes a ranking
	// answerable: "entity:8f3a" ranks first is not a finding, "Kubernetes"
	// ranks first is.
	Label    string  `json:"label,omitempty"`
	NodeType string  `json:"node_type,omitempty"`
	Score    float64 `json:"score"`
}

// GraphPageRankOptions bounds a PageRank run and its answer.
type GraphPageRankOptions struct {
	// TopN caps how many of the highest-ranked nodes are returned. Zero means
	// no cap — the whole ranking, which costs nothing extra because the engine
	// scores every node regardless.
	TopN int

	// Iterations bounds the power iteration. Zero uses the engine's default of
	// 100. The loop also stops early once no score moves by more than 1e-6,
	// which on a graph of any realistic shape happens well before 100, so this
	// is a ceiling rather than a cost. There is no way to relax that tolerance:
	// it is a constant inside pkg/graph.
	Iterations int

	// DampingFactor is the probability that the random surfer follows an edge
	// rather than teleporting. Zero, or anything outside (0,1], uses the
	// engine's default of 0.85. Lower values weight local structure more
	// heavily and converge faster.
	DampingFactor float64
}

// GraphPageRankResult is the ranking, and how much of it was withheld.
type GraphPageRankResult struct {
	Nodes []RankedGraphNode `json:"nodes"`
	// TotalNodes is how many nodes were scored, which is every node in the
	// graph. It is the denominator that tells a caller whether the returned
	// head is the interesting part of a large graph or simply all of a small
	// one.
	TotalNodes int `json:"total_nodes"`
	// Truncated reports that TopN cut the ranking short. A trimmed list is
	// otherwise indistinguishable from a complete one, and the difference
	// decides whether "these are the important nodes" or "these are the
	// important nodes we looked at" is the true sentence.
	Truncated bool `json:"truncated"`
}

// GraphPageRank ranks every node by how much of the graph's structure flows
// through it, highest first.
//
// This is the question no keyword can answer: not "what mentions Kubernetes"
// but "what is this knowledge base actually about". A node scores highly by
// being pointed at by nodes that are themselves pointed at, so the ranking
// surfaces the entities the corpus is organised around rather than the ones it
// says most often. It is also the honest way to choose what to show first when
// a graph is too large to show whole — the alternative, showing whichever rows
// the database happened to return, presents an arbitrary sample as a summary.
//
// An empty or edgeless graph is an empty or flat ranking, not an error: with no
// edges every node holds the same share and the tie-break by id decides the
// order, which is the correct answer to "what matters here" when nothing does.
//
// Cost is one full scan of graph_nodes and graph_edges, held in memory as
// integer topology, times the iteration count. The engine offers no way to rank
// a subgraph, so this is always the whole graph.
func (db *DB) GraphPageRank(ctx context.Context, opts GraphPageRankOptions) (*GraphPageRankResult, error) {
	scores, err := db.graph.PageRank(ctx, opts.Iterations, opts.DampingFactor)
	if err != nil {
		return nil, fmt.Errorf("graph pagerank: %w", err)
	}

	// Re-sorted rather than trusted. The engine sorts on score alone with
	// sort.Slice, which is not stable, so any two nodes holding the same score
	// may swap places between identical calls — and equal scores are not an
	// edge case here but the resting state of a graph with few edges. Ordering
	// by id within a score band makes the answer reproducible, which is what
	// lets it be tested, cached, and diffed against yesterday's.
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Score != scores[j].Score {
			return scores[i].Score > scores[j].Score
		}
		return scores[i].NodeID < scores[j].NodeID
	})

	total := len(scores)
	truncated := false
	if opts.TopN > 0 && total > opts.TopN {
		scores = scores[:opts.TopN]
		truncated = true
	}

	// Labels are resolved after the cut, so a ranking of a hundred thousand
	// nodes reads back twenty rows rather than a hundred thousand.
	ids := make([]string, 0, len(scores))
	for _, score := range scores {
		ids = append(ids, score.NodeID)
	}
	summaries, err := db.graphNodeSummariesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("graph pagerank labels: %w", err)
	}

	nodes := make([]RankedGraphNode, 0, len(scores))
	for _, score := range scores {
		summary := summaries[score.NodeID]
		nodes = append(nodes, RankedGraphNode{
			ID:       score.NodeID,
			Label:    graphNodeLabel(score.NodeID, summary.Content),
			NodeType: summary.NodeType,
			Score:    score.Score,
		})
	}

	return &GraphPageRankResult{Nodes: nodes, TotalNodes: total, Truncated: truncated}, nil
}

// PredictedGraphEdge is one pair the graph's shape suggests, with both ends
// named.
type PredictedGraphEdge struct {
	FromID       string  `json:"from_id"`
	FromLabel    string  `json:"from_label,omitempty"`
	FromNodeType string  `json:"from_node_type,omitempty"`
	ToID         string  `json:"to_id"`
	ToLabel      string  `json:"to_label,omitempty"`
	ToNodeType   string  `json:"to_node_type,omitempty"`
	Score        float64 `json:"score"`
	// Method is the engine's own word for what produced the score:
	// "vector_similarity" when the two nodes merely look alike, "combined" when
	// they also share neighbours. It is reported rather than hidden because the
	// two mean different things — see GraphPredictEdges.
	Method string `json:"method"`
}

// GraphPredictEdgesOptions asks which nodes one node should be connected to.
type GraphPredictEdgesOptions struct {
	// NodeID is the node to predict from. Required: the engine's link
	// prediction is single-source, not a sweep over all pairs.
	NodeID string

	// MaxResults caps the predictions returned. Zero uses the engine's default
	// of 10.
	MaxResults int
}

// GraphPredictEdgesResult carries the predictions and whether the cap bit.
type GraphPredictEdgesResult struct {
	Edges     []PredictedGraphEdge `json:"edges"`
	Truncated bool                 `json:"truncated"`
	// TiedAtTop counts how many predictions share the highest score. It is
	// here because of what a real brain does to this method: node vectors on a
	// store with no embedder are short lexical hashes, and two short entity
	// names routinely land in the same single non-zero bucket, which is a
	// cosine of exactly 1. On one real 616-node graph the top of this list was
	// four unrelated names all at 1.0000. The ranking is not wrong — those
	// vectors really are identical — but a caller reading a maximal score has
	// to be able to tell "these two are the same thing" from "this graph's
	// vectors cannot tell anything apart". A large tie at the top means the
	// second.
	TiedAtTop int `json:"tied_at_top"`
}

// GraphPredictEdges returns nodes that NodeID is not connected to but arguably
// should be, best first.
//
// In a knowledge base a prediction is two different findings wearing the same
// shape, and the API deliberately does not pretend to know which one it has
// found:
//
//   - a genuinely missing fact — two things that belong together and were
//     never linked, worth checking against the source, or
//   - one entity stored twice under different names — a duplicate to merge.
//
// Both are worth surfacing and only a reader with the labels in front of them
// can tell them apart, which is why both endpoints come back named.
//
// Two signals produce the score, and they now reinforce rather than cancel:
// how alike the two nodes' vectors are, and how many neighbours they share. The
// engine keeps pairs above 0.5, and shared structure alone can carry a pair
// over that line, so a genuinely missing link between two things that look
// nothing alike is reachable. It was not until this was fixed — the two scores
// were averaged, which put structure-only evidence permanently below the cut
// and made a shared neighbour *lower* an otherwise strong score. On a brain
// whose nodes carry only lexical vectors the similarity half is weak and most
// of what surfaces will still be near-duplicates; that is a property of the
// vectors, not of the method.
//
// The limit that remains is cost: the engine loads every node in the graph,
// vectors and all, to score them against this one, and offers no way to
// restrict the candidate set. On a large brain with wide embeddings this is a
// large allocation. Call it deliberately, not in a loop.
//
// A node with no plausible partners is an empty answer, not an error. A node
// that does not exist is an error, because asking about a node that is not
// there is a mistake rather than a finding.
func (db *DB) GraphPredictEdges(ctx context.Context, opts GraphPredictEdgesOptions) (*GraphPredictEdgesResult, error) {
	nodeID := strings.TrimSpace(opts.NodeID)
	if nodeID == "" {
		return nil, fmt.Errorf("cortexdb: predict edges requires a node id")
	}

	limit := opts.MaxResults
	if limit <= 0 {
		limit = defaultPredictEdgesToolLimit
	}

	// One past the cap, so the answer can say whether there were more. The
	// engine truncates silently to the topK it is given, which leaves a full
	// page indistinguishable from an exhausted one.
	predictions, err := db.graph.PredictEdges(ctx, nodeID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("graph predict edges: %w", err)
	}

	// Same tie-break as the ranking, for the same reason: the engine sorts on
	// score alone and unstably, and predictions land on identical scores often
	// — two exact duplicates of the same entity both score 1.
	sort.Slice(predictions, func(i, j int) bool {
		if predictions[i].Score != predictions[j].Score {
			return predictions[i].Score > predictions[j].Score
		}
		return predictions[i].ToNodeID < predictions[j].ToNodeID
	})

	truncated := len(predictions) > limit
	if truncated {
		predictions = predictions[:limit]
	}

	ids := make([]string, 0, len(predictions)+1)
	ids = append(ids, nodeID)
	for _, prediction := range predictions {
		ids = append(ids, prediction.ToNodeID)
	}
	summaries, err := db.graphNodeSummariesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("graph predict edges labels: %w", err)
	}
	from := summaries[nodeID]

	edges := make([]PredictedGraphEdge, 0, len(predictions))
	for _, prediction := range predictions {
		to := summaries[prediction.ToNodeID]
		edges = append(edges, PredictedGraphEdge{
			FromID:       prediction.FromNodeID,
			FromLabel:    graphNodeLabel(prediction.FromNodeID, from.Content),
			FromNodeType: from.NodeType,
			ToID:         prediction.ToNodeID,
			ToLabel:      graphNodeLabel(prediction.ToNodeID, to.Content),
			ToNodeType:   to.NodeType,
			Score:        prediction.Score,
			Method:       prediction.Method,
		})
	}

	// Counted over what is returned, not over every candidate the engine
	// scored: the list is already cut to the caller's limit by here, and going
	// back for the rest would be a second full pass over the graph to sharpen
	// a warning. When the whole returned page is tied and Truncated is set,
	// the real tie is at least this large — which is all a caller needs to
	// know that the vectors, not the graph, are doing the talking.
	tied := 0
	if len(edges) > 0 {
		top := edges[0].Score
		for _, e := range edges {
			if e.Score == top {
				tied++
			}
		}
	}

	return &GraphPredictEdgesResult{Edges: edges, Truncated: truncated, TiedAtTop: tied}, nil
}

// GraphStatistics reports the size and shape of the whole graph.
//
// The return type is pkg/graph's own because it is already the right shape and
// already carries the JSON names the tool surface uses; a parallel struct here
// would only give a caller two spellings of the same five numbers.
//
// The two numbers worth reading carefully are the last two. Density compares
// the edge count against every ordered pair, so it falls away quickly as a
// graph grows and is only meaningful against another measurement of the same
// graph. ConnectedComponents treats edges as undirected: it counts islands, and
// a count far above one on a brain that should be about a single subject is
// usually the sign of an ingest that wrote entities without linking them.
//
// An empty graph reports zeros, not an error.
func (db *DB) GraphStatistics(ctx context.Context) (*graph.GraphStatistics, error) {
	stats, err := db.graph.GetGraphStatistics(ctx)
	if err != nil {
		return nil, fmt.Errorf("graph statistics: %w", err)
	}
	return stats, nil
}

// graphNodeLabel picks what to call a node in an answer.
//
// Content first because for an entity node that is its name, and the id second
// because a node with no content still has to be identifiable — an empty label
// beside a score would make the row unreadable rather than merely terse.
func graphNodeLabel(nodeID, content string) string {
	label := strings.TrimSpace(content)
	if label == "" {
		return nodeID
	}
	// Counted in runes, not bytes: the corpora this runs on are frequently CJK,
	// where a byte cut lands mid-character and produces a replacement glyph.
	runes := []rune(label)
	if len(runes) > maxGraphNodeLabelRunes {
		label = strings.TrimSpace(string(runes[:maxGraphNodeLabelRunes])) + "…"
	}
	return label
}

// ToolRankGraphNodesRequest asks which nodes carry the most structure.
type ToolRankGraphNodesRequest struct {
	TopN          int     `json:"top_n,omitempty"`
	Iterations    int     `json:"iterations,omitempty"`
	DampingFactor float64 `json:"damping_factor,omitempty"`
}

// ToolRankGraphNodesResponse returns the ranked head of the graph.
type ToolRankGraphNodesResponse struct {
	Nodes      []RankedGraphNode `json:"nodes"`
	Count      int               `json:"count"`
	TotalNodes int               `json:"total_nodes"`
	Truncated  bool              `json:"truncated"`
}

// RankGraphNodes ranks the graph's nodes by PageRank for a tool caller.
func (t *GraphRAGToolbox) RankGraphNodes(ctx context.Context, req ToolRankGraphNodesRequest) (*ToolRankGraphNodesResponse, error) {
	topN := req.TopN
	if topN <= 0 {
		topN = defaultPageRankToolTopN
	}
	if topN > maxPageRankToolTopN {
		topN = maxPageRankToolTopN
	}

	result, err := t.db.GraphPageRank(ctx, GraphPageRankOptions{
		TopN:          topN,
		Iterations:    req.Iterations,
		DampingFactor: req.DampingFactor,
	})
	if err != nil {
		return nil, err
	}
	return &ToolRankGraphNodesResponse{
		Nodes:      result.Nodes,
		Count:      len(result.Nodes),
		TotalNodes: result.TotalNodes,
		Truncated:  result.Truncated,
	}, nil
}

// ToolPredictGraphEdgesRequest asks what one node should be connected to.
type ToolPredictGraphEdgesRequest struct {
	NodeID     string `json:"node_id"`
	MaxResults int    `json:"max_results,omitempty"`
}

// ToolPredictGraphEdgesResponse returns the suggested pairs.
type ToolPredictGraphEdgesResponse struct {
	Edges     []PredictedGraphEdge `json:"edges"`
	Count     int                  `json:"count"`
	Truncated bool                 `json:"truncated"`
	// TiedAtTop is how many of these share the highest score. Read it before
	// reading the scores: several unrelated nodes tied at exactly 1.0 means
	// this graph's vectors cannot tell them apart, not that they are the same
	// thing. See GraphPredictEdgesResult.
	TiedAtTop int `json:"tied_at_top"`
}

// PredictGraphEdges suggests missing connections for a tool caller.
func (t *GraphRAGToolbox) PredictGraphEdges(ctx context.Context, req ToolPredictGraphEdgesRequest) (*ToolPredictGraphEdgesResponse, error) {
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultPredictEdgesToolLimit
	}
	if limit > maxPredictEdgesToolLimit {
		limit = maxPredictEdgesToolLimit
	}

	result, err := t.db.GraphPredictEdges(ctx, GraphPredictEdgesOptions{
		NodeID:     req.NodeID,
		MaxResults: limit,
	})
	if err != nil {
		return nil, err
	}
	return &ToolPredictGraphEdgesResponse{
		Edges:     result.Edges,
		Count:     len(result.Edges),
		Truncated: result.Truncated,
		TiedAtTop: result.TiedAtTop,
	}, nil
}

// ToolGraphStatisticsRequest takes no arguments: the question is about the
// whole graph and there is nothing to narrow. It exists so the tool dispatcher
// has a shape to decode into, like every other tool here.
type ToolGraphStatisticsRequest struct{}

// ToolGraphStatisticsResponse is the graph's own report on itself.
//
// Spelled out here rather than reusing pkg/graph's struct, unlike the Go API
// above: the tool surface is a published contract that a model's prompt is
// written against, and it should not change shape because an engine struct
// gained a field.
type ToolGraphStatisticsResponse struct {
	NodeCount           int     `json:"node_count"`
	EdgeCount           int     `json:"edge_count"`
	AverageDegree       float64 `json:"average_degree"`
	Density             float64 `json:"density"`
	ConnectedComponents int     `json:"connected_components"`
}

// GraphStatistics reports the graph's size and shape for a tool caller.
func (t *GraphRAGToolbox) GraphStatistics(ctx context.Context, _ ToolGraphStatisticsRequest) (*ToolGraphStatisticsResponse, error) {
	stats, err := t.db.GraphStatistics(ctx)
	if err != nil {
		return nil, err
	}
	return &ToolGraphStatisticsResponse{
		NodeCount:           stats.NodeCount,
		EdgeCount:           stats.EdgeCount,
		AverageDegree:       stats.AverageDegree,
		Density:             stats.Density,
		ConnectedComponents: stats.ConnectedComponents,
	}, nil
}
