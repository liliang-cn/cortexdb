package graphflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Entity resolution — merge duplicate/alias entity nodes into one canonical
// node, so "CortexDB"/"cortexdb"/"Cortex DB" (and, with an LLM,
// "K8s"/"Kubernetes") stop fragmenting the graph. Deterministic normalization
// catches spelling/casing/punctuation variants; an optional LLM catches
// acronyms and synonyms. Merging repoints every edge to the canonical node
// (via the graph store's MergeEntities) and dedupes the resulting edges.

// ResolveOptions configures ResolveEntities.
type ResolveOptions struct {
	// LLM, when set, additionally proposes acronym/synonym merges that
	// normalization cannot catch (e.g. K8s ↔ Kubernetes). Optional.
	LLM JSONGenerator
	// DryRun reports the merges it would make without applying them.
	DryRun bool
	// NodeTypes, when set, restricts resolution to entities of those types.
	//
	// Resolution reads a node's content as its name, and a store that holds
	// more than one kind of graph will have names that repeat without being
	// duplicates: a code graph gives each symbol an id built from its path and
	// leaves the bare name as the content, so every package's main.go shares a
	// canonical key. Merging those collapses distinct files into one and
	// repoints their edges. Naming the types that resolution is about is what
	// makes it safe to run over such a store.
	//
	// Empty means every entity participates, which is what callers had before
	// this existed.
	NodeTypes []string
}

// ResolveGroup is one set of entities merged into a canonical name.
type ResolveGroup struct {
	Canonical string   `json:"canonical"`
	Aliases   []string `json:"aliases"`
}

// ResolveReport summarizes an entity-resolution pass.
type ResolveReport struct {
	EntitiesBefore int            `json:"entities_before"`
	EntitiesMerged int            `json:"entities_merged"` // alias nodes removed
	Groups         []ResolveGroup `json:"groups"`
	DryRun         bool           `json:"dry_run,omitempty"`
}

type entityInfo struct {
	id     string
	name   string
	degree int
}

// ResolveEntities finds duplicate/alias entities and merges each group into a
// single canonical node. Returns what it did (or would do, when DryRun).
func ResolveEntities(ctx context.Context, db *cortexdb.DB, opts ResolveOptions) (*ResolveReport, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: resolve: nil db")
	}
	entities := loadEntityInfos(ctx, db, opts.NodeTypes)
	report := &ResolveReport{EntitiesBefore: len(entities), DryRun: opts.DryRun}
	if len(entities) < 2 {
		return report, nil
	}

	byID := make(map[string]entityInfo, len(entities))
	byName := make(map[string]entityInfo, len(entities)) // lower(name) -> info
	for _, e := range entities {
		byID[e.id] = e
		byName[strings.ToLower(e.name)] = e
	}

	// Track which entities are already claimed by a merge group so a name is
	// never merged twice.
	claimed := make(map[string]struct{})

	// 1. Deterministic: group by normalized key (case/space/punctuation-insensitive).
	keyGroups := make(map[string][]entityInfo)
	var keyOrder []string
	for _, e := range entities {
		k := canonicalKey(e.name)
		if k == "" {
			continue
		}
		if _, seen := keyGroups[k]; !seen {
			keyOrder = append(keyOrder, k)
		}
		keyGroups[k] = append(keyGroups[k], e)
	}
	groups := make([][]entityInfo, 0)
	for _, k := range keyOrder {
		g := keyGroups[k]
		if len(g) < 2 {
			continue
		}
		groups = append(groups, g)
		for _, e := range g {
			claimed[e.id] = struct{}{}
		}
	}

	// 2. Optional LLM: acronym/synonym groups that normalization missed.
	if opts.LLM != nil {
		for _, g := range llmAliasGroups(ctx, opts.LLM, entities, byName, claimed) {
			groups = append(groups, g)
			for _, e := range g {
				claimed[e.id] = struct{}{}
			}
		}
	}

	// 3. Merge each group into its canonical (highest degree, tie → longer name).
	for _, g := range groups {
		canonical := pickCanonical(g)
		aliases := make([]entityInfo, 0, len(g)-1)
		for _, e := range g {
			if e.id != canonical.id {
				aliases = append(aliases, e)
			}
		}
		if len(aliases) == 0 {
			continue
		}
		aliasNames := make([]string, len(aliases))
		aliasIDs := make([]string, len(aliases))
		for i, a := range aliases {
			aliasNames[i] = a.name
			aliasIDs[i] = a.id
		}
		report.Groups = append(report.Groups, ResolveGroup{Canonical: canonical.name, Aliases: aliasNames})
		report.EntitiesMerged += len(aliases)

		if opts.DryRun {
			continue
		}
		if err := db.Graph().MergeEntities(ctx, canonical.id, aliasIDs); err != nil {
			return nil, fmt.Errorf("graphflow: resolve merge %q: %w", canonical.name, err)
		}
		recordAliases(ctx, db, canonical.id, aliasNames)
	}

	if !opts.DryRun && report.EntitiesMerged > 0 {
		dedupeEntityEdges(ctx, db)
	}
	return report, nil
}

