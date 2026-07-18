package graphflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Community detection + summarization + global search — the Microsoft-GraphRAG
// shape. Louvain (in pkg/graph) partitions the entity graph into communities;
// an LLM writes a report per community; global search then answers
// whole-corpus questions ("what are the main themes?") by map-reducing over
// those community reports instead of retrieving individual chunks.

const (
	defaultMinCommunitySize    = 3
	defaultMaxEntitiesInPrompt = 40
	communityCollection        = "communities"
	globalMapCharBudget        = 6000
)

// CommunitySummary is one LLM-written community report.
type CommunitySummary struct {
	ID       int      `json:"id"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Findings []string `json:"findings,omitempty"`
	Entities []string `json:"entities"`
	Size     int      `json:"size"`
}

// CommunityReport is the set of community summaries produced by one build.
type CommunityReport struct {
	Communities []CommunitySummary `json:"communities"`
}

// CommunityOptions configures BuildCommunitySummaries.
type CommunityOptions struct {
	LLM     JSONGenerator // required: writes each community report
	MinSize int           // skip communities with fewer entities (default 3)
	Max     int           // cap communities summarized (0 = all)
}

// BuildCommunitySummaries detects entity communities (Louvain) and writes an
// LLM report for each, persisting them as knowledge documents in the
// "communities" collection (so they are retrievable and survive across runs)
// and returning them. It is the prerequisite for GlobalSearch. Per-community
// LLM failures are non-fatal (that community is skipped).
func BuildCommunitySummaries(ctx context.Context, db *cortexdb.DB, opts CommunityOptions) (*CommunityReport, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: community: nil db")
	}
	if opts.LLM == nil {
		return nil, fmt.Errorf("graphflow: community summaries require an LLM")
	}
	minSize := opts.MinSize
	if minSize <= 0 {
		minSize = defaultMinCommunitySize
	}

	communities, err := db.Graph().CommunityDetection(ctx)
	if err != nil {
		return nil, fmt.Errorf("graphflow: community detection: %w", err)
	}
	names := loadEntityDisplayNames(ctx, db)
	relations := loadEntityRelations(ctx, db, names)

	report := &CommunityReport{}
	count := 0
	for _, c := range communities {
		members := make([]string, 0, len(c.Nodes))
		memberSet := make(map[string]struct{}, len(c.Nodes))
		for _, nid := range c.Nodes {
			if name, ok := names[nid]; ok { // entity nodes only
				members = append(members, name)
				memberSet[strings.ToLower(name)] = struct{}{}
			}
		}
		if len(members) < minSize {
			continue
		}
		if opts.Max > 0 && count >= opts.Max {
			break
		}
		sort.Strings(members)
		promptMembers := members
		if len(promptMembers) > defaultMaxEntitiesInPrompt {
			promptMembers = promptMembers[:defaultMaxEntitiesInPrompt]
		}
		rels := relationsWithin(relations, memberSet)

		summary, err := summarizeCommunity(ctx, opts.LLM, promptMembers, rels)
		if err != nil {
			continue
		}
		summary.ID = c.ID
		summary.Size = len(members)
		summary.Entities = promptMembers
		report.Communities = append(report.Communities, *summary)

		// Persist as a knowledge document so it is retrievable and reusable.
		if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
			KnowledgeID: fmt.Sprintf("community:%d", c.ID),
			Title:       summary.Title,
			Content:     communityDocContent(summary),
			Collection:  communityCollection,
			Metadata:    map[string]string{"kind": "community", "size": fmt.Sprintf("%d", len(members))},
		}); err != nil {
			// Persistence failure is non-fatal; the report is still returned.
			continue
		}
		count++
	}
	return report, nil
}

// GlobalSearchResult is the answer to a whole-corpus question.
type GlobalSearchResult struct {
	Query            string   `json:"query"`
	Answer           string   `json:"answer"`
	CommunitiesUsed  int      `json:"communities_used"`
	SupportingPoints []string `json:"supporting_points,omitempty"`
}

// GlobalSearchOptions configures GlobalSearch.
type GlobalSearchOptions struct {
	LLM       JSONGenerator // required
	MaxPoints int           // top key points fed to the reduce step (default 12)
	// BuildIfEmpty builds community summaries first when none are persisted yet.
	BuildIfEmpty bool
}

// GlobalSearch answers a whole-corpus question by map-reducing over community
// reports (Microsoft-GraphRAG global search): each community's report yields
// query-relevant key points with helpfulness scores (map), and the top points
// are synthesized into one answer (reduce). Requires community summaries to
// exist (BuildCommunitySummaries) unless BuildIfEmpty is set.
func GlobalSearch(ctx context.Context, db *cortexdb.DB, query string, opts GlobalSearchOptions) (*GlobalSearchResult, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: global search: nil db")
	}
	if opts.LLM == nil {
		return nil, fmt.Errorf("graphflow: global search requires an LLM")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("graphflow: global search: empty query")
	}
	maxPoints := opts.MaxPoints
	if maxPoints <= 0 {
		maxPoints = 12
	}

	summaries := loadPersistedCommunities(ctx, db)
	if len(summaries) == 0 && opts.BuildIfEmpty {
		if _, err := BuildCommunitySummaries(ctx, db, CommunityOptions{LLM: opts.LLM}); err != nil {
			return nil, err
		}
		summaries = loadPersistedCommunities(ctx, db)
	}
	if len(summaries) == 0 {
		return nil, fmt.Errorf("graphflow: no community summaries — run BuildCommunitySummaries first")
	}

	// Map: score each community's contribution to the query, in char-bounded batches.
	type scoredPoint struct {
		Point string
		Score float64
	}
	points := make([]scoredPoint, 0)
	for _, batch := range batchCommunityDocs(summaries, globalMapCharBudget) {
		mapped, err := mapCommunities(ctx, opts.LLM, query, batch)
		if err != nil {
			continue
		}
		for _, p := range mapped {
			if strings.TrimSpace(p.Point) != "" {
				points = append(points, scoredPoint{Point: p.Point, Score: p.Score})
			}
		}
	}
	if len(points) == 0 {
		return &GlobalSearchResult{Query: query, Answer: "No community context was relevant to this question.", CommunitiesUsed: len(summaries)}, nil
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].Score > points[j].Score })
	if len(points) > maxPoints {
		points = points[:maxPoints]
	}
	topPoints := make([]string, len(points))
	for i, p := range points {
		topPoints[i] = p.Point
	}

	// Reduce: synthesize a final answer from the top key points.
	answer, err := reduceAnswer(ctx, opts.LLM, query, topPoints)
	if err != nil {
		return nil, err
	}
	return &GlobalSearchResult{
		Query:            query,
		Answer:           answer,
		CommunitiesUsed:  len(summaries),
		SupportingPoints: topPoints,
	}, nil
}

// --- LLM steps ---

const communitySystemPrompt = "You write a concise report describing a community of related entities from a knowledge graph. " +
	"Given the member entities and the relationships among them, infer what this community is about. " +
	"Return JSON only: {\"title\":\"short title\",\"summary\":\"2-4 sentence description\",\"findings\":[\"key point\", …]}."

func summarizeCommunity(ctx context.Context, llm JSONGenerator, members, relations []string) (*CommunitySummary, error) {
	var b strings.Builder
	b.WriteString("Entities:\n")
	b.WriteString(strings.Join(members, ", "))
	if len(relations) > 0 {
		b.WriteString("\n\nRelationships:\n")
		b.WriteString(strings.Join(relations, "\n"))
	}
	b.WriteString("\n\nWrite the community report as specified. JSON only.")
	raw, err := llm.GenerateJSON(ctx, communitySystemPrompt, b.String())
	if err != nil {
		return nil, err
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Title    string   `json:"title"`
		Summary  string   `json:"summary"`
		Findings []string `json:"findings"`
	}
	if err := json.Unmarshal(obj, &parsed); err != nil {
		return nil, err
	}
	if strings.TrimSpace(parsed.Summary) == "" && strings.TrimSpace(parsed.Title) == "" {
		return nil, fmt.Errorf("empty community summary")
	}
	if parsed.Title == "" {
		parsed.Title = truncateRunes(parsed.Summary, 60)
	}
	return &CommunitySummary{Title: parsed.Title, Summary: parsed.Summary, Findings: parsed.Findings}, nil
}

const globalMapSystemPrompt = "You extract, from community reports, the points that help answer the user's question. " +
	"For each relevant point give a helpfulness score from 0 (irrelevant) to 100 (directly answers it). " +
	"Ignore irrelevant communities. Return JSON only: {\"points\":[{\"point\":\"…\",\"score\":0-100}, …]}."

type mappedPoint struct {
	Point string  `json:"point"`
	Score float64 `json:"score"`
}

func mapCommunities(ctx context.Context, llm JSONGenerator, query string, batch string) ([]mappedPoint, error) {
	user := "Question: " + query + "\n\nCommunity reports:\n\n" + batch + "\n\nExtract helpful points with scores. JSON only."
	raw, err := llm.GenerateJSON(ctx, globalMapSystemPrompt, user)
	if err != nil {
		return nil, err
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Points []mappedPoint `json:"points"`
	}
	if err := json.Unmarshal(obj, &parsed); err != nil {
		return nil, err
	}
	return parsed.Points, nil
}

const globalReduceSystemPrompt = "You answer the user's question using only the provided key points drawn from a whole corpus. " +
	"Synthesize a clear, well-structured answer; do not invent facts beyond the points. " +
	"Return JSON only: {\"answer\":\"…\"}."

func reduceAnswer(ctx context.Context, llm JSONGenerator, query string, points []string) (string, error) {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(query)
	b.WriteString("\n\nKey points:\n")
	for _, p := range points {
		b.WriteString("- ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	b.WriteString("\nWrite the final answer. JSON only.")
	raw, err := llm.GenerateJSON(ctx, globalReduceSystemPrompt, b.String())
	if err != nil {
		return "", err
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(obj, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Answer) == "" {
		return "", fmt.Errorf("empty reduce answer")
	}
	return parsed.Answer, nil
}

// --- data loading helpers ---

// loadEntityDisplayNames maps entity node id -> display name (entity nodes only).
func loadEntityDisplayNames(ctx context.Context, db *cortexdb.DB) map[string]string {
	out := make(map[string]string)
	rows, err := db.SQL().QueryContext(ctx, `SELECT id, COALESCE(content,'') FROM graph_nodes WHERE id LIKE 'entity:%'`)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return out
		}
		name := strings.TrimSpace(content)
		if name == "" {
			name = trimEntityPrefix(id)
		}
		out[id] = name
	}
	return out
}

type relEdge struct{ from, etype, to string } // display names

// loadEntityRelations returns meaningful entity↔entity relations as display-name triples.
func loadEntityRelations(ctx context.Context, db *cortexdb.DB, names map[string]string) []relEdge {
	rows, err := db.SQL().QueryContext(ctx,
		`SELECT from_node_id, COALESCE(edge_type,''), to_node_id FROM graph_edges WHERE edge_type NOT IN ('has_chunk','next','mentions','in_community')`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	out := make([]relEdge, 0)
	for rows.Next() {
		var from, etype, to string
		if err := rows.Scan(&from, &etype, &to); err != nil {
			return out
		}
		fn, ok1 := names[from]
		tn, ok2 := names[to]
		if ok1 && ok2 {
			out = append(out, relEdge{from: fn, etype: etype, to: tn})
		}
	}
	return out
}

// relationsWithin renders relations whose both endpoints are in the member set.
func relationsWithin(all []relEdge, memberSet map[string]struct{}) []string {
	out := make([]string, 0)
	for _, e := range all {
		if _, ok := memberSet[strings.ToLower(e.from)]; !ok {
			continue
		}
		if _, ok := memberSet[strings.ToLower(e.to)]; !ok {
			continue
		}
		etype := e.etype
		if etype == "" {
			etype = "related_to"
		}
		out = append(out, fmt.Sprintf("%s -%s-> %s", e.from, etype, e.to))
	}
	return out
}

// loadPersistedCommunities reads community reports from the knowledge store.
func loadPersistedCommunities(ctx context.Context, db *cortexdb.DB) []CommunitySummary {
	rows, err := db.SQL().QueryContext(ctx,
		`SELECT id, COALESCE(title,''), COALESCE(content,'') FROM documents WHERE id LIKE 'community:%' ORDER BY id`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	out := make([]CommunitySummary, 0)
	for rows.Next() {
		var id, title, content string
		if err := rows.Scan(&id, &title, &content); err != nil {
			return out
		}
		out = append(out, CommunitySummary{Title: title, Summary: content})
	}
	return out
}

// communityDocContent renders a community summary as a knowledge document body.
func communityDocContent(s *CommunitySummary) string {
	var b strings.Builder
	b.WriteString(s.Summary)
	for _, f := range s.Findings {
		b.WriteString("\n- ")
		b.WriteString(f)
	}
	return strings.TrimSpace(b.String())
}

// batchCommunityDocs groups community reports into char-bounded map batches.
func batchCommunityDocs(summaries []CommunitySummary, maxChars int) []string {
	batches := make([]string, 0)
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			batches = append(batches, b.String())
			b.Reset()
		}
	}
	for _, s := range summaries {
		doc := "## " + s.Title + "\n" + s.Summary
		if b.Len() > 0 && b.Len()+len(doc)+2 > maxChars {
			flush()
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(truncateRunes(doc, maxChars))
	}
	flush()
	return batches
}

// extractJSONObject strips <think> blocks and returns the outermost JSON object.
func extractJSONObject(raw []byte) ([]byte, error) {
	text := thinkTagRE.ReplaceAllString(string(raw), "")
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("graphflow: no json object in model output")
	}
	return []byte(text[start : end+1]), nil
}

func trimEntityPrefix(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}
