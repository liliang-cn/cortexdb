package cortexdb

// The history of one entity and its relations, read the way `git log` reads a
// file, with the `git blame` answer attached: which version is in effect, and
// why.
//
// The past was already stored. Every closed node and edge version sits in
// graph_node_history / graph_edge_history with when it closed, why, what
// replaced it and who did it — and none of it was reachable from outside Go,
// and none of it was joined up. "Leo lives_in Beijing" followed by "Leo
// lives_in Chengdu" is two edge ids; a reader holding either id could not see
// the other, and nothing said that one of them is the answer today and why.
//
// # One line per subject and predicate
//
// A version belongs to the line (subject, predicate), whatever its edge id and
// whatever object it points at, so a lineage that spans several ids reads as
// one history, oldest first. The entity's own edges are read in both
// directions: lines it is the subject of ("out") and lines it is the object of
// ("in").
//
// # Bounded
//
// Pages are over lines, keyed on (direction, subject, predicate) with the next
// key as the cursor, and each line loads at most MaxVersions versions — the
// newest, when there are more, with the cut reported. Nothing loads a whole
// history, the same discipline GraphDiff keeps.
//
// # Why this version is in effect
//
// Computed from stored rows, never by a model, and never from the schema as it
// is now: a closure the ontology decided carries the schema id and version it
// was decided under (graph.Invalidation), and the explanation cites that, so a
// later schema change cannot rewrite why an old fact ended. Where several
// values are live for a link, the answer says so and says what the schema
// thinks of it, rather than naming a winner the data does not support.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Statuses a version can have. Reasons the graph stores beyond these
// (document_deleted, orphaned, pruned, or a caller's own) are passed through.
const (
	HistoryStatusCurrent    = "current"
	HistoryStatusSuperseded = "superseded"
	HistoryStatusRetracted  = "retracted"
	HistoryStatusMerged     = "merged"
	// HistoryStatusEnded is a version that is over for a reason the store never
	// recorded: a row archived before reasons were kept, or a fact ended by its
	// own valid_to property.
	HistoryStatusEnded = "ended"
)

// Kinds of in_effect explanation.
const (
	InEffectOnlyVersion     = "only_version"
	InEffectReplaced        = "replaced"
	InEffectRevised         = "revised"
	InEffectMergeSurvivor   = "merge_survivor"
	InEffectSeveralInEffect = "several_in_effect"
)

const (
	defaultHistoryLineLimit  = 50
	maxHistoryLineLimit      = 500
	defaultHistoryMaxVersion = 100
	maxHistoryMaxVersions    = 1000
)

// Rules an in_effect explanation can cite.
const (
	InEffectRuleSingleValued     = "single_valued_link"
	InEffectRuleExplicit         = "explicit_supersede"
	InEffectRuleCorrection       = "correction"
	InEffectRuleDocumentReingest = "document_reingest"
	InEffectRuleMerge            = "merge"
	InEffectRuleRetraction       = "retraction"
	InEffectRuleSucceeded        = "succeeded"
)

// Schema opinions on a link that has several live values.
const (
	SchemaOpinionNone                 = "no_opinion"
	SchemaOpinionMultiValued          = "multi_valued"
	SchemaOpinionSingleValuedUnforced = "single_valued_unenforced"
)

// GraphHistoryRequest asks for the versions of one entity and its relations.
type GraphHistoryRequest struct {
	// Entity is a node id or a name. A name resolves by exact or normalised
	// match only, never by containment; one that matches several nodes is an
	// error naming them, not a guess.
	Entity string
	// Relation limits the lines to one edge type.
	Relation string
	// From and To bound the versions to those whose valid interval overlaps
	// [From, To). Either may be zero.
	From, To time.Time
	// AsOf asks which versions were in effect at that instant, and why. Zero
	// means now. It does not hide later versions: the log is still complete.
	AsOf time.Time
	// Limit is the number of lines per page (default 50, at most 500).
	Limit int
	// MaxVersions caps the versions loaded per line (default 100, at most
	// 1000); the newest are kept.
	MaxVersions int
	// Cursor continues a previous page.
	Cursor string
}

// GraphHistoryResult is the timeline of one entity.
type GraphHistoryResult struct {
	Entity string     `json:"entity"`
	Name   string     `json:"name,omitempty"`
	AsOf   *time.Time `json:"as_of,omitempty"`
	// NodeVersions are the entity's own versions, oldest first. First page only.
	NodeVersions          []HistoryNodeVersion `json:"node_versions,omitempty"`
	NodeVersionsTruncated bool                 `json:"node_versions_truncated,omitempty"`
	// MergedFrom lists the entities that were folded into this one.
	MergedFrom []HistoryMerge `json:"merged_from,omitempty"`
	Lines      []HistoryLine  `json:"lines"`
	NextCursor string         `json:"next_cursor,omitempty"`
	Truncated  bool           `json:"truncated,omitempty"`
}

// HistoryMerge is one entity folded into the one being read.
type HistoryMerge struct {
	ID   string    `json:"id"`
	Name string    `json:"name,omitempty"`
	At   time.Time `json:"at,omitzero"`
}

// HistoryNodeVersion is one version of the entity's own record.
type HistoryNodeVersion struct {
	Content      string    `json:"content,omitempty"`
	NodeType     string    `json:"node_type,omitempty"`
	ValidFrom    time.Time `json:"valid_from,omitzero"`
	ValidTo      time.Time `json:"valid_to,omitzero"`
	RecordedAt   time.Time `json:"recorded_at,omitzero"`
	RetractedAt  time.Time `json:"retracted_at,omitzero"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason,omitempty"`
	SupersededBy string    `json:"superseded_by,omitempty"`
	Producer     string    `json:"producer,omitempty"`
	InEffect     *InEffect `json:"in_effect,omitempty"`
	live         bool
	end          time.Time
}