// canonicalKey normalizes a name to a case/space/punctuation-insensitive key,
// so "CortexDB", "cortexdb", "Cortex DB", "cortex-db" collapse together.
//
// A leading dash survives, because a flag is not a spelling of an identifier.
// Dropping it collapsed "--read-only" onto "ReadOnly" and merged them under
// whichever won, so the graph could no longer tell the thing you paste into a
// shell from the field of the same idea — which is the one property a CLI's
// help text is ingested for. Two spellings of one flag still meet: only the
// dash is preserved, not the punctuation after it.
func canonicalKey(name string) string {
	var b strings.Builder
	if strings.HasPrefix(strings.TrimSpace(name), "-") {
		b.WriteRune('-')
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// pickCanonical chooses the surviving node: highest degree, then longest name
// (a fuller proper noun like "Kubernetes" over "K8s"), then lexical.
func pickCanonical(group []entityInfo) entityInfo {
	best := group[0]
	for _, e := range group[1:] {
		switch {
		case e.degree != best.degree:
			if e.degree > best.degree {
				best = e
			}
		case len([]rune(e.name)) != len([]rune(best.name)):
			if len([]rune(e.name)) > len([]rune(best.name)) {
				best = e
			}
		default:
			if e.name < best.name {
				best = e
			}
		}
	}
	return best
}

// loadEntityInfos returns every entity node with its display name and degree.
func loadEntityInfos(ctx context.Context, db *cortexdb.DB, nodeTypes []string) []entityInfo {
	degree := make(map[string]int)
	if rows, err := db.SQL().QueryContext(ctx, `SELECT from_node_id, to_node_id FROM graph_edges`); err == nil {
		for rows.Next() {
			var f, t string
			if err := rows.Scan(&f, &t); err != nil {
				break
			}
			degree[f]++
			degree[t]++
		}
		_ = rows.Close()
	}
	query := `SELECT id, COALESCE(content,'') FROM graph_nodes WHERE id LIKE 'entity:%'`
	args := make([]any, 0, len(nodeTypes))
	if len(nodeTypes) > 0 {
		query += ` AND node_type IN (` + strings.TrimSuffix(strings.Repeat("?,", len(nodeTypes)), ",") + `)`
		for _, t := range nodeTypes {
			args = append(args, t)
		}
	}
	rows, err := db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	out := make([]entityInfo, 0)
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return out
		}
		name := strings.TrimSpace(content)
		if name == "" {
			name = trimEntityPrefix(id)
		}
		out = append(out, entityInfo{id: id, name: name, degree: degree[id]})
	}
	// Stable order for deterministic canonical selection / output.
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

const resolveSystemPrompt = "You find entities that refer to the SAME real-world thing under different names " +
	"(acronyms, abbreviations, synonyms, plural forms, and cross-language names, " +
	"e.g. K8s = Kubernetes, Postgres = PostgreSQL, VM = VMs, \u674e\u5458\u5916 = Liang Li). " +
	"ONLY group names that are truly the same entity — never merely related ones. " +
	"Use the canonical (fullest, most standard) name as the canonical. " +
	"Return JSON only: {\"groups\":[{\"canonical\":\"…\",\"aliases\":[\"…\"]}]}. Omit singletons."

// llmAliasGroups asks the LLM for acronym/synonym alias groups among the
// not-yet-claimed entities, and maps proposed names back to existing nodes.
func llmAliasGroups(ctx context.Context, llm JSONGenerator, entities []entityInfo, byName map[string]entityInfo, claimed map[string]struct{}) [][]entityInfo {
	names := make([]string, 0, len(entities))
	for _, e := range entities {
		if _, done := claimed[e.id]; done {
			continue
		}
		names = append(names, e.name)
	}
	if len(names) < 2 {
		return nil
	}
	user := "Entities:\n" + strings.Join(names, "\n") + "\n\nGroup the ones that are the same entity. JSON only."
	raw, err := llm.GenerateJSON(ctx, resolveSystemPrompt, user)
	if err != nil {
		return nil
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		return nil
	}
	var parsed struct {
		Groups []struct {
			Canonical string   `json:"canonical"`
			Aliases   []string `json:"aliases"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(obj, &parsed); err != nil {
		return nil
	}
	out := make([][]entityInfo, 0)
	for _, g := range parsed.Groups {
		members := make([]entityInfo, 0)
		seen := make(map[string]struct{})
		for _, nm := range append([]string{g.Canonical}, g.Aliases...) {
			info, ok := byName[strings.ToLower(strings.TrimSpace(nm))]
			if !ok {
				continue
			}
			if _, dup := seen[info.id]; dup {
				continue
			}
			if _, done := claimed[info.id]; done {
				continue
			}
			seen[info.id] = struct{}{}
			members = append(members, info)
		}
		if len(members) >= 2 {
			out = append(out, members)
		}
	}
	return out
}

// recordAliases stores the merged alias names on the canonical node's
// properties, so the canonicalization is inspectable (and future-searchable).
func recordAliases(ctx context.Context, db *cortexdb.DB, canonicalID string, aliases []string) {
	node, err := db.Graph().GetNode(ctx, canonicalID)
	if err != nil || node == nil {
		return
	}
	if node.Properties == nil {
		node.Properties = map[string]interface{}{}
	}
	existing := map[string]struct{}{}
	if prev, ok := node.Properties["aliases"].([]interface{}); ok {
		for _, a := range prev {
			if s, ok := a.(string); ok {
				existing[s] = struct{}{}
			}
		}
	}
	merged := make([]string, 0, len(existing)+len(aliases))
	for s := range existing {
		merged = append(merged, s)
	}
	for _, a := range aliases {
		if _, ok := existing[a]; !ok {
			merged = append(merged, a)
			existing[a] = struct{}{}
		}
	}
	sort.Strings(merged)
	node.Properties["aliases"] = merged
	_ = db.Graph().UpsertNode(ctx, node)
}

// dedupeEntityEdges removes duplicate entity↔entity edges (same from/to/type)
// left behind after repointing, keeping one per group.
func dedupeEntityEdges(ctx context.Context, db *cortexdb.DB) {
	_, _ = db.SQL().ExecContext(ctx, `
		DELETE FROM graph_edges
		WHERE from_node_id LIKE 'entity:%' AND to_node_id LIKE 'entity:%'
		  AND rowid NOT IN (
			SELECT MIN(rowid) FROM graph_edges
			GROUP BY from_node_id, to_node_id, edge_type
		  )`)
}
