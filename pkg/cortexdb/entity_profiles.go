package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Entity profiles: a kept, up-to-date account of one entity (#253).
//
// Without one, "what do we know about X" was rebuilt from whichever chunks
// ranked highest for the question's wording, so an entity mentioned in 30
// documents was the 4 chunks that happened to match. A profile says what the
// entity is, its relations with their times, and what each source says of
// it, each statement citing its chunk.
//
// Upkeep needs no hook in any write path. A profile records the fingerprint
// of the edges it was built from (how many touch the entity, the newest one);
// a write that adds or retires a mention or a relation changes it, which is
// what makes the profile stale, whichever tool wrote. A stale profile is
// rebuilt when it is read, one entity at a time, and the rebuilt one
// supersedes the old, which is kept in entity_profile_history.
const (
	// minProfileDocuments is how many documents must mention an entity
	// before recall keeps a profile of it unasked; a caller naming it gets
	// one regardless.
	minProfileDocuments = 2
	maxProfileFacts     = 12
	maxProfileSources   = 8
	maxProfileChars     = 1500
	// maxRecallProfiles is how many profiles a recall puts in its pack.
	maxRecallProfiles = 3
)

// EntityProfile is the kept account of one entity.
type EntityProfile struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	// ChunkIDs are the chunks its statements cite.
	ChunkIDs      []string `json:"chunk_ids,omitempty"`
	DocumentCount int      `json:"document_count"`
	// Version counts the rebuilds; the earlier ones are in history.
	Version int    `json:"version"`
	BuiltAt string `json:"built_at"`
	// BuiltBy is "reflector" when the KnowledgeMemoryReflector wrote it,
	// "facts" when it was assembled from the graph and the excerpts.
	BuiltBy string `json:"built_by"`
	// Stale says the entity's sources changed after it was built and it was
	// served without a rebuild.
	Stale bool `json:"stale,omitempty"`
}

