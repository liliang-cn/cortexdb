package graphflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// LLM-driven knowledge-graph maintenance — create, UPDATE and DELETE.
//
// OrganizeFromBrain only ever adds; a graph that can only grow accumulates
// stale and wrong facts. This is the full mutation surface: an LLM is shown the
// RELEVANT PART OF THE EXISTING GRAPH alongside new text, and answers with a
// set of edits (add / update / delete) to reconcile the two. The edits are then
// applied deterministically here — the model proposes, this code disposes.
//
// Deliberately embedder-free: relevant existing entities are found by lexical
// mention (does the text name this entity?), writes go through the GraphRAG
// upsert path which builds lexical vectors, and nothing here calls an embedder.
// The whole surface works with only a chat model configured.

// Edit ops and kinds.
const (
	EditOpAdd    = "add"
	EditOpUpdate = "update"
	EditOpDelete = "delete"

	EditKindEntity   = "entity"
	EditKindRelation = "relation"
)

// GraphEdit is one proposed mutation.
type GraphEdit struct {
	Op   string `json:"op"`   // add|update|delete
	Kind string `json:"kind"` // entity|relation

	// Entity fields (Kind == "entity").
	Name    string `json:"name,omitempty"`
	Type    string `json:"type,omitempty"`
	Summary string `json:"summary,omitempty"`

	// Relation fields (Kind == "relation").
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	RelType string `json:"rel_type,omitempty"`

	// Reason is the model's justification; surfaced in dry runs.
	Reason string `json:"reason,omitempty"`
}

// GraphEditPlan is a set of mutations.
type GraphEditPlan struct {
	Edits []GraphEdit `json:"edits"`
}

// GraphEditOptions configures how a plan is produced and applied.
type GraphEditOptions struct {
	// LLM is required by UpdateGraphFromText; ApplyGraphEdits ignores it.
	LLM JSONGenerator
	// DryRun reports what would change without touching the graph.
	DryRun bool
	// AllowDelete must be set for delete edits to apply. Deletion is
	// destructive and not reversible, so it is opt-in.
	AllowDelete bool
	// MaxDeletes caps how many deletes may apply in one pass (0 = default 20),
	// so a confused model cannot wipe a graph in a single call.
	MaxDeletes int
	// MaxContextEntities bounds how many existing entities are shown to the
	// model (0 = default 60).
	MaxContextEntities int
}

// GraphEditReport summarizes an applied (or simulated) plan.
type GraphEditReport struct {
	EntitiesAdded    int         `json:"entities_added"`
	EntitiesUpdated  int         `json:"entities_updated"`
	EntitiesDeleted  int         `json:"entities_deleted"`
	RelationsAdded   int         `json:"relations_added"`
	RelationsDeleted int         `json:"relations_deleted"`
	Skipped          []string    `json:"skipped,omitempty"`
	Applied          []GraphEdit `json:"applied,omitempty"`
	DryRun           bool        `json:"dry_run,omitempty"`
}

const defaultMaxDeletes = 20
const defaultMaxContextEntities = 60

