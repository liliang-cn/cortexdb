package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Deciding which entity a name refers to, using the graph that the text built.
//
// A mention like "Zika" resolves to several nodes, and the two usual ways of
// choosing between them both fail on exactly the cases worth asking about.
// String similarity cannot tell "Zika virus" from "Zika fever" because the
// strings are equally close to the mention. An embedding of the mention on its
// own has nothing to be close to either, since the ambiguity is not in the word.
//
// What separates them is the company the mention keeps. If "microcephaly"
// appears in the same sentence and the graph holds
// "Congenital Zika virus infection causes microcephaly", then that candidate is
// the one, and the reason is a path that can be printed and checked. This is the
// method from Knowledge Graphs and LLMs in Action, ch. 8: candidates, context
// from co-occurrence, connecting paths, paths rendered as sentences, and a
// choice.
//
// Everything here is the first four steps. The fifth — the choice — is a
// judgement, and CortexDB does not make judgements or call models. What it
// returns instead is the evidence in the one shape whose only remaining step is
// the choosing: ranked candidates, each carrying the paths that support it and a
// deterministic score computed from them. A caller with no model takes the top
// of the ranking; a caller with a model hands it the sentences and lets it
// decide; either way the answer can be explained afterwards, because the paths
// are the reason and they came back with it.

// ErrNoMentions is returned when a disambiguation request names nothing to
// disambiguate.
//
// An empty list is not a question with an empty answer, it is a question that
// was never asked — most often a field that did not get filled in. Returning an
// empty result for it would look like "none of your mentions could be resolved",
// which is a finding, and this is not one.
var ErrNoMentions = errors.New("cortexdb: disambiguation needs at least one mention")

// Why a mention came back without an answer. These are the honest outcomes: a
// wrong confident pick is worse than an admitted failure, so nothing here
// silently falls back to the first candidate.
const (
	// DisambiguationNoCandidates means the graph has no node by that name at
	// all. Nothing to choose between, and nothing to blame the method for.
	DisambiguationNoCandidates = "no_candidates"

	// DisambiguationNoContext means there was no other mention to disambiguate
	// against. Co-occurrence is the entire input signal; with one mention the
	// method has nothing to work with, and saying "no supporting paths" here
	// would blame the graph for what the caller did not supply.
	DisambiguationNoContext = "no_context"

	// DisambiguationNoSupport means candidates exist but none of them connects
	// to any candidate of any other mention. Check SearchIncomplete and
	// ConnectedBeyondMaxLength before reading this as "not connected": with
	// either of them set, it means "not connected as far as we looked".
	DisambiguationNoSupport = "no_supporting_paths"

	// DisambiguationTie means two or more candidates are supported exactly
	// equally. The graph genuinely does not separate them, so the ranking's
	// first element is an arbitrary choice and is reported as one.
	DisambiguationTie = "tie"
)

// The bounds that keep this affordable, and why these numbers.
//
// Cost here is multiplicative: mentions choose pairs, pairs multiply by
// candidates on both sides, and every resulting pair is a traversal. Five
// mentions with five candidates each is ten mention pairs and two hundred and
// fifty candidate pairs, from an input that looks like one short sentence.
const (
	// defaultDisambiguationCandidates matches find_nodes' own per-name default.
	// Beyond about five, the extra candidates are containment matches that lost
	// on every other measure, and each one multiplies the pair count.
	defaultDisambiguationCandidates = 5

	// defaultDisambiguationPathLength is where a connecting path stops being
	// evidence about this mention. Three hops still carries the shape of a real
	// relationship; past that, almost any two nodes in a well-connected graph
	// are joined by something, usually through a hub like a type or a document,
	// and the path stops discriminating between candidates.
	defaultDisambiguationPathLength = 3

	// defaultDisambiguationSearches bounds the traversals. On a graph of tens of
	// thousands of nodes a single traversal that finds nothing walks the whole
	// reachable component, so this is the number that decides whether the call
	// takes milliseconds or minutes. Two hundred covers the realistic input —
	// a handful of mentions from one sentence — with room to spare, and the
	// response says when it bit.
	defaultDisambiguationSearches = 200
)