func (db *DB) ensureEntityProfileTables(ctx context.Context) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS entity_profiles (
			entity_id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			content TEXT NOT NULL,
			chunk_ids TEXT NOT NULL DEFAULT '[]',
			document_count INTEGER NOT NULL DEFAULT 0,
			fingerprint TEXT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			built_at TEXT NOT NULL,
			built_by TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS entity_profile_history (
			entity_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			name TEXT NOT NULL,
			content TEXT NOT NULL,
			chunk_ids TEXT NOT NULL DEFAULT '[]',
			built_at TEXT NOT NULL,
			built_by TEXT NOT NULL,
			superseded_at TEXT NOT NULL,
			PRIMARY KEY (entity_id, version)
		)`,
	} {
		if _, err := db.exec(ctx, stmt); err != nil {
			return fmt.Errorf("create entity profile tables: %w", err)
		}
	}
	return nil
}

// profileSources is what an entity's profile is built from.
type profileSources struct {
	fingerprint string
	documents   int
	// chunks mentioning the entity, by document, in chunk order.
	chunks map[string][]profileChunk
	docs   []string
	hidden map[string]struct{}
	// relations are the entity's relations a document states.
	relations []*graph.GraphEdge
}

type profileChunk struct{ id, text string }

// chunkDocument is the document a chunk node id names: chunk:<doc>:<n>.
func chunkDocument(chunkID string) string {
	s := strings.TrimPrefix(chunkID, "chunk:")
	if i := strings.LastIndex(s, ":"); i > 0 {
		return s[:i]
	}
	return s
}

func (db *DB) readProfileSources(ctx context.Context, entityID string) (*profileSources, error) {
	// What documents say of the entity, and nothing else: the chunks that
	// mention it and the relations a document states. A memory's mention or
	// relation is its owner's, and a profile is shared.
	var n int
	var newest sql.NullString
	if err := db.queryRow(ctx, `SELECT COUNT(*), MAX(CAST(created_at AS TEXT)) FROM graph_edges
		WHERE to_node_id = ? AND edge_type = 'mentions' AND from_node_id LIKE 'chunk:%'`, entityID).Scan(&n, &newest); err != nil {
		return nil, fmt.Errorf("read entity mentions: %w", err)
	}
	src := &profileSources{fingerprint: fmt.Sprintf("%d|%s", n, newest.String), chunks: map[string][]profileChunk{}}
	edges, err := db.Graph().GetEdges(ctx, entityID, "both")
	if err != nil {
		return nil, fmt.Errorf("read entity relations: %w", err)
	}
	var relationIDs []string
	for _, e := range edges {
		if e == nil || !isEntityNodeID(e.FromNodeID) || !isEntityNodeID(e.ToNodeID) || isWeakPredicate(e.EdgeType) {
			continue
		}
		doc, _ := e.Properties["document_id"].(string)
		if doc == "" {
			continue
		}
		src.relations = append(src.relations, e)
		relationIDs = append(relationIDs, e.ID+"@"+e.CreatedAt.UTC().Format(time.RFC3339Nano))
	}
	sort.Strings(relationIDs)
	src.fingerprint += "|" + strings.Join(relationIDs, ",")
	rows, err := db.query(ctx, `SELECT c.id, COALESCE(c.content, '') FROM graph_edges m
		JOIN graph_nodes c ON c.id = m.from_node_id
		WHERE m.to_node_id = ? AND m.edge_type = 'mentions' AND m.from_node_id LIKE 'chunk:%'
		ORDER BY c.id`, entityID)
	if err != nil {
		return nil, fmt.Errorf("read entity mentions: %w", err)
	}
	if err := eachRow(rows, func() error {
		var c profileChunk
		if err := rows.Scan(&c.id, &c.text); err != nil {
			return err
		}
		doc := chunkDocument(c.id)
		if _, seen := src.chunks[doc]; !seen {
			src.docs = append(src.docs, doc)
		}
		src.chunks[doc] = append(src.chunks[doc], c)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read entity mentions: %w", err)
	}
	// A document no plain read may see (expired, or held back by the review
	// gate) is no source: the profile is shared. Which ones are hidden is
	// part of the fingerprint, so a document expiring rebuilds it.
	among := append([]string(nil), src.docs...)
	for _, e := range src.relations {
		doc, _ := e.Properties["document_id"].(string)
		among = append(among, doc)
	}
	src.hidden = db.hiddenAmong(ctx, nil, among)
	visible := src.docs[:0:0]
	for _, doc := range src.docs {
		if _, gone := src.hidden[doc]; gone {
			delete(src.chunks, doc)
			continue
		}
		visible = append(visible, doc)
	}
	hidden := make([]string, 0, len(src.hidden))
	for doc := range src.hidden {
		hidden = append(hidden, doc)
	}
	sort.Strings(hidden)
	src.fingerprint += "|" + strings.Join(hidden, ",")
	src.docs = visible
	src.documents = len(src.docs)
	return src, nil
}

func (db *DB) loadEntityProfile(ctx context.Context, entityID string) (*EntityProfile, string, error) {
	var p EntityProfile
	var chunkIDs, fingerprint string
	err := db.queryRow(ctx, `SELECT entity_id, name, content, chunk_ids, document_count, fingerprint, version, built_at, built_by
		FROM entity_profiles WHERE entity_id = ?`, entityID).Scan(&p.EntityID, &p.Name, &p.Content, &chunkIDs, &p.DocumentCount, &fingerprint, &p.Version, &p.BuiltAt, &p.BuiltBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read entity profile: %w", err)
	}
	_ = json.Unmarshal([]byte(chunkIDs), &p.ChunkIDs)
	return &p, fingerprint, nil
}

// saveEntityProfile writes p, moving the profile it replaces to history.
func (db *DB) saveEntityProfile(ctx context.Context, p *EntityProfile, fingerprint string, previous *EntityProfile) error {
	ids, _ := json.Marshal(p.ChunkIDs)
	tx, err := db.store.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if previous != nil {
		prevIDs, _ := json.Marshal(previous.ChunkIDs)
		if _, err := db.txExec(ctx, tx, (`INSERT INTO entity_profile_history (entity_id, version, name, content, chunk_ids, built_at, built_by, superseded_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), previous.EntityID, previous.Version, previous.Name, previous.Content, string(prevIDs), previous.BuiltAt, previous.BuiltBy, p.BuiltAt); err != nil {
			return fmt.Errorf("keep entity profile history: %w", err)
		}
		if _, err := db.txExec(ctx, tx, (`DELETE FROM entity_profiles WHERE entity_id = ?`), p.EntityID); err != nil {
			return err
		}
	}
	if _, err := db.txExec(ctx, tx, (`INSERT INTO entity_profiles (entity_id, name, content, chunk_ids, document_count, fingerprint, version, built_at, built_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`), p.EntityID, p.Name, p.Content, string(ids), p.DocumentCount, fingerprint, p.Version, p.BuiltAt, p.BuiltBy); err != nil {
		return fmt.Errorf("save entity profile: %w", err)
	}
	return tx.Commit()
}