// ApplyGraphEdits applies a mutation plan deterministically. It is the single
// write path used by UpdateGraphFromText, and is also useful on its own when a
// caller (or an agent like Claude Code) has already decided on the edits.
func ApplyGraphEdits(ctx context.Context, db *cortexdb.DB, plan GraphEditPlan, opts GraphEditOptions) (*GraphEditReport, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: graph edit: nil db")
	}
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("graphflow: graph edit: init graph schema: %w", err)
	}
	maxDeletes := opts.MaxDeletes
	if maxDeletes <= 0 {
		maxDeletes = defaultMaxDeletes
	}
	report := &GraphEditReport{DryRun: opts.DryRun}
	tools := db.GraphRAGTools()
	deletes := 0

	// Batch the additive edits so they go through one upsert each.
	addEntities := make([]cortexdb.ToolEntityInput, 0)
	addRelations := make([]cortexdb.ToolRelationInput, 0)

	for _, e := range plan.Edits {
		op := strings.ToLower(strings.TrimSpace(e.Op))
		kind := strings.ToLower(strings.TrimSpace(e.Kind))
		if kind == "" {
			// Infer: relation edits carry endpoints.
			if strings.TrimSpace(e.From) != "" && strings.TrimSpace(e.To) != "" {
				kind = EditKindRelation
			} else {
				kind = EditKindEntity
			}
		}

		switch {
		case kind == EditKindEntity && (op == EditOpAdd || op == EditOpUpdate):
			name := collapseSpaces(e.Name)
			if name == "" {
				report.Skipped = append(report.Skipped, "entity edit without a name")
				continue
			}
			exists := entityExists(ctx, db, name)
			if op == EditOpUpdate && !exists {
				report.Skipped = append(report.Skipped, fmt.Sprintf("update: entity %q not found", name))
				continue
			}
			if opts.DryRun {
				if exists {
					report.EntitiesUpdated++
				} else {
					report.EntitiesAdded++
				}
				report.Applied = append(report.Applied, e)
				continue
			}
			in := cortexdb.ToolEntityInput{Name: name, Description: e.Summary}
			if t := strings.TrimSpace(e.Type); t != "" {
				in.Type = t
			}
			addEntities = append(addEntities, in)
			if exists {
				report.EntitiesUpdated++
			} else {
				report.EntitiesAdded++
			}
			report.Applied = append(report.Applied, e)

		case kind == EditKindRelation && op == EditOpAdd:
			from, to := collapseSpaces(e.From), collapseSpaces(e.To)
			if from == "" || to == "" || strings.EqualFold(from, to) {
				report.Skipped = append(report.Skipped, "relation add with missing or self endpoints")
				continue
			}
			if opts.DryRun {
				report.RelationsAdded++
				report.Applied = append(report.Applied, e)
				continue
			}
			relType := strings.TrimSpace(e.RelType)
			if relType == "" {
				relType = "related_to"
			}
			addRelations = append(addRelations, cortexdb.ToolRelationInput{From: from, To: to, Type: relType})
			report.RelationsAdded++
			report.Applied = append(report.Applied, e)

		case op == EditOpDelete:
			if !opts.AllowDelete {
				report.Skipped = append(report.Skipped, "delete skipped (AllowDelete is false)")
				continue
			}
			if deletes >= maxDeletes {
				report.Skipped = append(report.Skipped, fmt.Sprintf("delete cap of %d reached", maxDeletes))
				continue
			}
			if kind == EditKindRelation {
				from, to := collapseSpaces(e.From), collapseSpaces(e.To)
				if from == "" || to == "" {
					report.Skipped = append(report.Skipped, "relation delete with missing endpoints")
					continue
				}
				if opts.DryRun {
					report.RelationsDeleted++
					deletes++
					report.Applied = append(report.Applied, e)
					continue
				}
				n, derr := deleteRelation(ctx, db, from, to, strings.TrimSpace(e.RelType))
				if derr != nil {
					return report, derr
				}
				if n == 0 {
					report.Skipped = append(report.Skipped, fmt.Sprintf("delete: relation %s->%s not found", from, to))
					continue
				}
				report.RelationsDeleted += n
				deletes++
				report.Applied = append(report.Applied, e)
				continue
			}
			name := collapseSpaces(e.Name)
			if name == "" {
				report.Skipped = append(report.Skipped, "entity delete without a name")
				continue
			}
			if !entityExists(ctx, db, name) {
				report.Skipped = append(report.Skipped, fmt.Sprintf("delete: entity %q not found", name))
				continue
			}
			if opts.DryRun {
				report.EntitiesDeleted++
				deletes++
				report.Applied = append(report.Applied, e)
				continue
			}
			// DeleteNode cascades this entity's edges.
			if derr := db.Graph().DeleteNode(ctx, cortexdb.EntityNodeID(name)); derr != nil {
				return report, fmt.Errorf("graphflow: graph edit: delete entity %q: %w", name, derr)
			}
			report.EntitiesDeleted++
			deletes++
			report.Applied = append(report.Applied, e)

		default:
			report.Skipped = append(report.Skipped, fmt.Sprintf("unsupported edit op=%q kind=%q", e.Op, e.Kind))
		}
	}

	if !opts.DryRun {
		if len(addEntities) > 0 {
			if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: addEntities}); err != nil {
				return report, fmt.Errorf("graphflow: graph edit: upsert entities: %w", err)
			}
		}
		if len(addRelations) > 0 {
			if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: addRelations}); err != nil {
				return report, fmt.Errorf("graphflow: graph edit: upsert relations: %w", err)
			}
		}
	}
	return report, nil
}