// DisambiguationOptions narrows the search and bounds its cost.
//
// Every bound has a default; zero means the default rather than zero, because a
// zero bound would make the method answer "nothing is connected" without looking
// at anything, which is the one answer it must never give falsely.
type DisambiguationOptions struct {
	// NodeTypes restricts candidates to these node types, e.g. []string{"Disease"}.
	// Empty means any type. This is the cheapest bound available: it cuts
	// candidates before they multiply into pairs, and it is usually known — a
	// caller disambiguating names out of a clinical note knows it wants diseases.
	NodeTypes []string

	// MaxCandidatesPerMention caps how many nodes each mention may resolve to.
	// Zero means defaultDisambiguationCandidates.
	MaxCandidatesPerMention int

	// MaxPathLength is the longest path, in edges, that still counts as support.
	// Zero means defaultDisambiguationPathLength.
	//
	// This bounds what is reported, not what is walked: the engine's shortest
	// path takes no depth limit, so a pair that turns out to be six hops apart
	// still costs a full traversal. It is worth setting anyway — the response
	// distinguishes "connected further away than you allowed" from "not
	// connected", which a depth-limited search could not have told you.
	MaxPathLength int

	// MaxPathSearches caps how many candidate pairs are examined. Zero means
	// defaultDisambiguationSearches. This is the bound that actually controls
	// runtime. When it bites, the pairs dropped are the ones whose candidates
	// find_nodes already ranked worst, and every mention that lost a pair to it
	// says so.
	MaxPathSearches int
}

// DisambiguationBounds echoes the bounds a run actually used.
//
// Returned because most callers pass zeros and get defaults, and a caller
// reading "no supporting paths" needs to know how far the search went before it
// decides whether to widen and ask again.
type DisambiguationBounds struct {
	MaxCandidatesPerMention int `json:"max_candidates_per_mention"`
	MaxPathLength           int `json:"max_path_length"`
	MaxPathSearches         int `json:"max_path_searches"`
}

// DisambiguationPath is one connection, and the sentence that says what it is.
type DisambiguationPath struct {
	// ToMention is the co-occurring mention this path reaches, and ToNodeID /
	// ToName the candidate of it that was reached. Both are here because the
	// mention says why the connection counts as evidence and the node says what
	// was actually connected — a path to the wrong reading of a neighbouring
	// mention is still a path, and a caller should be able to see that.
	ToMention string `json:"to_mention"`
	ToNodeID  string `json:"to_node_id"`
	ToName    string `json:"to_name,omitempty"`

	// Length is the path length in edges. Zero means both mentions resolved to
	// the same node.
	Length int `json:"length"`

	// Sentence is the path written out, e.g. "Congenital Zika virus infection
	// causes microcephaly". This is the part a model reads and a person checks.
	Sentence string `json:"sentence"`

	// EdgeIDs are the edges the path ran through, in order, so any claim in the
	// sentence can be taken to fact_provenance and traced back to the text it
	// came from. Evidence that cannot be audited is just an assertion with extra
	// steps.
	EdgeIDs []string `json:"edge_ids,omitempty"`
}

// DisambiguationCandidate is one reading of a mention with the case for it.
type DisambiguationCandidate struct {
	NodeID   string `json:"node_id"`
	Name     string `json:"name"`
	NodeType string `json:"node_type,omitempty"`

	// Score is the support this candidate got. See scoreCandidate for the rule.
	// It is comparable between candidates of the same mention and not much else:
	// it is a sum over co-occurring mentions, so a mention seen in a long
	// sentence can outscore the same mention seen in a short one.
	Score float64 `json:"score"`

	// SupportingMentions is how many distinct co-occurring mentions this
	// candidate connects to. It is the coarse form of Score and often the more
	// useful one — "connected to three of the four other things in the sentence"
	// is a statement a caller can act on without knowing the scoring rule.
	SupportingMentions int `json:"supporting_mentions"`

	// Evidence holds every connecting path found for this candidate, shortest
	// first. A losing candidate keeps its empty Evidence rather than being
	// dropped: the fact that a plausible reading had nothing behind it is part
	// of the answer, and a caller comparing "one supported candidate" against
	// "one supported and three unsupported" is looking at different situations.
	Evidence []DisambiguationPath `json:"evidence"`
}