// EntityProfile returns the profile of an entity, rebuilding it when its
// sources changed since it was built. Without named, an entity fewer than
// minProfileDocuments documents mention has none (nil). rebuild false serves
// a stale profile as it is, marked Stale, rather than rebuilding it; an
// entity with no profile yet is built either way.
func (b *KnowledgeMemory) EntityProfile(ctx context.Context, entityID string, named, rebuild bool) (*EntityProfile, error) {
	db := b.db
	// Only an entity has a profile: a chunk or document node is its own text.
	if !isEntityNodeID(entityID) {
		return nil, nil
	}
	node, err := db.Graph().GetNode(ctx, entityID)
	if err != nil || node == nil {
		return nil, nil
	}
	src, err := db.readProfileSources(ctx, entityID)
	if err != nil {
		return nil, err
	}
	if !named && src.documents < minProfileDocuments {
		return nil, nil
	}
	var stored *EntityProfile
	var fingerprint string
	if db.entityProfilesKept {
		if stored, fingerprint, err = db.loadEntityProfile(ctx, entityID); err != nil {
			return nil, err
		}
	}
	if stored != nil && fingerprint == src.fingerprint {
		return stored, nil
	}
	if stored != nil && !rebuild {
		stored.Stale = true
		return stored, nil
	}
	p, err := b.buildEntityProfile(ctx, node.ID, firstNonEmpty(strings.TrimSpace(node.Content), entityLabelFromID(node.ID)), node.NodeType, src)
	if err != nil {
		return nil, err
	}
	p.Version = 1
	if stored != nil {
		p.Version = stored.Version + 1
	}
	// Keeping it is a cache: a brain that cannot write (opened read-only)
	// still answers with the profile it built.
	if db.entityProfilesKept {
		if err := db.saveEntityProfile(ctx, p, src.fingerprint, stored); err != nil {
			slog.Warn("cortexdb: entity profile not kept", "entity", entityID, "err", err)
		}
	}
	return p, nil
}

// EntityProfileHistory returns an entity's superseded profiles, newest first.
func (b *KnowledgeMemory) EntityProfileHistory(ctx context.Context, entityID string) ([]EntityProfile, error) {
	rows, err := b.db.query(ctx, `SELECT entity_id, version, name, content, chunk_ids, built_at, built_by
		FROM entity_profile_history WHERE entity_id = ? ORDER BY version DESC`, entityID)
	if err != nil {
		return nil, fmt.Errorf("read entity profile history: %w", err)
	}
	var out []EntityProfile
	err = eachRow(rows, func() error {
		var p EntityProfile
		var ids string
		if err := rows.Scan(&p.EntityID, &p.Version, &p.Name, &p.Content, &ids, &p.BuiltAt, &p.BuiltBy); err != nil {
			return err
		}
		_ = json.Unmarshal([]byte(ids), &p.ChunkIDs)
		out = append(out, p)
		return nil
	})
	return out, err
}