// HistoryLine is every version of one subject + predicate.
type HistoryLine struct {
	Subject     string `json:"subject"`
	SubjectName string `json:"subject_name,omitempty"`
	Predicate   string `json:"predicate"`
	// Direction is "out" when the entity is the subject and "in" when it is
	// the object.
	Direction string `json:"direction"`
	// Versions are oldest first.
	Versions          []HistoryVersion `json:"versions"`
	VersionsTruncated bool             `json:"versions_truncated,omitempty"`
}

// HistoryVersion is one version of one edge.
type HistoryVersion struct {
	ID            string             `json:"id"`
	Subject       string             `json:"subject"`
	Object        string             `json:"object"`
	ObjectName    string             `json:"object_name,omitempty"`
	ValidFrom     time.Time          `json:"valid_from,omitzero"`
	ValidTo       time.Time          `json:"valid_to,omitzero"`
	RecordedAt    time.Time          `json:"recorded_at,omitzero"`
	RetractedAt   time.Time          `json:"retracted_at,omitzero"`
	Status        string             `json:"status"`
	Reason        string             `json:"reason,omitempty"`
	SupersededBy  string             `json:"superseded_by,omitempty"`
	Producer      string             `json:"producer,omitempty"`
	SchemaID      string             `json:"schema_id,omitempty"`
	SchemaVersion int                `json:"schema_version,omitempty"`
	Provenance    *VersionProvenance `json:"provenance,omitempty"`
	// InEffect is set on a version that is in effect (now, or at AsOf).
	InEffect *InEffect `json:"in_effect,omitempty"`

	live  bool
	props map[string]any
	end   time.Time // effective end: valid_to, the valid_to property or retracted_at
}

// VersionProvenance is where a version came from, as fact_provenance reads it.
type VersionProvenance struct {
	DocumentID string   `json:"document_id,omitempty"`
	ChunkIDs   []string `json:"chunk_ids,omitempty"`
	Inferred   bool     `json:"inferred,omitempty"`
	Rule       string   `json:"rule,omitempty"`
	Source     string   `json:"source,omitempty"`
}

// InEffect says why a version is the one in effect.
type InEffect struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// Rule is what decided it, for a version that replaced another.
	Rule string `json:"rule,omitempty"`
	// Replaced is the version this one took over from.
	Replaced *ReplacedVersion `json:"replaced,omitempty"`
	// SchemaID and SchemaVersion are the schema recorded when the rule was a
	// schema's: the one in force when the decision was made, not at read time.
	SchemaID      string `json:"schema_id,omitempty"`
	SchemaVersion int    `json:"schema_version,omitempty"`
	Producer      string `json:"producer,omitempty"`
	// Revisions counts in-place rewrites of this same edge before this version.
	Revisions int `json:"revisions,omitempty"`
	// MergedFrom names the entity folded into the one this edge now belongs to.
	MergedFrom string `json:"merged_from,omitempty"`
	// Alongside lists the other values in effect, for several_in_effect.
	Alongside []string `json:"alongside,omitempty"`
	// SchemaOpinion is what the active schema says about a link with several
	// live values: no_opinion, multi_valued or single_valued_unenforced.
	SchemaOpinion string `json:"schema_opinion,omitempty"`
	// AlsoAssertedBy lists other edge ids stating the same fact (other
	// documents), which corroborate it and change nothing about what holds.
	AlsoAssertedBy []string `json:"also_asserted_by,omitempty"`
}

// ReplacedVersion is the version a current one took over from.
type ReplacedVersion struct {
	ID         string    `json:"id"`
	Object     string    `json:"object"`
	ObjectName string    `json:"object_name,omitempty"`
	EndedAt    time.Time `json:"ended_at,omitzero"`
	Reason     string    `json:"reason,omitempty"`
}

// GraphHistory returns the timeline of an entity and its relations. See the
// file header for the model.
func (db *DB) GraphHistory(ctx context.Context, req GraphHistoryRequest) (*GraphHistoryResult, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: graph history: nil db")
	}
	if strings.TrimSpace(req.Entity) == "" {
		return nil, fmt.Errorf("cortexdb: graph history: an entity (node id or name) is required")
	}
	if !req.From.IsZero() && !req.To.IsZero() && !req.To.After(req.From) {
		return nil, fmt.Errorf("cortexdb: graph history: the window is empty: to must be after from")
	}
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("cortexdb: graph history: %w", err)
	}
	limit := clampInt(req.Limit, defaultHistoryLineLimit, maxHistoryLineLimit)
	maxVersions := clampInt(req.MaxVersions, defaultHistoryMaxVersion, maxHistoryMaxVersions)
	cur, err := decodeHistoryCursor(req.Cursor)
	if err != nil {
		return nil, err
	}

	id, err := db.resolveHistoryEntity(ctx, req.Entity)
	if err != nil {
		return nil, err
	}
	h := &historyReader{db: db, ctx: ctx, entity: id, relation: strings.TrimSpace(req.Relation),
		from: stampInstant(req.From), to: stampInstant(req.To), asOf: stampInstant(req.AsOf), maxVersions: maxVersions}
	out := &GraphHistoryResult{Entity: id, Lines: []HistoryLine{}}
	if !h.asOf.IsZero() {
		at := h.asOf
		out.AsOf = &at
	}

	if cur == nil {
		if err := h.nodeVersions(out); err != nil {
			return nil, err
		}
	}

	keys, more, err := h.lineKeys(cur, limit)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		line, err := h.loadLine(k)
		if err != nil {
			return nil, err
		}
		if len(line.Versions) > 0 {
			out.Lines = append(out.Lines, *line)
		}
	}
	if more && len(keys) > 0 {
		out.Truncated = true
		out.NextCursor = encodeHistoryCursor(keys[len(keys)-1])
	}
	if err := h.annotate(out); err != nil {
		return nil, err
	}
	return out, nil
}