// MentionDisambiguation is one mention's candidates, ranked, and what stopped
// the search.
type MentionDisambiguation struct {
	Mention string `json:"mention"`

	// Match is how find_nodes matched the best-matching candidate: "exact",
	// "fold" (case and punctuation ignored) or "contains". Carried through
	// because a mention whose only candidates are containment matches is a
	// weaker starting point no matter how well its paths score.
	Match string `json:"match,omitempty"`

	// Candidates, best supported first. Ties in score fall back to find_nodes'
	// own ranking, which is a string-matching judgement — hence Unresolved
	// below, so nobody mistakes that fallback for evidence.
	Candidates []DisambiguationCandidate `json:"candidates"`

	// Unresolved says the top of Candidates is not an answer. See Reason.
	Unresolved bool   `json:"unresolved"`
	Reason     string `json:"reason,omitempty"`

	// CandidatesTruncated says this mention resolved to more nodes than
	// MaxCandidatesPerMention allowed through, so the right reading may not be
	// among the candidates at all.
	CandidatesTruncated bool `json:"candidates_truncated,omitempty"`

	// SearchIncomplete says at least one pair involving this mention was never
	// examined because MaxPathSearches ran out. Without this, a bounded search
	// and an unconnected graph would return the same thing, and they are not the
	// same thing.
	SearchIncomplete bool `json:"search_incomplete,omitempty"`

	// ConnectedBeyondMaxLength says a connection was found and then discarded
	// for being longer than MaxPathLength. The distinction from "no connection"
	// is the whole reason this field exists: one means widen the search, the
	// other means the graph does not relate these things.
	ConnectedBeyondMaxLength bool `json:"connected_beyond_max_length,omitempty"`
}

// DisambiguationResult is the whole answer: one entry per distinct mention, in
// the order they were given.
type DisambiguationResult struct {
	Mentions []MentionDisambiguation `json:"mentions"`

	// SearchesPerformed is the number of candidate pairs actually examined, and
	// SearchBudgetExhausted whether MaxPathSearches stopped the run. The
	// per-mention SearchIncomplete flags say who was affected; these two say how
	// close the whole call came to the ceiling, which is what a caller tunes.
	SearchesPerformed     int  `json:"searches_performed"`
	SearchBudgetExhausted bool `json:"search_budget_exhausted,omitempty"`

	// Bounds are the bounds this run used, defaults filled in.
	Bounds DisambiguationBounds `json:"bounds"`
}