func (b *KnowledgeMemory) buildEntityProfile(ctx context.Context, entityID, name, nodeType string, src *profileSources) (*EntityProfile, error) {
	facts, err := b.entityFacts(ctx, src)
	if err != nil {
		return nil, err
	}
	// One excerpt per document: the first sentence of its chunks that names
	// the entity, else the start of its first chunk.
	type excerpt struct{ doc, chunkID, text string }
	var excerpts []excerpt
	lower := strings.ToLower(name)
	for _, doc := range src.docs {
		if len(excerpts) == maxProfileSources {
			break
		}
		chunks := src.chunks[doc]
		e := excerpt{doc: doc, chunkID: chunks[0].id, text: clipString(strings.TrimSpace(chunks[0].text), 200)}
	found:
		for _, c := range chunks {
			for _, s := range splitSentences(c.text) {
				if containsWord(strings.ToLower(s), lower) {
					e.chunkID, e.text = c.id, clipString(s, 240)
					break found
				}
			}
		}
		excerpts = append(excerpts, e)
	}

	p := &EntityProfile{EntityID: entityID, Name: name, DocumentCount: src.documents, BuiltAt: time.Now().UTC().Format(time.RFC3339Nano), BuiltBy: "facts"}
	for _, e := range excerpts {
		p.ChunkIDs = append(p.ChunkIDs, e.chunkID)
	}

	if reflector := b.db.KnowledgeMemoryReflector; reflector != nil {
		chunks := make([]GraphRAGChunkResult, 0, len(excerpts))
		for _, doc := range src.docs {
			for _, c := range src.chunks[doc] {
				if len(chunks) == 2*maxProfileSources {
					break
				}
				chunks = append(chunks, GraphRAGChunkResult{ID: c.id, DocumentID: doc, Content: c.text})
			}
		}
		input := KnowledgeMemoryReflectInput{Recall: KnowledgeMemoryRecallResponse{
			Query:      name,
			Chunks:     chunks,
			GraphFacts: facts,
			Entities:   []string{name},
		}}
		instructions := fmt.Sprintf("Write a profile of %s: what it is, its current attributes and relations with their times, and what each source says about it. Cite the chunk id of each statement in brackets. Keep to what the sources say.", name)
		reflection, err := reflector.Reflect(ctx, KnowledgeMemoryReflectRequest{Recall: KnowledgeMemoryRecallRequest{Query: name}, Instructions: instructions, MaxSummaryChars: maxProfileChars}, input)
		if err != nil {
			return nil, fmt.Errorf("reflect entity profile of %s: %w", name, err)
		}
		if reflection != nil && strings.TrimSpace(reflection.Summary) != "" {
			p.Content = clipString(strings.TrimSpace(reflection.Summary), maxProfileChars)
			p.BuiltBy = "reflector"
			if len(reflection.SourceChunkIDs) > 0 {
				p.ChunkIDs = reflection.SourceChunkIDs
			}
			return p, nil
		}
	}

	var w strings.Builder
	kind := ""
	if t := strings.TrimSpace(nodeType); t != "" && t != "entity" {
		kind = " (" + t + ")"
	}
	fmt.Fprintf(&w, "%s%s, mentioned in %d documents.", name, kind, src.documents)
	if len(facts) > 0 {
		w.WriteString("\nRelations:")
		for _, f := range facts {
			line := fmt.Sprintf("%s —%s→ %s", f.Subject, f.Predicate, f.Object)
			if when := factPeriod(f.ValidFrom, f.ValidTo); when != "" {
				line += " (" + when + ")"
			}
			w.WriteString("\n- " + line)
		}
	}
	if len(excerpts) > 0 {
		w.WriteString("\nSources:")
		for _, e := range excerpts {
			fmt.Fprintf(&w, "\n- %s: %s [%s]", e.doc, e.text, e.chunkID)
		}
	}
	p.Content = clipString(w.String(), maxProfileChars)
	return p, nil
}