// UpdateGraphFromText reconciles new text against the existing graph with an
// LLM: it finds the entities the text already shares with the graph, shows the
// model that subgraph plus the text, and asks for the edits — including
// corrections (update) and retractions (delete) — needed to make the graph
// reflect the text. The proposed plan is then applied by ApplyGraphEdits.
//
// Deletes require opts.AllowDelete; use opts.DryRun first to review.
func UpdateGraphFromText(ctx context.Context, db *cortexdb.DB, text string, opts GraphEditOptions) (*GraphEditReport, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: graph edit: nil db")
	}
	if opts.LLM == nil {
		return nil, fmt.Errorf("graphflow: graph edit requires an LLM")
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("graphflow: graph edit: empty text")
	}

	plan, err := proposeGraphEdits(ctx, db, text, opts)
	if err != nil {
		return nil, err
	}
	return ApplyGraphEdits(ctx, db, *plan, opts)
}

// ProposeGraphEdits returns the edits an LLM would make for this text without
// applying them — useful for showing a user a diff before committing.
func ProposeGraphEdits(ctx context.Context, db *cortexdb.DB, text string, opts GraphEditOptions) (*GraphEditPlan, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: graph edit: nil db")
	}
	if opts.LLM == nil {
		return nil, fmt.Errorf("graphflow: graph edit requires an LLM")
	}
	return proposeGraphEdits(ctx, db, text, opts)
}

const graphEditSystemPrompt = "You maintain a knowledge graph. You are given the CURRENT GRAPH (entities and relations already stored) and NEW TEXT. " +
	"Produce the minimal set of edits that makes the graph correctly reflect the new text. " +
	"Use op=add for genuinely new entities/relations; op=update to correct an existing entity's type or summary; " +
	"op=delete ONLY when the new text explicitly contradicts or retracts something already in the graph. " +
	"Never delete something merely because the new text does not mention it. " +
	"Entity names must be the proper noun itself (short), never a sentence. Relation types are short snake_case verb phrases. " +
	"Return JSON only: {\"edits\":[{\"op\":\"add|update|delete\",\"kind\":\"entity|relation\",\"name\":\"\",\"type\":\"\",\"summary\":\"\",\"from\":\"\",\"to\":\"\",\"rel_type\":\"\",\"reason\":\"\"}]}. " +
	"Omit fields that do not apply. Return an empty edits array if nothing should change."