// DisambiguateMentions decides which graph entity each mention refers to, using
// the other mentions as context.
//
// Duplicate mentions are collapsed: the same string twice is one mention seen
// twice, not two mentions that corroborate each other, and leaving them in would
// let a repeated word vote for itself.
//
// From running this against a real 616-node brain: NodeTypes is not optional in
// practice on any graph that keeps documents and chunks as nodes beside its
// entities. Asked to resolve "MCP" and "OpenClaw" together, find_nodes matched
// both to one document titled "…from WeChat/OpenClaw (MCP + iTerm2 tty typing)"
// by substring — and a single node that two mentions both resolve to is the
// strongest evidence this method has, so a coincidence of two words in one title
// scored 1.0, tied with the genuine protocol node, and the answer was "tie".
// Naming the entity-like types cleared it: MCP resolved to the protocol on the
// strength of "claude -p gates MCP", and OpenClaw came back as
// no_supporting_paths, which is the truth about that graph. The filter is also
// the cheapest bound there is, because it cuts candidates before they multiply
// into pairs.
func (db *DB) DisambiguateMentions(ctx context.Context, mentions []string, opts DisambiguationOptions) (*DisambiguationResult, error) {
	names := dedupeMentions(mentions)
	if len(names) == 0 {
		return nil, ErrNoMentions
	}

	bounds := DisambiguationBounds{
		MaxCandidatesPerMention: opts.MaxCandidatesPerMention,
		MaxPathLength:           opts.MaxPathLength,
		MaxPathSearches:         opts.MaxPathSearches,
	}
	if bounds.MaxCandidatesPerMention <= 0 {
		bounds.MaxCandidatesPerMention = defaultDisambiguationCandidates
	}
	if bounds.MaxPathLength <= 0 {
		bounds.MaxPathLength = defaultDisambiguationPathLength
	}
	if bounds.MaxPathSearches <= 0 {
		bounds.MaxPathSearches = defaultDisambiguationSearches
	}

	// Step 1, candidates, is find_nodes and nothing else. It already does the
	// three matching passes and already reports which one hit, and a second
	// name-resolution path here would be a second thing to keep in agreement
	// with the graph's spelling conventions. Ask for one candidate past the cap
	// so the answer can say the cap bit.
	found, err := db.GraphRAGTools().FindNodes(ctx, ToolFindNodesRequest{
		Names:     names,
		NodeTypes: opts.NodeTypes,
		Limit:     bounds.MaxCandidatesPerMention + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve mention candidates: %w", err)
	}
	byName := make(map[string]ToolNodeNameMatch, len(found.Matches))
	for _, match := range found.Matches {
		byName[match.Name] = match
	}

	result := &DisambiguationResult{
		Mentions: make([]MentionDisambiguation, 0, len(names)),
		Bounds:   bounds,
	}
	// candidates[i] holds the nodes mention i may refer to, in find_nodes' order,
	// which is also the tie-break order and the order the pair budget spends in.
	candidates := make([][]*graph.GraphNode, len(names))
	for i, name := range names {
		match := byName[name]
		nodes := match.Nodes
		truncated := len(nodes) > bounds.MaxCandidatesPerMention
		if truncated {
			nodes = nodes[:bounds.MaxCandidatesPerMention]
		}
		candidates[i] = nodes

		entry := MentionDisambiguation{
			Mention:             name,
			Match:               match.Match,
			Candidates:          make([]DisambiguationCandidate, 0, len(nodes)),
			CandidatesTruncated: truncated,
		}
		for _, node := range nodes {
			entry.Candidates = append(entry.Candidates, DisambiguationCandidate{
				NodeID:   node.ID,
				Name:     node.Content,
				NodeType: node.NodeType,
				Evidence: []DisambiguationPath{},
			})
		}
		result.Mentions = append(result.Mentions, entry)
	}

	// Step 2, context from co-occurrence. With one mention there is no context,
	// and the method has nothing to say — which is a different statement from
	// "the graph does not connect this to anything", so it gets its own reason.
	if len(names) < 2 {
		result.Mentions[0].Unresolved = true
		result.Mentions[0].Reason = DisambiguationNoContext
		if len(result.Mentions[0].Candidates) == 0 {
			result.Mentions[0].Reason = DisambiguationNoCandidates
		}
		return result, nil
	}

	// Steps 3 and 4: connect the candidates and write the connections out.
	work := planDisambiguationPairs(candidates)
	if len(work) > bounds.MaxPathSearches {
		// The pairs were ordered worst-last, so truncation drops the candidates
		// find_nodes already thought least likely rather than an arbitrary tail.
		for _, pair := range work[bounds.MaxPathSearches:] {
			result.Mentions[pair.leftMention].SearchIncomplete = true
			result.Mentions[pair.rightMention].SearchIncomplete = true
		}
		work = work[:bounds.MaxPathSearches]
		result.SearchBudgetExhausted = true
	}

	// The same unordered node pair turns up once for each of the two mentions
	// that produced it, and repeats again whenever two mentions share a
	// candidate. Each entry is a traversal, so the cache is worth more than it
	// costs; a nil value is a remembered absence, which is the expensive case.
	seen := make(map[[2]string]*graph.PathResult, len(work))
	for _, pair := range work {
		key := [2]string{pair.leftNode, pair.rightNode}
		if key[0] > key[1] {
			key[0], key[1] = key[1], key[0]
		}
		path, cached := seen[key]
		if !cached {
			path, err = db.shortestPathEitherWay(ctx, pair.leftNode, pair.rightNode)
			if err != nil {
				return nil, err
			}
			seen[key] = path
			result.SearchesPerformed++
		}
		if path == nil {
			continue
		}
		if path.Distance > bounds.MaxPathLength {
			result.Mentions[pair.leftMention].ConnectedBeyondMaxLength = true
			result.Mentions[pair.rightMention].ConnectedBeyondMaxLength = true
			continue
		}

		// One connection, recorded from both ends: it is evidence for the left
		// candidate that it reaches the right mention, and evidence for the
		// right candidate that it reaches the left one.
		sentence := renderPathSentence(path, pair.leftNode)
		edgeIDs := make([]string, 0, len(path.Edges))
		for _, edge := range path.Edges {
			edgeIDs = append(edgeIDs, edge.ID)
		}
		left := &result.Mentions[pair.leftMention].Candidates[pair.leftIndex]
		right := &result.Mentions[pair.rightMention].Candidates[pair.rightIndex]
		left.Evidence = append(left.Evidence, DisambiguationPath{
			ToMention: names[pair.rightMention],
			ToNodeID:  pair.rightNode,
			ToName:    right.Name,
			Length:    path.Distance,
			Sentence:  sentence,
			EdgeIDs:   edgeIDs,
		})
		right.Evidence = append(right.Evidence, DisambiguationPath{
			ToMention: names[pair.leftMention],
			ToNodeID:  pair.leftNode,
			ToName:    left.Name,
			Length:    path.Distance,
			Sentence:  sentence,
			EdgeIDs:   edgeIDs,
		})
	}

	// Step 5 is the caller's. What is left here is arranging the evidence so the
	// choice is the only thing missing.
	for i := range result.Mentions {
		rankDisambiguationCandidates(&result.Mentions[i])
	}
	return result, nil
}

// dedupeMentions trims, drops the empties, and keeps the first spelling of each
// repeat, in the order given.
func dedupeMentions(mentions []string) []string {
	out := make([]string, 0, len(mentions))
	seen := make(map[string]struct{}, len(mentions))
	for _, mention := range mentions {
		trimmed := strings.TrimSpace(mention)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

// disambiguationPair is one candidate-to-candidate search waiting to be run.
type disambiguationPair struct {
	leftMention, leftIndex   int
	rightMention, rightIndex int
	leftNode, rightNode      string
	// rankSum is how far down find_nodes' ranking the two candidates sat. It
	// decides what the budget spends on first.
	rankSum int
}

// planDisambiguationPairs lists every candidate pair drawn from two different
// mentions, weakest last.
//
// The order is the whole point. When MaxPathSearches cuts the list short, the
// pairs that survive are the ones between candidates that already matched their
// mentions best, so a budget that runs out costs the answer its least likely
// evidence rather than a random slice of it. Ties are broken by position so two
// identical inputs always spend the budget on the same pairs.
func planDisambiguationPairs(candidates [][]*graph.GraphNode) []disambiguationPair {
	pairs := make([]disambiguationPair, 0, 64)
	for i := range candidates {
		for j := i + 1; j < len(candidates); j++ {
			for x, left := range candidates[i] {
				for y, right := range candidates[j] {
					pairs = append(pairs, disambiguationPair{
						leftMention: i, leftIndex: x,
						rightMention: j, rightIndex: y,
						leftNode: left.ID, rightNode: right.ID,
						rankSum: x + y,
					})
				}
			}
		}
	}
	sort.SliceStable(pairs, func(a, b int) bool {
		if pairs[a].rankSum != pairs[b].rankSum {
			return pairs[a].rankSum < pairs[b].rankSum
		}
		if pairs[a].leftMention != pairs[b].leftMention {
			return pairs[a].leftMention < pairs[b].leftMention
		}
		return pairs[a].rightMention < pairs[b].rightMention
	})
	return pairs
}

// shortestPathEitherWay returns the shorter of the two directed paths between
// two nodes, or nil when neither exists.
//
// The engine's shortest path follows edges the way they are stored, but the
// evidence a path carries is symmetric: "Congenital Zika virus infection causes
// microcephaly" tells you as much when you arrive from microcephaly as when you
// arrive from Zika. Searching only one way would make the answer depend on which
// mention the writer happened to put first in the sentence.
func (db *DB) shortestPathEitherWay(ctx context.Context, fromID, toID string) (*graph.PathResult, error) {
	forward, err := db.shortestPathOrNil(ctx, fromID, toID)
	if err != nil {
		return nil, err
	}
	// Nothing beats a direct edge, and a self-pair is distance zero, so the
	// second traversal — the expensive half of this call — is skipped whenever
	// the first one already found the best possible answer.
	if forward != nil && forward.Distance <= 1 {
		return forward, nil
	}
	backward, err := db.shortestPathOrNil(ctx, toID, fromID)
	if err != nil {
		return nil, err
	}
	switch {
	case forward == nil:
		return backward, nil
	case backward == nil:
		return forward, nil
	case backward.Distance < forward.Distance:
		return backward, nil
	default:
		return forward, nil
	}
}

// shortestPathOrNil calls the graph's shortest path and turns "there isn't one"
// back into an absence.
//
// The engine reports an unconnected pair as an error, which is reasonable for a
// caller that asked about one pair it expected to be joined. Here it is the
// commonest and most informative outcome — half of what disambiguation learns is
// which candidates are *not* connected to anything — so letting it propagate
// would fail the call on its own findings.
//
// Matching on the message is unpleasant and deliberate: pkg/graph offers no
// sentinel to compare against, and this file does not get to add one. If one
// ever appears, this should switch to errors.Is.
func (db *DB) shortestPathOrNil(ctx context.Context, fromID, toID string) (*graph.PathResult, error) {
	path, err := db.graph.ShortestPath(ctx, fromID, toID)
	if err != nil {
		if strings.Contains(err.Error(), "no path found") {
			return nil, nil
		}
		return nil, fmt.Errorf("shortest path %s -> %s: %w", fromID, toID, err)
	}
	return path, nil
}

// rankDisambiguationCandidates scores, sorts, and decides whether the top of the
// list is an answer.
func rankDisambiguationCandidates(mention *MentionDisambiguation) {
	for i := range mention.Candidates {
		candidate := &mention.Candidates[i]
		sort.SliceStable(candidate.Evidence, func(a, b int) bool {
			return candidate.Evidence[a].Length < candidate.Evidence[b].Length
		})
		candidate.Score, candidate.SupportingMentions = scoreCandidate(candidate.Evidence)
	}

	// find_nodes' order is the tie-break, so it has to survive the sort: a
	// stable sort on score alone would keep it, but only because it is stable,
	// which is too subtle a thing to depend on. Rank is compared explicitly.
	order := make(map[string]int, len(mention.Candidates))
	for i, candidate := range mention.Candidates {
		order[candidate.NodeID] = i
	}
	sort.SliceStable(mention.Candidates, func(a, b int) bool {
		if mention.Candidates[a].Score != mention.Candidates[b].Score {
			return mention.Candidates[a].Score > mention.Candidates[b].Score
		}
		return order[mention.Candidates[a].NodeID] < order[mention.Candidates[b].NodeID]
	})

	switch {
	case len(mention.Candidates) == 0:
		mention.Unresolved = true
		mention.Reason = DisambiguationNoCandidates
	case mention.Candidates[0].Score == 0:
		// Including the case of a single candidate with nothing behind it. It is
		// unambiguous — a caller can see that for itself from the one-element
		// list — but it is not corroborated, and this method's answer is the
		// corroboration.
		mention.Unresolved = true
		mention.Reason = DisambiguationNoSupport
	case len(mention.Candidates) > 1 && mention.Candidates[0].Score == mention.Candidates[1].Score:
		mention.Unresolved = true
		mention.Reason = DisambiguationTie
	}
}

// scoreCandidate turns a candidate's paths into a number, and counts how many
// distinct co-occurring mentions stand behind it.
//
// The rule: one vote per co-occurring mention, worth 1/length of the shortest
// path that reaches it. Two decisions there, both of which change the answer.
//
// Per mention rather than per path, because paths to the same neighbour are not
// independent evidence. A neighbouring mention that resolved to five candidates
// can be connected five times over, and all five connections rest on the same
// co-occurrence — counting each one would let a single ambiguous neighbour
// outvote three unambiguous ones, which is backwards: the ambiguous neighbour is
// the less trustworthy witness.
//
// Weighted by 1/length, because every extra hop is another chance the connection
// is incidental. A direct edge is a stated fact about these two things; a
// three-hop path is usually two facts joined by something generic. 1/length is
// steep where the difference matters (1, 0.5, 0.33) and flattens out past that,
// where all long paths are equally weak. Length zero — both mentions resolving
// to one node — is treated as one, since nothing should score above a direct
// statement.
func scoreCandidate(evidence []DisambiguationPath) (score float64, supporting int) {
	best := make(map[string]int, len(evidence))
	for _, path := range evidence {
		length := path.Length
		if length < 1 {
			length = 1
		}
		if current, ok := best[path.ToMention]; !ok || length < current {
			best[path.ToMention] = length
		}
	}
	for _, length := range best {
		score += 1 / float64(length)
	}
	return score, len(best)
}

// renderPathSentence writes a path out as English.
//
// This is step 4, and it is not decoration. A path returned as node and edge IDs
// is evidence only to something that can query the graph again; written out, it
// is evidence to a model reading it in a prompt and to a person reading it in a
// log, and it is the form in which a wrong answer becomes obviously wrong.
//
// Each hop becomes its own clause with the shared node named twice — "A causes
// B, and B is a birth defect" — rather than chained with a pronoun. Relative
// clauses need the edge labels to be verbs that read well in sequence, and edge
// labels come out of extraction, so they are frequently neither.
func renderPathSentence(path *graph.PathResult, fromID string) string {
	if path == nil {
		return ""
	}
	content := make(map[string]string, len(path.Nodes))
	for _, node := range path.Nodes {
		name := strings.TrimSpace(node.Content)
		if name == "" {
			// A node with no content is a graph-hygiene problem, not a reason to
			// return a sentence with a hole in it.
			name = node.ID
		}
		content[node.ID] = name
	}
	nameOf := func(id string) string {
		if name, ok := content[id]; ok {
			return name
		}
		return id
	}

	if len(path.Edges) == 0 {
		// Distance zero: the two mentions are two names for one node.
		return fmt.Sprintf("Both mentions resolve to the same entity: %s.", nameOf(fromID))
	}

	clauses := make([]string, 0, len(path.Edges))
	for _, edge := range path.Edges {
		clauses = append(clauses, fmt.Sprintf("%s %s %s",
			nameOf(edge.FromNodeID), humanizeEdgeType(edge.EdgeType), nameOf(edge.ToNodeID)))
	}
	return strings.Join(clauses, ", and ") + "."
}

// humanizeEdgeType turns a stored edge label into something that reads as a
// verb: CAUSES, causes, is_a and isTreatedBy all become words with spaces
// between them.
//
// CJK labels pass through untouched, since neither the case rules nor the word
// splitting apply to them, which is the behaviour a mixed-language graph needs.
// An unlabelled edge gets "is related to" — vague, but the connection is real
// and dropping the clause would lose it.
func humanizeEdgeType(edgeType string) string {
	trimmed := strings.TrimSpace(edgeType)
	if trimmed == "" {
		return "is related to"
	}
	var b strings.Builder
	b.Grow(len(trimmed) + 4)
	runes := []rune(trimmed)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-':
			b.WriteRune(' ')
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])):
			// A capital after a lowercase letter is a word boundary in
			// camelCase; a capital after another capital is not, or ACRONYMS
			// would come apart one letter at a time.
			b.WriteRune(' ')
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ToolDisambiguateMentionsRequest asks which entity each mention refers to.
type ToolDisambiguateMentionsRequest struct {
	Mentions []string `json:"mentions"`
	// NodeTypes is an optional candidate filter, e.g. ["Disease"].
	NodeTypes []string `json:"node_types,omitempty"`
	// The bounds. Zero means the default in each case; see
	// DisambiguationOptions for what each one costs.
	MaxCandidatesPerMention int `json:"max_candidates_per_mention,omitempty"`
	MaxPathLength           int `json:"max_path_length,omitempty"`
	MaxPathSearches         int `json:"max_path_searches,omitempty"`
}

// DisambiguateMentions resolves ambiguous names against the graph for a tool
// caller, and hands back the evidence rather than a verdict.
//
// The response is DisambiguationResult unchanged. There is nothing to trim for
// the tool surface: it carries no vectors and no whole nodes, only names, scores
// and the sentences, and the sentences are the entire point of calling it — a
// model that gets the ranking without them can only take the top on faith, which
// is the failure mode this whole method exists to avoid.
func (t *GraphRAGToolbox) DisambiguateMentions(ctx context.Context, req ToolDisambiguateMentionsRequest) (*DisambiguationResult, error) {
	return t.db.DisambiguateMentions(ctx, req.Mentions, DisambiguationOptions{
		NodeTypes:               req.NodeTypes,
		MaxCandidatesPerMention: req.MaxCandidatesPerMention,
		MaxPathLength:           req.MaxPathLength,
		MaxPathSearches:         req.MaxPathSearches,
	})
}