// entityFacts are the relations documents state of the entity, grouped by
// kind and in time order within a kind.
func (b *KnowledgeMemory) entityFacts(ctx context.Context, src *profileSources) ([]KnowledgeMemoryGraphFact, error) {
	label := func(id string) string {
		if n, err := b.db.Graph().GetNode(ctx, id); err == nil && n != nil && strings.TrimSpace(n.Content) != "" {
			return strings.TrimSpace(n.Content)
		}
		return entityLabelFromID(id)
	}
	seen := map[string]bool{}
	var facts []KnowledgeMemoryGraphFact
	for _, e := range src.relations {
		doc, _ := e.Properties["document_id"].(string)
		if _, gone := src.hidden[doc]; gone {
			continue
		}
		key := e.FromNodeID + "|" + e.EdgeType + "|" + e.ToNodeID
		if seen[key] {
			continue
		}
		seen[key] = true
		from, to := eventTimeOf(e.Properties)
		facts = append(facts, KnowledgeMemoryGraphFact{
			Subject: label(e.FromNodeID), Predicate: e.EdgeType, Object: label(e.ToNodeID),
			SubjectID: e.FromNodeID, ObjectID: e.ToNodeID,
			ValidFrom: formatEventTime(from), ValidTo: formatEventTime(to),
		})
	}
	sort.SliceStable(facts, func(i, j int) bool {
		if facts[i].Predicate != facts[j].Predicate {
			return facts[i].Predicate < facts[j].Predicate
		}
		return facts[i].Object < facts[j].Object
	})
	facts = inTimeOrder(facts)
	if len(facts) > maxProfileFacts {
		facts = facts[:maxProfileFacts]
	}
	return facts, nil
}

// recallProfiles are the profiles of the entities a query names, at most
// maxRecallProfiles. An entity the caller names gets one however few
// documents mention it; one found in the query's words needs
// minProfileDocuments. With a reflector, which costs a model call, one stale
// profile is rebuilt per recall and the others are served marked stale.
func (b *KnowledgeMemory) recallProfiles(ctx context.Context, query string, entityNames []string) ([]EntityProfile, error) {
	named := map[string]bool{}
	for _, n := range entityNames {
		if id := resolveEntityNodeID("", n); id != "" {
			named[id] = true
		}
	}
	ids, err := b.db.queryEntityNodeIDs(ctx, query, entityNames)
	if err != nil {
		return nil, err
	}
	var out []EntityProfile
	rebuilds := 0
	for _, id := range ids {
		if len(out) == maxRecallProfiles {
			break
		}
		rebuild := b.db.KnowledgeMemoryReflector == nil || rebuilds == 0
		before := 0
		if stored, _, err := b.db.loadEntityProfile(ctx, id); err == nil && stored != nil {
			before = stored.Version
		}
		p, err := b.EntityProfile(ctx, id, named[id], rebuild)
		if err != nil {
			return nil, err
		}
		if p == nil {
			continue
		}
		if p.Version != before {
			rebuilds++
		}
		out = append(out, *p)
	}
	return out, nil
}

// profilesServed reports whether a caller may be given profiles: they are
// built from the shared graph, whose edges carry no collection, so not
// under a scope that confines knowledge.
func profilesServed(ctx context.Context) bool {
	scope, ok := callerScopeOf(ctx)
	return !ok || scope.Knowledge == nil
}

// renderProfiles is the pack's profile section and the chunks it cites.
func renderProfiles(profiles []EntityProfile) (string, []string) {
	var b strings.Builder
	var ids []string
	for i, p := range profiles {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimSpace(p.Content))
		if p.Stale {
			b.WriteString("\n(built before its latest sources)")
		}
		ids = append(ids, p.ChunkIDs...)
	}
	return b.String(), ids
}