func clampInt(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func stampInstant(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Truncate(time.Microsecond)
}

// --- resolving the entity ------------------------------------------------------

// resolveHistoryEntity turns a node id or a name into one node id.
//
// The resolver is the one verify_claims uses — exact or normalised match,
// following merge aliases — so a name means the same thing in every tool. A
// retracted entity has no live node, so an id with history of its own is also
// accepted: asking what happened to something that was deleted is the point.
func (db *DB) resolveHistoryEntity(ctx context.Context, entity string) (string, error) {
	entity = strings.TrimSpace(entity)
	if id := db.existingNodeID(ctx, entity); id != "" {
		return id, nil
	}
	resolved, err := db.resolveClaimEndpoints(ctx, []Claim{{Subject: entity}})
	if err != nil {
		return "", fmt.Errorf("cortexdb: graph history: %w", err)
	}
	switch ids := resolved[entity]; len(ids) {
	case 1:
		return ids[0], nil
	case 0:
	default:
		return "", fmt.Errorf("cortexdb: graph history: %q matches %d entities (%s); pass the node id of the one you mean",
			entity, len(ids), strings.Join(ids, ", "))
	}
	for _, candidate := range []string{entity, EntityNodeID(entity)} {
		if candidate == "" {
			continue
		}
		var one int
		for _, q := range []string{
			`SELECT 1 FROM graph_node_history WHERE id = ? LIMIT 1`,
			`SELECT 1 FROM graph_edge_history WHERE from_node_id = ? OR to_node_id = ? LIMIT 1`,
		} {
			args := []any{candidate}
			if strings.Contains(q, "OR to_node_id") {
				args = append(args, candidate)
			}
			err := db.queryRow(ctx, q, args...).Scan(&one)
			if err == nil {
				return candidate, nil
			}
			if err != sql.ErrNoRows {
				return "", fmt.Errorf("cortexdb: graph history: %w", err)
			}
		}
	}
	return "", fmt.Errorf("cortexdb: graph history: no entity %q, live or in history", entity)
}

// --- cursor ------------------------------------------------------------------

type lineKey struct {
	Dir       string `json:"d"` // "out" or "in"
	Subject   string `json:"s"`
	Predicate string `json:"p"`
}

func encodeHistoryCursor(k lineKey) string {
	b, _ := json.Marshal(k)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeHistoryCursor(c string) (*lineKey, error) {
	c = strings.TrimSpace(c)
	if c == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: graph history: bad cursor")
	}
	var k lineKey
	if err := json.Unmarshal(raw, &k); err != nil || (k.Dir != "out" && k.Dir != "in") {
		return nil, fmt.Errorf("cortexdb: graph history: bad cursor")
	}
	return &k, nil
}

// --- reading -----------------------------------------------------------------

type historyReader struct {
	db          *DB
	ctx         context.Context
	entity      string
	relation    string
	from, to    time.Time
	asOf        time.Time
	maxVersions int
	types       map[string]string
}

// windowSQL is the valid-time overlap predicate with its arguments. It is an
// optimisation: overlap is re-checked in Go against the effective interval,
// which also knows a fact's own valid_to property.
func (h *historyReader) windowSQL() (string, []any) {
	var sb strings.Builder
	var args []any
	if !h.to.IsZero() {
		sb.WriteString(` AND (valid_from IS NULL OR valid_from < ?)`)
		args = append(args, h.to)
	}
	if !h.from.IsZero() {
		sb.WriteString(` AND (valid_to IS NULL OR valid_to > ?)`)
		args = append(args, h.from)
	}
	return sb.String(), args
}

// lineKeys returns up to limit line keys after the cursor, outbound lines first,
// and whether there are more.
func (h *historyReader) lineKeys(cur *lineKey, limit int) ([]lineKey, bool, error) {
	var keys []lineKey
	for _, dir := range []string{"out", "in"} {
		if cur != nil && cur.Dir == "in" && dir == "out" {
			continue
		}
		fixed := "from_node_id"
		if dir == "in" {
			fixed = "to_node_id"
		}
		win, winArgs := h.windowSQL()
		where := fixed + ` = ?`
		args := []any{h.entity}
		if h.relation != "" {
			where += ` AND etype = ?`
			args = append(args, h.relation)
		} else {
			holes, a := sqlPlaceholders(bookkeepingEdgeTypes)
			where += ` AND etype NOT IN (` + holes + `)`
			args = append(args, a...)
		}
		where += win
		args = append(args, winArgs...)
		if cur != nil && cur.Dir == dir {
			where += ` AND (from_node_id > ? OR (from_node_id = ? AND etype > ?))`
			args = append(args, cur.Subject, cur.Subject, cur.Predicate)
		}
		args = append(args, limit+1-len(keys))
		rows, err := h.db.query(h.ctx, `
			SELECT from_node_id, etype FROM (
				SELECT from_node_id, to_node_id, COALESCE(edge_type, '') AS etype, valid_from, valid_to FROM graph_edges
				UNION ALL
				SELECT from_node_id, to_node_id, COALESCE(edge_type, '') AS etype, valid_from, valid_to FROM graph_edge_history
			) e WHERE `+where+`
			GROUP BY from_node_id, etype
			ORDER BY from_node_id, etype
			LIMIT ?`, args...)
		if err != nil {
			return nil, false, fmt.Errorf("cortexdb: graph history: lines: %w", err)
		}
		for rows.Next() {
			var k lineKey
			k.Dir = dir
			if err := rows.Scan(&k.Subject, &k.Predicate); err != nil {
				_ = rows.Close()
				return nil, false, fmt.Errorf("cortexdb: graph history: lines: %w", err)
			}
			keys = append(keys, k)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		_ = rows.Close()
		if len(keys) > limit {
			return keys[:limit], true, nil
		}
	}
	return keys, false, nil
}

const historyEdgeSelect = `id, from_node_id, to_node_id, COALESCE(edge_type, ''), COALESCE(properties, ''),
	valid_from, valid_to, recorded_at, retracted_at`

// loadLine reads the versions of one line from the live and history tables,
// at most maxVersions of them, newest kept.
func (h *historyReader) loadLine(k lineKey) (*HistoryLine, error) {
	line := &HistoryLine{Subject: k.Subject, Predicate: k.Predicate, Direction: k.Dir}
	win, winArgs := h.windowSQL()
	base := `from_node_id = ? AND COALESCE(edge_type, '') = ?`
	baseArgs := []any{k.Subject, k.Predicate}
	if k.Dir == "in" {
		base += ` AND to_node_id = ?`
		baseArgs = append(baseArgs, h.entity)
	}
	args := append(append([]any{}, baseArgs...), winArgs...)
	args = append(args, h.maxVersions+1)

	var versions []HistoryVersion
	for _, t := range []struct {
		table string
		extra string
		live  bool
	}{
		{"graph_edges", "", true},
		{"graph_edge_history", `, COALESCE(reason, ''), COALESCE(superseded_by, ''), COALESCE(producer, ''), COALESCE(schema_id, ''), COALESCE(schema_version, 0)`, false},
	} {
		rows, err := h.db.query(h.ctx, `SELECT `+historyEdgeSelect+t.extra+` FROM `+t.table+
			` WHERE `+base+win+` ORDER BY recorded_at DESC NULLS LAST LIMIT ?`, args...)
		if err != nil {
			return nil, fmt.Errorf("cortexdb: graph history: versions: %w", err)
		}
		got, err := scanHistoryVersions(rows, t.live)
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		versions = append(versions, got...)
	}

	// A merge repoints an edge: its earlier versions name the entity that was
	// folded in, so they are not found by the line's key. Read the history of
	// the ids already found, so the line shows the merge as one of its own.
	if ids := distinctVersionIDs(versions); len(ids) > 0 {
		seen := map[string]bool{}
		for _, v := range versions {
			seen[versionIdentity(v)] = true
		}
		for _, chunk := range chunkStrings(ids, 200) {
			holes, a := sqlPlaceholders(chunk)
			rows, err := h.db.query(h.ctx, `SELECT `+historyEdgeSelect+
				`, COALESCE(reason, ''), COALESCE(superseded_by, ''), COALESCE(producer, ''), COALESCE(schema_id, ''), COALESCE(schema_version, 0)
				FROM graph_edge_history WHERE id IN (`+holes+`)`, a...)
			if err != nil {
				return nil, fmt.Errorf("cortexdb: graph history: versions: %w", err)
			}
			got, err := scanHistoryVersions(rows, false)
			_ = rows.Close()
			if err != nil {
				return nil, err
			}
			for _, v := range got {
				if !seen[versionIdentity(v)] {
					seen[versionIdentity(v)] = true
					versions = append(versions, v)
				}
			}
		}
	}

	sort.SliceStable(versions, func(i, j int) bool { return versionLess(versions[i], versions[j]) })
	if len(versions) > h.maxVersions {
		versions = versions[len(versions)-h.maxVersions:]
		line.VersionsTruncated = true
	}
	versions = foldAnnotatedRewrites(versions)

	for _, v := range versions {
		if h.overlapsWindow(v) {
			line.Versions = append(line.Versions, v)
		}
	}
	return line, nil
}

func scanHistoryVersions(rows *sql.Rows, live bool) ([]HistoryVersion, error) {
	var out []HistoryVersion
	for rows.Next() {
		var v HistoryVersion
		var props string
		var vf, vt, rec, ret sql.NullTime
		dest := []any{&v.ID, &v.Subject, &v.Object, new(string), &props, &vf, &vt, &rec, &ret}
		var edgeType string
		dest[3] = &edgeType
		if !live {
			dest = append(dest, &v.Reason, &v.SupersededBy, &v.Producer, &v.SchemaID, &v.SchemaVersion)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("cortexdb: graph history: versions: %w", err)
		}
		v.ValidFrom, v.ValidTo = utcTime(vf), utcTime(vt)
		v.RecordedAt, v.RetractedAt = utcTime(rec), utcTime(ret)
		v.live = live
		if props != "" {
			_ = json.Unmarshal([]byte(props), &v.props)
		}
		v.end = effectiveEnd(v)
		if v.ValidTo.IsZero() && !v.end.IsZero() && v.RetractedAt.IsZero() {
			v.ValidTo = v.end
		}
		if prov := provenanceOf(v.props); prov != nil {
			v.Provenance = prov
		}
		v.Status = versionStatus(v)
		out = append(out, v)
	}
	return out, rows.Err()
}

func utcTime(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

// effectiveEnd is when a version stopped being in effect: the earliest of its
// valid_to column, its valid_to property (how graphflow ends a fact it keeps
// live) and its retraction.
func effectiveEnd(v HistoryVersion) time.Time {
	var end time.Time
	consider := func(t time.Time) {
		if !t.IsZero() && (end.IsZero() || t.Before(end)) {
			end = t
		}
	}
	consider(v.ValidTo)
	consider(v.RetractedAt)
	if s, ok := v.props["valid_to"].(string); ok {
		if t, ok := parseFactTime(s); ok {
			consider(t.UTC())
		}
	}
	return end
}

func provenanceOf(props map[string]any) *VersionProvenance {
	if len(props) == 0 {
		return nil
	}
	var p FactProvenance
	p.fillFromProperties(props)
	if p.DocumentID == "" && len(p.ChunkIDs) == 0 && !p.Inferred && p.Rule == "" && p.Source == "" {
		return nil
	}
	return &VersionProvenance{DocumentID: p.DocumentID, ChunkIDs: p.ChunkIDs, Inferred: p.Inferred, Rule: p.Rule, Source: p.Source}
}

func versionStatus(v HistoryVersion) string {
	if v.live {
		if !v.end.IsZero() && !v.end.After(time.Now().UTC()) {
			if s, _ := v.props["superseded_by"].(string); s != "" {
				return HistoryStatusSuperseded
			}
			return HistoryStatusEnded
		}
		return HistoryStatusCurrent
	}
	switch v.Reason {
	case graph.ReasonSuperseded:
		return HistoryStatusSuperseded
	case graph.ReasonRetracted:
		return HistoryStatusRetracted
	case graph.ReasonMerged:
		return HistoryStatusMerged
	case "":
		return HistoryStatusEnded
	}
	return v.Reason
}

func versionStart(v HistoryVersion) time.Time {
	if !v.ValidFrom.IsZero() {
		return v.ValidFrom
	}
	return v.RecordedAt
}

func versionLess(a, b HistoryVersion) bool {
	sa, sb := versionStart(a), versionStart(b)
	if !sa.Equal(sb) {
		return sa.Before(sb)
	}
	if !a.RecordedAt.Equal(b.RecordedAt) {
		return a.RecordedAt.Before(b.RecordedAt)
	}
	return a.ID < b.ID
}

func versionIdentity(v HistoryVersion) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d|%d|%t", v.ID, v.Subject, v.Object,
		v.ValidFrom.UnixMicro(), v.RecordedAt.UnixMicro(), v.ValidTo.UnixMicro(), v.live)
}

func distinctVersionIDs(vs []HistoryVersion) []string {
	seen := map[string]bool{}
	var ids []string
	for _, v := range vs {
		if !seen[v.ID] {
			seen[v.ID] = true
			ids = append(ids, v.ID)
		}
	}
	return ids
}

func chunkStrings(in []string, size int) [][]string {
	var out [][]string
	for len(in) > size {
		out = append(out, in[:size])
		in = in[size:]
	}
	if len(in) > 0 {
		out = append(out, in)
	}
	return out
}

// foldAnnotatedRewrites joins the two rows graphflow's supersede leaves for one
// fact: it ends a fact by rewriting the live edge with a valid_to property, so
// the open version moves to history and the live row differs from it only in
// the keys that say it ended. They are one version, not two.
func foldAnnotatedRewrites(vs []HistoryVersion) []HistoryVersion {
	strip := func(p map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range p {
			if k == "valid_to" || k == "superseded_by" {
				continue
			}
			out[k] = v
		}
		return out
	}
	same := func(a, b map[string]any) bool {
		x, _ := json.Marshal(strip(a))
		y, _ := json.Marshal(strip(b))
		return string(x) == string(y)
	}
	var out []HistoryVersion
	for _, v := range vs {
		if v.live && !v.end.IsZero() {
			folded := false
			for i := len(out) - 1; i >= 0; i-- {
				p := out[i]
				if p.ID != v.ID {
					continue
				}
				if !p.live && p.Reason == graph.ReasonSuperseded && p.SupersededBy != p.ID && same(p.props, v.props) {
					p.ValidTo = v.end
					p.end = v.end
					p.Status = versionStatus(p)
					out[i] = p
					folded = true
				}
				break
			}
			if folded {
				continue
			}
		}
		out = append(out, v)
	}
	return out
}

func (h *historyReader) overlapsWindow(v HistoryVersion) bool {
	start := versionStart(v)
	if !h.to.IsZero() && !start.IsZero() && !start.Before(h.to) {
		return false
	}
	if !h.from.IsZero() && !v.end.IsZero() && !v.end.After(h.from) {
		return false
	}
	return true
}

// --- node versions -----------------------------------------------------------

func (h *historyReader) nodeVersions(out *GraphHistoryResult) error {
	var versions []HistoryNodeVersion
	win, winArgs := h.windowSQL()
	for _, t := range []struct {
		table string
		extra string
		live  bool
	}{
		{"graph_nodes", "", true},
		{"graph_node_history", `, COALESCE(reason, ''), COALESCE(superseded_by, ''), COALESCE(producer, '')`, false},
	} {
		args := append([]any{h.entity}, winArgs...)
		args = append(args, h.maxVersions+1)
		rows, err := h.db.query(h.ctx, `SELECT COALESCE(content, ''), COALESCE(node_type, ''),
			valid_from, valid_to, recorded_at, retracted_at`+t.extra+` FROM `+t.table+
			` WHERE id = ?`+win+` ORDER BY recorded_at DESC NULLS LAST LIMIT ?`, args...)
		if err != nil {
			return fmt.Errorf("cortexdb: graph history: node versions: %w", err)
		}
		for rows.Next() {
			var v HistoryNodeVersion
			var vf, vt, rec, ret sql.NullTime
			dest := []any{&v.Content, &v.NodeType, &vf, &vt, &rec, &ret}
			if !t.live {
				dest = append(dest, &v.Reason, &v.SupersededBy, &v.Producer)
			}
			if err := rows.Scan(dest...); err != nil {
				_ = rows.Close()
				return fmt.Errorf("cortexdb: graph history: node versions: %w", err)
			}
			v.ValidFrom, v.ValidTo = utcTime(vf), utcTime(vt)
			v.RecordedAt, v.RetractedAt = utcTime(rec), utcTime(ret)
			v.live = t.live
			v.end = v.ValidTo
			if v.end.IsZero() {
				v.end = v.RetractedAt
			}
			switch {
			case t.live:
				v.Status = HistoryStatusCurrent
			case v.Reason == graph.ReasonSuperseded:
				v.Status = HistoryStatusSuperseded
			case v.Reason == graph.ReasonRetracted:
				v.Status = HistoryStatusRetracted
			case v.Reason == graph.ReasonMerged:
				v.Status = HistoryStatusMerged
			case v.Reason == "":
				v.Status = HistoryStatusEnded
			default:
				v.Status = v.Reason
			}
			versions = append(versions, v)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	sort.SliceStable(versions, func(i, j int) bool {
		a, b := versions[i], versions[j]
		sa, sb := a.ValidFrom, b.ValidFrom
		if sa.IsZero() {
			sa = a.RecordedAt
		}
		if sb.IsZero() {
			sb = b.RecordedAt
		}
		if !sa.Equal(sb) {
			return sa.Before(sb)
		}
		return a.RecordedAt.Before(b.RecordedAt)
	})
	if len(versions) > h.maxVersions {
		versions = versions[len(versions)-h.maxVersions:]
		out.NodeVersionsTruncated = true
	}

	// Entities folded into this one.
	rows, err := h.db.query(h.ctx, `SELECT id, COALESCE(content, ''), retracted_at FROM graph_node_history
		WHERE superseded_by = ? AND reason = ? ORDER BY retracted_at`, h.entity, graph.ReasonMerged)
	if err != nil {
		return fmt.Errorf("cortexdb: graph history: merges: %w", err)
	}
	for rows.Next() {
		var m HistoryMerge
		var at sql.NullTime
		if err := rows.Scan(&m.ID, &m.Name, &at); err != nil {
			_ = rows.Close()
			return fmt.Errorf("cortexdb: graph history: merges: %w", err)
		}
		m.At = utcTime(at)
		out.MergedFrom = append(out.MergedFrom, m)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}

	for i := range versions {
		v := &versions[i]
		if h.nodeInEffect(*v) {
			v.InEffect = nodeInEffect(versions[:i], out.MergedFrom)
		}
	}
	out.NodeVersions = versions
	for _, v := range versions {
		if v.Content != "" && v.live {
			out.Name = v.Content
		}
	}
	if out.Name == "" && len(versions) > 0 {
		out.Name = versions[len(versions)-1].Content
	}
	return nil
}

func (h *historyReader) nodeInEffect(v HistoryNodeVersion) bool {
	if h.asOf.IsZero() {
		return v.live
	}
	start := v.ValidFrom
	if start.IsZero() {
		start = v.RecordedAt
	}
	if !start.IsZero() && start.After(h.asOf) {
		return false
	}
	if !v.end.IsZero() && !v.end.After(h.asOf) {
		return false
	}
	if !v.RecordedAt.IsZero() && v.RecordedAt.After(h.asOf) {
		return false
	}
	return true
}

func nodeInEffect(earlier []HistoryNodeVersion, merges []HistoryMerge) *InEffect {
	if len(merges) > 0 {
		names := make([]string, 0, len(merges))
		for _, m := range merges {
			names = append(names, firstNonEmpty(m.Name, m.ID))
		}
		return &InEffect{Kind: InEffectMergeSurvivor, Rule: InEffectRuleMerge, MergedFrom: strings.Join(names, ", "),
			Summary: fmt.Sprintf("Survivor of a merge: merged from %s.", strings.Join(names, ", "))}
	}
	rewrites := 0
	for _, e := range earlier {
		if e.Reason == graph.ReasonSuperseded {
			rewrites++
		}
	}
	if rewrites > 0 {
		return &InEffect{Kind: InEffectRevised, Revisions: rewrites,
			Summary: fmt.Sprintf("This record was rewritten %d time(s); this is the latest version.", rewrites)}
	}
	return &InEffect{Kind: InEffectOnlyVersion, Summary: "The only version: nothing else has been asserted for this record."}
}

// --- explaining -----------------------------------------------------------------

// annotate fills object names and the in_effect explanation on every line.
func (h *historyReader) annotate(out *GraphHistoryResult) error {
	ids := map[string]bool{out.Entity: true}
	for _, l := range out.Lines {
		ids[l.Subject] = true
		for _, v := range l.Versions {
			ids[v.Subject], ids[v.Object] = true, true
		}
	}
	names, types, err := h.db.historyNodeInfo(h.ctx, ids)
	if err != nil {
		return err
	}
	h.types = types
	if out.Name == "" {
		out.Name = names[out.Entity]
	}
	for i := range out.MergedFrom {
		if out.MergedFrom[i].Name == "" {
			out.MergedFrom[i].Name = names[out.MergedFrom[i].ID]
		}
	}

	schemaID, schemaVersion, haveSchema := h.db.ActiveOntologyRef(h.ctx)
	var schema *OntologySchema
	if haveSchema {
		schema, _ = h.db.loadActiveOntologySchema(h.ctx)
	}
	for li := range out.Lines {
		line := &out.Lines[li]
		line.SubjectName = names[line.Subject]
		for vi := range line.Versions {
			v := &line.Versions[vi]
			v.ObjectName = names[v.Object]
		}
		h.explainLine(line, names, schema, schemaID, schemaVersion)
	}
	return nil
}

func (h *historyReader) versionInEffect(v HistoryVersion) bool {
	if h.asOf.IsZero() {
		return v.live && v.Status == HistoryStatusCurrent
	}
	if s := versionStart(v); !s.IsZero() && s.After(h.asOf) {
		return false
	}
	if !v.end.IsZero() && !v.end.After(h.asOf) {
		return false
	}
	if !v.RecordedAt.IsZero() && v.RecordedAt.After(h.asOf) {
		return false
	}
	return true
}

func (h *historyReader) explainLine(line *HistoryLine, names map[string]string, schema *OntologySchema, schemaID string, schemaVersion int) {
	vs := line.Versions
	// The values in effect, by object: two edges naming the same object are one
	// value stated twice.
	inEffect := map[string][]int{}
	var objects []string
	for i, v := range vs {
		if !h.versionInEffect(v) {
			continue
		}
		if _, ok := inEffect[v.Object]; !ok {
			objects = append(objects, v.Object)
		}
		inEffect[v.Object] = append(inEffect[v.Object], i)
	}
	label := func(id string) string { return firstNonEmpty(names[id], id) }
	subject := firstNonEmpty(line.SubjectName, line.Subject)
	subjectType := h.types[line.Subject]

	for _, obj := range objects {
		idxs := inEffect[obj]
		// The version that carries the explanation is the latest one stating
		// this value; the rest are the same fact from another source.
		lead := idxs[len(idxs)-1]
		v := &vs[lead]
		var also []string
		for _, i := range idxs {
			if i != lead && vs[i].ID != v.ID {
				also = append(also, vs[i].ID)
			}
		}
		var ie *InEffect
		if len(objects) > 1 {
			var others []string
			for _, o := range objects {
				if o != obj {
					others = append(others, label(o))
				}
			}
			ie = severalInEffect(line, subject, subjectType, label(obj), others, schema, schemaID, schemaVersion)
		} else {
			ie = h.explainSingle(vs, lead, subject, line.Predicate, label)
		}
		ie.AlsoAssertedBy = also
		v.InEffect = ie
	}
}

func severalInEffect(line *HistoryLine, subject, subjectType, object string, others []string, schema *OntologySchema, schemaID string, schemaVersion int) *InEffect {
	ie := &InEffect{Kind: InEffectSeveralInEffect, Alongside: others, SchemaOpinion: SchemaOpinionNone}
	opinion := fmt.Sprintf("the active ontology does not describe %q, so it has no opinion on whether the values exclude each other", line.Predicate)
	if schema == nil {
		opinion = fmt.Sprintf("there is no active ontology, so nothing says whether the values of %q exclude each other", line.Predicate)
	} else {
		ie.SchemaID, ie.SchemaVersion = schemaID, schemaVersion
		single, known := schemaSubjectSingleValued(schema, line.Predicate, subjectType)
		if !known {
			single, known = schemaSubjectSingleValued(schema, normalizeOntologyLabel(line.Predicate), subjectType)
		}
		switch {
		case known && !single:
			ie.SchemaOpinion = SchemaOpinionMultiValued
			opinion = fmt.Sprintf("the active schema %s v%d declares %q multi-valued, so these values hold together",
				schemaID, schemaVersion, line.Predicate)
		case known && single:
			ie.SchemaOpinion = SchemaOpinionSingleValuedUnforced
			opinion = fmt.Sprintf("the active schema %s v%d declares %q single-valued, yet more than one value is live: they were written before the rule, or by a path that does not enforce it; the store does not pick between them",
				schemaID, schemaVersion, line.Predicate)
		default:
			ie.SchemaID, ie.SchemaVersion = "", 0
		}
	}
	ie.Summary = fmt.Sprintf("%s %s %s is one of %d values in effect (also: %s); %s.",
		subject, line.Predicate, object, len(others)+1, strings.Join(others, ", "), opinion)
	return ie
}

// explainSingle explains the one value in effect on a line: what it took over
// from, and by which rule.
func (h *historyReader) explainSingle(vs []HistoryVersion, idx int, subject, predicate string, label func(string) string) *InEffect {
	v := vs[idx]
	// The edge's first version on this line is where it began; what it took
	// over from is looked for before that.
	first := idx
	revisions, lastRevision := 0, time.Time{}
	var mergedFromID string
	for i := 0; i < idx; i++ {
		p := vs[i]
		if p.ID != v.ID {
			continue
		}
		if i < first {
			first = i
		}
		switch {
		case p.Reason == graph.ReasonMerged:
			if p.Subject != v.Subject {
				mergedFromID = p.Subject
			} else if p.Object != v.Object {
				mergedFromID = p.Object
			}
		case p.Reason == graph.ReasonSuperseded && p.SupersededBy == p.ID:
			revisions++
			if p.end.After(lastRevision) {
				lastRevision = p.end
			}
		}
	}
	origin := vs[first]

	if mergedFromID != "" {
		return &InEffect{Kind: InEffectMergeSurvivor, Rule: InEffectRuleMerge, MergedFrom: label(mergedFromID),
			Revisions: revisions, Producer: producerOf(vs, v.ID, graph.ReasonMerged),
			Summary: fmt.Sprintf("Survivor of a merge: %s was merged into %s, and this edge was repointed.",
				label(mergedFromID), label(entityOfMerge(v, mergedFromID)))}
	}

	if p := predecessorOf(vs, origin, h.asOf); p != nil {
		ie := &InEffect{Kind: InEffectReplaced, Revisions: revisions, Producer: p.Producer,
			Replaced: &ReplacedVersion{ID: p.ID, Object: p.Object, ObjectName: label(p.Object), EndedAt: p.end, Reason: p.Reason}}
		at := p.end.UTC().Format(time.RFC3339)
		switch {
		case p.Reason == graph.ReasonSuperseded && p.SchemaID != "":
			ie.Rule, ie.SchemaID, ie.SchemaVersion = InEffectRuleSingleValued, p.SchemaID, p.SchemaVersion
			ie.Summary = fmt.Sprintf("Replaced %s at %s: %s is single-valued in schema %s v%d, so the new value closed the old one (producer %s).",
				label(p.Object), at, predicate, p.SchemaID, p.SchemaVersion, firstNonEmpty(p.Producer, "unknown"))
		case p.Reason == graph.ReasonSuperseded && p.SupersededBy == v.ID:
			ie.Rule = InEffectRuleExplicit
			ie.Summary = fmt.Sprintf("Replaced %s at %s: explicitly superseded by producer %s.",
				label(p.Object), at, firstNonEmpty(p.Producer, "unknown"))
		case p.Reason == graph.ReasonRetracted:
			ie.Rule = InEffectRuleRetraction
			ie.Summary = fmt.Sprintf("Took over after %s was retracted at %s.", label(p.Object), at)
		default:
			ie.Rule = InEffectRuleSucceeded
			ie.Summary = fmt.Sprintf("Took over after %s ended at %s (%s).", label(p.Object), at,
				firstNonEmpty(p.Reason, "no reason recorded"))
		}
		if revisions > 0 {
			ie.Summary += fmt.Sprintf(" Rewritten in place %d time(s) since.", revisions)
		}
		return ie
	}

	if revisions > 0 {
		rule := InEffectRuleCorrection
		how := "corrected in place"
		if v.Provenance != nil && v.Provenance.DocumentID != "" {
			rule = InEffectRuleDocumentReingest
			how = fmt.Sprintf("re-ingested from document %s", v.Provenance.DocumentID)
		}
		return &InEffect{Kind: InEffectRevised, Rule: rule, Revisions: revisions,
			Summary: fmt.Sprintf("%s %s %s was %s %d time(s); this is the latest version, as of %s.",
				subject, predicate, label(v.Object), how, revisions, lastRevision.UTC().Format(time.RFC3339))}
	}
	return &InEffect{Kind: InEffectOnlyVersion,
		Summary: fmt.Sprintf("The only version: nothing else has been asserted for %s %s.", subject, predicate)}
}

// entityOfMerge is the end of the edge that stayed: the one that is not the
// folded-in entity.
func entityOfMerge(v HistoryVersion, folded string) string {
	if v.Subject == folded {
		return v.Object
	}
	return v.Subject
}

func producerOf(vs []HistoryVersion, id, reason string) string {
	for _, p := range vs {
		if p.ID == id && p.Reason == reason {
			return p.Producer
		}
	}
	return ""
}

// predecessorOf finds the version the origin took over from: the one that
// names it as its successor, else the latest version of a different value that
// had ended by the time the origin began.
func predecessorOf(vs []HistoryVersion, origin HistoryVersion, asOf time.Time) *HistoryVersion {
	var best *HistoryVersion
	explicit := false
	for i := range vs {
		p := &vs[i]
		if p.ID == origin.ID || p.end.IsZero() {
			continue
		}
		if !asOf.IsZero() && p.end.After(asOf) {
			continue
		}
		linked := p.Reason == graph.ReasonSuperseded && p.SupersededBy == origin.ID
		if !linked {
			if p.Object == origin.Object || p.end.After(versionStart(origin)) {
				continue
			}
		}
		switch {
		case best == nil,
			linked && !explicit,
			linked == explicit && p.end.After(best.end):
			best, explicit = p, linked
		}
	}
	return best
}

// historyNodeNames reads display names for node ids, from the live table and
// then from history for the ones that are gone.
func (db *DB) historyNodeInfo(ctx context.Context, ids map[string]bool) (names, types map[string]string, err error) {
	names, types = map[string]string{}, map[string]string{}
	var want []string
	for id := range ids {
		if id != "" {
			want = append(want, id)
		}
	}
	sort.Strings(want)
	for _, table := range []string{"graph_nodes", "graph_node_history"} {
		var missing []string
		for _, id := range want {
			if names[id] == "" {
				missing = append(missing, id)
			}
		}
		for _, chunk := range chunkStrings(missing, 200) {
			holes, args := sqlPlaceholders(chunk)
			order := ""
			if table == "graph_node_history" {
				order = " ORDER BY recorded_at"
			}
			rows, err := db.query(ctx, `SELECT id, COALESCE(content, ''), COALESCE(node_type, '') FROM `+table+` WHERE id IN (`+holes+`)`+order, args...)
			if err != nil {
				return nil, nil, fmt.Errorf("cortexdb: graph history: names: %w", err)
			}
			for rows.Next() {
				var id, name, typ string
				if err := rows.Scan(&id, &name, &typ); err != nil {
					_ = rows.Close()
					return nil, nil, fmt.Errorf("cortexdb: graph history: names: %w", err)
				}
				if name != "" {
					names[id] = name
				}
				if typ != "" {
					types[id] = typ
				}
			}
			err = rows.Err()
			_ = rows.Close()
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return names, types, nil
}