func proposeGraphEdits(ctx context.Context, db *cortexdb.DB, text string, opts GraphEditOptions) (*GraphEditPlan, error) {
	maxCtx := opts.MaxContextEntities
	if maxCtx <= 0 {
		maxCtx = defaultMaxContextEntities
	}
	current, err := relevantSubgraphForText(ctx, db, text, maxCtx)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("CURRENT GRAPH:\n")
	if current == "" {
		b.WriteString("(empty — nothing relevant is stored yet)\n")
	} else {
		b.WriteString(current)
	}
	b.WriteString("\nNEW TEXT:\n")
	b.WriteString(truncateRunes(strings.TrimSpace(text), 6000))
	b.WriteString("\n\nReturn the edits. JSON only.")

	raw, err := opts.LLM.GenerateJSON(ctx, graphEditSystemPrompt, b.String())
	if err != nil {
		return nil, fmt.Errorf("graphflow: graph edit: llm: %w", err)
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		return nil, fmt.Errorf("graphflow: graph edit: %w", err)
	}
	var plan GraphEditPlan
	if err := json.Unmarshal(obj, &plan); err != nil {
		return nil, fmt.Errorf("graphflow: graph edit: decode plan: %w", err)
	}
	return &plan, nil
}

// relevantSubgraphForText renders the part of the graph the text actually talks
// about: entities whose name is mentioned in the text, plus the relations among
// them. Matching is purely lexical — no embedder required — which is what makes
// update/delete usable in a no-embedding-model setup.
func relevantSubgraphForText(ctx context.Context, db *cortexdb.DB, text string, maxEntities int) (string, error) {
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return "", fmt.Errorf("graphflow: graph edit: init graph schema: %w", err)
	}
	names := loadEntityDisplayNames(ctx, db)
	lowerText := strings.ToLower(text)

	type hit struct {
		id, name string
	}
	hits := make([]hit, 0)
	for id, name := range names {
		n := strings.ToLower(strings.TrimSpace(name))
		if len([]rune(n)) < 2 {
			continue
		}
		if strings.Contains(lowerText, n) {
			hits = append(hits, hit{id: id, name: name})
		}
	}
	// Longest names first: they are the most specific matches.
	sort.SliceStable(hits, func(i, j int) bool {
		if len([]rune(hits[i].name)) != len([]rune(hits[j].name)) {
			return len([]rune(hits[i].name)) > len([]rune(hits[j].name))
		}
		return hits[i].name < hits[j].name
	})
	if len(hits) > maxEntities {
		hits = hits[:maxEntities]
	}
	if len(hits) == 0 {
		return "", nil
	}

	inScope := make(map[string]struct{}, len(hits))
	var b strings.Builder
	b.WriteString("Entities:\n")
	for _, h := range hits {
		inScope[h.id] = struct{}{}
		b.WriteString("- ")
		b.WriteString(h.name)
		b.WriteString("\n")
	}

	rels := loadEntityRelations(ctx, db, names)
	lines := make([]string, 0)
	for _, r := range rels {
		if !mentionedIn(lowerText, r.from) || !mentionedIn(lowerText, r.to) {
			continue
		}
		etype := r.etype
		if etype == "" {
			etype = "related_to"
		}
		lines = append(lines, fmt.Sprintf("- %s -%s-> %s", r.from, etype, r.to))
	}
	if len(lines) > 0 {
		sort.Strings(lines)
		b.WriteString("Relations:\n")
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func mentionedIn(lowerText, name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return len([]rune(n)) >= 2 && strings.Contains(lowerText, n)
}

// entityExists reports whether an entity node is already stored.
func entityExists(ctx context.Context, db *cortexdb.DB, name string) bool {
	node, err := db.Graph().GetNode(ctx, cortexdb.EntityNodeID(name))
	return err == nil && node != nil
}

// deleteRelation removes edges between two entities, optionally restricted to
// one relation type. Returns how many edges were removed.
func deleteRelation(ctx context.Context, db *cortexdb.DB, from, to, relType string) (int, error) {
	fromID := cortexdb.EntityNodeID(from)
	toID := cortexdb.EntityNodeID(to)
	query := `DELETE FROM graph_edges WHERE from_node_id = ? AND to_node_id = ?`
	args := []any{fromID, toID}
	if relType != "" {
		query += ` AND edge_type = ?`
		args = append(args, relType)
	}
	res, err := db.SQL().ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("graphflow: graph edit: delete relation: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return int(n), nil
}
