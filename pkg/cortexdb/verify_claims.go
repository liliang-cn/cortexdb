package cortexdb

// Checking a claim against the graph, with no model in the loop.
//
// A generator that answers from a knowledge graph produces sentences of the
// shape "Leo lives in Chengdu". Before one of them reaches a person the useful
// question is not "is this plausible" — a model is good at plausible — but
// "does the graph say so, say otherwise, or say nothing". Those are three
// different answers and they call for three different actions: cite it, fix
// it, or say it is unsupported. Collapsing the last two into "not found" is the
// usual mistake, and it is the costly one: a claim the graph contradicts reads
// as merely uncited.
//
// Everything here is a read of what is stored, using notions the store already
// has rather than new ones:
//
//   - "Current" is the valid-time interval graphflow writes into an edge's
//     properties (valid_from / valid_to). An edge with no interval is current.
//   - "Only one value can hold" is LinkSingleValued — the ontology's
//     cardinality — plus whatever the caller declares for this request. Where
//     neither says so nothing is assumed: a second "knows" edge contradicts
//     nothing.
//   - "These two cannot both be true" is the knowledge contract's
//     _contradicts, written by whoever detected the conflict.
//   - "Says who" is fact_provenance and the contract's _grade / _producer /
//     _source, attached to every edge a verdict rests on.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Claim verdicts.
const (
	ClaimSupported    = "supported"
	ClaimContradicted = "contradicted"
	ClaimAbsent       = "absent"
)

// Reasons a verdict carries. Stable strings: a caller branches on them.
const (
	// supported
	ClaimReasonCurrentEdge = "current_edge"
	// contradicted
	ClaimReasonSingleValuedConflict = "single_valued_conflict"
	ClaimReasonConflictingCurrent   = "single_valued_conflict_both_current"
	ClaimReasonEnded                = "ended"
	ClaimReasonExplicit             = "explicit_contradiction"
	// absent
	ClaimReasonNoEdge         = "no_edge"
	ClaimReasonUnknownSubject = "unknown_subject"
	ClaimReasonUnknownObject  = "unknown_object"
	ClaimReasonRetracted      = "retracted"
	ClaimReasonRefused        = "refused"
	ClaimReasonNotYetValid    = "not_yet_valid"
)

// Evidence roles: what one edge contributes to a verdict.
const (
	EvidenceSupports      = "supports"
	EvidenceEnded         = "ended"
	EvidenceOtherValue    = "other_value"
	EvidenceContradicts   = "declares_contradiction"
	EvidenceRetracted     = "retracted"
	EvidenceRefused       = "refused"
	EvidenceNotYetValid   = "not_yet_valid"
	temporalValidFromProp = "valid_from"
	temporalValidToProp   = "valid_to"
)

// Claim is one (subject, relation, object) triple to check. Subject and object
// are names or node ids, resolved the way the rest of the graph API resolves
// them: an existing node id, then the entity id a name derives to (following
// aliases left by entity resolution), then an exact or case/punctuation-folded
// name match. Containment matches are never used — "Leo" must not verify a
// claim about "Leonardo".
type Claim struct {
	Subject  string `json:"subject"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// VerifyClaimsOptions tunes a verification run.
type VerifyClaimsOptions struct {
	// SingleValued names relations the caller knows reach at most one object
	// from a subject, in addition to what the active ontology says. The
	// caller's word is taken for this request only; nothing is written.
	SingleValued []string
	// At is the valid-time instant a claim is checked at: which facts held
	// then, according to everything the store holds now. Zero means now. It
	// is deliberately not a transaction-time as-of — "was Leo living in
	// Beijing in 2020" is answered by what we know today, not by what the
	// store happened to have been told by 2020.
	At time.Time
	// WithText loads the supporting chunk text for every evidence edge.
	WithText bool
}

// ClaimEvidence is one edge a verdict rests on, with where it came from.
type ClaimEvidence struct {
	Role       string         `json:"role"`
	Provenance FactProvenance `json:"provenance"`
	Cited      bool           `json:"cited"`
	// ObjectID is the edge's target; for other_value evidence it is the value
	// the graph holds instead of the claimed one.
	ObjectID  string `json:"object_id"`
	ValidFrom string `json:"valid_from,omitempty"`
	ValidTo   string `json:"valid_to,omitempty"`
	// The knowledge contract, verbatim, when the edge carries it.
	Grade          string `json:"grade,omitempty"`
	Producer       string `json:"producer,omitempty"`
	ContractSource string `json:"contract_source,omitempty"`
	RetractedAt    string `json:"retracted_at,omitempty"`
}

// ClaimVerdict is the answer for one claim.
type ClaimVerdict struct {
	Claim      Claim    `json:"claim"`
	Status     string   `json:"status"`
	Reason     string   `json:"reason"`
	SubjectIDs []string `json:"subject_ids,omitempty"`
	ObjectIDs  []string `json:"object_ids,omitempty"`
	// SingleValued says whether the relation was treated as reaching one
	// object, and SingleValuedBy who said so: "ontology" or "declared".
	SingleValued   bool            `json:"single_valued"`
	SingleValuedBy string          `json:"single_valued_by,omitempty"`
	Evidence       []ClaimEvidence `json:"evidence,omitempty"`
}

// VerifyClaimsResult is every verdict plus a tally.
type VerifyClaimsResult struct {
	Verdicts []ClaimVerdict `json:"verdicts"`
	Counts   map[string]int `json:"counts"`
	At       time.Time      `json:"at"`
}

type claimEdge struct {
	id, from, to, typ string
	props             map[string]any
	retractedAt       time.Time
}

// VerifyClaims checks each claim against the graph and says whether the graph
// supports it, contradicts it, or has nothing on it.
//
// Deterministic: the same store and the same claims give the same verdicts,
// in the order the claims were given.
func (db *DB) VerifyClaims(ctx context.Context, claims []Claim, opts VerifyClaimsOptions) (*VerifyClaimsResult, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: verify claims: nil db")
	}
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("cortexdb: verify claims: %w", err)
	}
	if graph.ReadOptionsFrom(ctx).IsAsOf() {
		return nil, fmt.Errorf("cortexdb: verify claims: an as-of context is not supported; pass VerifyClaimsOptions.At for a valid-time check")
	}
	at := opts.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}

	declared := map[string]bool{}
	for _, r := range opts.SingleValued {
		if k := normalizeOntologyLabel(r); k != "" {
			declared[k] = true
		}
	}
	ontologySingle := map[string]bool{}
	singleValued := func(relation string) (bool, string) {
		key := normalizeOntologyLabel(relation)
		if declared[key] {
			return true, "declared"
		}
		v, seen := ontologySingle[key]
		if !seen {
			single, known := db.LinkSingleValued(ctx, relation)
			if !known && key != ontologyAPIKey(relation) {
				single, known = db.LinkSingleValued(ctx, key)
			}
			v = known && single
			ontologySingle[key] = v
		}
		if v {
			return true, "ontology"
		}
		return false, ""
	}

	resolved, err := db.resolveClaimEndpoints(ctx, claims)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: verify claims: %w", err)
	}
	contra := &contradictionIndex{}

	out := &VerifyClaimsResult{Verdicts: make([]ClaimVerdict, 0, len(claims)), Counts: map[string]int{
		ClaimSupported: 0, ClaimContradicted: 0, ClaimAbsent: 0,
	}, At: at}
	for _, c := range claims {
		v, err := db.verifyOneClaim(ctx, c, resolved, at, singleValued, contra, opts)
		if err != nil {
			return nil, fmt.Errorf("cortexdb: verify claims: %w", err)
		}
		out.Counts[v.Status]++
		out.Verdicts = append(out.Verdicts, v)
	}
	return out, nil
}

// resolveClaimEndpoints maps every subject and object name to node ids, with
// one name scan for all of them.
func (db *DB) resolveClaimEndpoints(ctx context.Context, claims []Claim) (map[string][]string, error) {
	out := map[string][]string{}
	pending := make([]string, 0)
	for _, c := range claims {
		for _, name := range []string{c.Subject, c.Object} {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, done := out[name]; done {
				continue
			}
			out[name] = nil
			if id := db.existingNodeID(ctx, name); id != "" {
				out[name] = []string{id}
				continue
			}
			pending = append(pending, name)
		}
	}
	if len(pending) == 0 {
		return out, nil
	}
	found, err := db.GraphRAGTools().FindNodes(ctx, ToolFindNodesRequest{Names: pending})
	if err != nil {
		return nil, err
	}
	for _, m := range found.Matches {
		if m.Match != "exact" && m.Match != "fold" {
			continue
		}
		ids := make([]string, 0, len(m.Nodes))
		for _, n := range m.Nodes {
			if n != nil {
				ids = append(ids, n.ID)
			}
		}
		out[m.Name] = ids
	}
	return out, nil
}

// existingNodeID is the id a name or id stands for when that node exists.
func (db *DB) existingNodeID(ctx context.Context, nameOrID string) string {
	candidates := []string{nameOrID}
	if alias := db.resolveEntityNameToNode(ctx, nameOrID); alias != "" && alias != nameOrID {
		candidates = append(candidates, alias)
	}
	nodes, err := db.graph.GetNodesBatch(ctx, candidates)
	if err != nil {
		return ""
	}
	present := map[string]bool{}
	for _, n := range nodes {
		if n != nil {
			present[n.ID] = true
		}
	}
	for _, id := range candidates {
		if present[id] {
			return id
		}
	}
	return ""
}

func (db *DB) verifyOneClaim(ctx context.Context, c Claim, resolved map[string][]string, at time.Time,
	singleValued func(string) (bool, string), cidx *contradictionIndex, opts VerifyClaimsOptions) (ClaimVerdict, error) {
	v := ClaimVerdict{Claim: c}
	v.SubjectIDs = resolved[strings.TrimSpace(c.Subject)]
	v.ObjectIDs = resolved[strings.TrimSpace(c.Object)]
	v.SingleValued, v.SingleValuedBy = singleValued(c.Relation)
	relKey := normalizeOntologyLabel(firstNonEmpty(c.Relation, "related_to"))

	if len(v.SubjectIDs) == 0 {
		v.Status, v.Reason = ClaimAbsent, ClaimReasonUnknownSubject
		return v, nil
	}
	objects := map[string]bool{}
	for _, id := range v.ObjectIDs {
		objects[id] = true
	}

	edges, err := db.claimEdgesFrom(ctx, v.SubjectIDs)
	if err != nil {
		return v, err
	}
	var supports, ended, notYet, refused, others []claimEdge
	for _, e := range edges {
		if normalizeOntologyLabel(e.typ) != relKey {
			continue
		}
		state := intervalState(e.props, at)
		if objects[e.to] {
			switch {
			case edgePropString(e.props, KeyGrade) == GradeRefused:
				refused = append(refused, e)
			case state == intervalCurrent:
				supports = append(supports, e)
			case state == intervalEnded:
				ended = append(ended, e)
			default:
				notYet = append(notYet, e)
			}
			continue
		}
		if state == intervalCurrent && edgePropString(e.props, KeyGrade) != GradeRefused {
			others = append(others, e)
		}
	}

	add := func(role string, es []claimEdge) error {
		for _, e := range es {
			ev, err := db.claimEvidence(ctx, role, e, opts.WithText)
			if err != nil {
				return err
			}
			v.Evidence = append(v.Evidence, ev)
		}
		return nil
	}

	if len(supports) > 0 {
		if err := add(EvidenceSupports, supports); err != nil {
			return v, err
		}
		contra, err := db.explicitContradictions(ctx, supports, at, cidx)
		if err != nil {
			return v, err
		}
		switch {
		case len(contra) > 0:
			v.Status, v.Reason = ClaimContradicted, ClaimReasonExplicit
			return v, add(EvidenceContradicts, contra)
		case v.SingleValued && len(others) > 0:
			// The graph holds two current values for a link that reaches one.
			// Neither can be cited as the answer, and calling the claim
			// supported would hide exactly the inconsistency graph_health
			// reports.
			v.Status, v.Reason = ClaimContradicted, ClaimReasonConflictingCurrent
			return v, add(EvidenceOtherValue, others)
		}
		v.Status, v.Reason = ClaimSupported, ClaimReasonCurrentEdge
		return v, nil
	}

	if v.SingleValued && len(others) > 0 {
		v.Status, v.Reason = ClaimContradicted, ClaimReasonSingleValuedConflict
		if err := add(EvidenceOtherValue, others); err != nil {
			return v, err
		}
		return v, add(EvidenceEnded, ended)
	}
	if len(ended) > 0 {
		v.Status, v.Reason = ClaimContradicted, ClaimReasonEnded
		return v, add(EvidenceEnded, ended)
	}

	v.Status = ClaimAbsent
	switch {
	case len(notYet) > 0:
		v.Reason = ClaimReasonNotYetValid
		return v, add(EvidenceNotYetValid, notYet)
	case len(refused) > 0:
		v.Reason = ClaimReasonRefused
		return v, add(EvidenceRefused, refused)
	case len(v.ObjectIDs) == 0:
		v.Reason = ClaimReasonUnknownObject
		return v, nil
	}
	retracted, err := db.retractedClaimEdges(ctx, v.SubjectIDs, relKey, objects)
	if err != nil {
		return v, err
	}
	if len(retracted) > 0 {
		v.Reason = ClaimReasonRetracted
		return v, add(EvidenceRetracted, retracted)
	}
	v.Reason = ClaimReasonNoEdge
	return v, nil
}

// claimEdgesFrom loads every edge leaving the given nodes.
func (db *DB) claimEdgesFrom(ctx context.Context, nodeIDs []string) ([]claimEdge, error) {
	holes, args := sqlPlaceholders(nodeIDs)
	src, srcArgs := db.Graph().EdgeSource(ctx)
	rows, err := db.query(ctx, db.Dialect().Rebind(`
		SELECT id, from_node_id, to_node_id, COALESCE(edge_type, ''), COALESCE(properties, '')
		FROM `+src+` AS e WHERE from_node_id IN (`+holes+`) ORDER BY id`), append(srcArgs, args...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanClaimEdges(rows)
}

type rowScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanClaimEdges(rows rowScanner) ([]claimEdge, error) {
	out := make([]claimEdge, 0)
	for rows.Next() {
		var e claimEdge
		var raw string
		if err := rows.Scan(&e.id, &e.from, &e.to, &e.typ, &raw); err != nil {
			return nil, err
		}
		e.props = decodeEdgeProperties(raw)
		out = append(out, e)
	}
	return out, rows.Err()
}

func decodeEdgeProperties(raw string) map[string]any {
	props := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &props)
	}
	return props
}

// explicitContradictions finds current edges that the knowledge contract says
// cannot be true together with any of the supporting edges — named in either
// direction, since _contradicts is written "on both records by whoever detects
// it" and a writer that only managed one side should still be heard.
func (db *DB) explicitContradictions(ctx context.Context, supports []claimEdge, at time.Time, idx *contradictionIndex) ([]claimEdge, error) {
	supportIDs := map[string]bool{}
	named := map[string]bool{}
	for _, e := range supports {
		supportIDs[e.id] = true
		for _, id := range stringsFromAny(e.props[KeyContradicts]) {
			named[id] = true
		}
	}
	src, srcArgs := db.Graph().EdgeSource(ctx)
	seen := map[string]bool{}
	out := make([]claimEdge, 0)
	keep := func(es []claimEdge) {
		for _, e := range es {
			if supportIDs[e.id] || seen[e.id] || intervalState(e.props, at) != intervalCurrent ||
				edgePropString(e.props, KeyGrade) == GradeRefused {
				continue
			}
			seen[e.id] = true
			out = append(out, e)
		}
	}

	if len(named) > 0 {
		ids := make([]string, 0, len(named))
		for id := range named {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		holes, args := sqlPlaceholders(ids)
		rows, err := db.query(ctx, db.Dialect().Rebind(`
			SELECT id, from_node_id, to_node_id, COALESCE(edge_type, ''), COALESCE(properties, '')
			FROM `+src+` AS e WHERE id IN (`+holes+`) ORDER BY id`), append(append([]any{}, srcArgs...), args...)...)
		if err != nil {
			return nil, err
		}
		es, err := scanClaimEdges(rows)
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		keep(es)
	}

	// The reverse direction: edges naming a supporting edge in their own
	// _contradicts, from an index built once per call.
	byNamed, err := idx.load(ctx, db)
	if err != nil {
		return nil, err
	}
	for id := range supportIDs {
		keep(byNamed[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, nil
}

// contradictionIndex maps an edge id to the edges whose _contradicts names
// it. Few edges carry the key, so one scan per verification serves every
// claim in it instead of one scan per supporting edge.
type contradictionIndex struct {
	loaded  bool
	byNamed map[string][]claimEdge
}

func (idx *contradictionIndex) load(ctx context.Context, db *DB) (map[string][]claimEdge, error) {
	if idx.loaded {
		return idx.byNamed, nil
	}
	// A LIKE narrows the scan; the parse confirms, so the key appearing
	// inside some other string does not count.
	rows, err := db.query(ctx, `
		SELECT id, from_node_id, to_node_id, COALESCE(edge_type, ''), COALESCE(properties, '')
		FROM graph_edges WHERE properties LIKE '%contradicts%' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	es, err := scanClaimEdges(rows)
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	idx.byNamed = map[string][]claimEdge{}
	for _, e := range es {
		for _, named := range stringsFromAny(e.props[KeyContradicts]) {
			idx.byNamed[named] = append(idx.byNamed[named], e)
		}
	}
	idx.loaded = true
	return idx.byNamed, nil
}

// retractedClaimEdges finds the claim's edge in history, retracted. A fact the
// store stopped believing is not a contradiction — a document being deleted
// retracts its facts without denying them — but it is worth showing.
func (db *DB) retractedClaimEdges(ctx context.Context, subjectIDs []string, relKey string, objects map[string]bool) ([]claimEdge, error) {
	holes, args := sqlPlaceholders(subjectIDs)
	rows, err := db.query(ctx, db.Dialect().Rebind(`
		SELECT id, from_node_id, to_node_id, COALESCE(edge_type, ''), COALESCE(properties, ''), retracted_at
		FROM graph_edge_history
		WHERE retracted_at IS NOT NULL AND from_node_id IN (`+holes+`)
		ORDER BY id, retracted_at`), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]int{}
	out := make([]claimEdge, 0)
	for rows.Next() {
		var e claimEdge
		var raw string
		var retracted nullableTime
		if err := rows.Scan(&e.id, &e.from, &e.to, &e.typ, &raw, &retracted); err != nil {
			return nil, err
		}
		if !objects[e.to] || normalizeOntologyLabel(e.typ) != relKey {
			continue
		}
		e.props = decodeEdgeProperties(raw)
		e.retractedAt = retracted.Time
		if i, ok := seen[e.id]; ok {
			out[i] = e // the latest retraction of the same edge wins
			continue
		}
		seen[e.id] = len(out)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (db *DB) claimEvidence(ctx context.Context, role string, e claimEdge, withText bool) (ClaimEvidence, error) {
	prov := FactProvenance{EdgeID: e.id, From: e.from, To: e.to, Type: e.typ}
	prov.fillFromProperties(e.props)
	if withText && len(prov.ChunkIDs) > 0 && e.retractedAt.IsZero() {
		full, err := db.FactProvenanceFor(ctx, e.id, true)
		if err != nil {
			return ClaimEvidence{}, err
		}
		prov = *full
	}
	ev := ClaimEvidence{
		Role:           role,
		Provenance:     prov,
		Cited:          prov.Cited() || edgePropString(e.props, KeySource) != "",
		ObjectID:       e.to,
		ValidFrom:      edgePropString(e.props, temporalValidFromProp),
		ValidTo:        edgePropString(e.props, temporalValidToProp),
		Grade:          edgePropString(e.props, KeyGrade),
		Producer:       edgePropString(e.props, KeyProducer),
		ContractSource: edgePropString(e.props, KeySource),
	}
	if !e.retractedAt.IsZero() {
		ev.RetractedAt = e.retractedAt.UTC().Format(time.RFC3339Nano)
	}
	return ev, nil
}

func edgePropString(props map[string]any, key string) string {
	if props == nil {
		return ""
	}
	switch v := props[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

type intervalStateKind int

const (
	intervalCurrent intervalStateKind = iota
	intervalEnded
	intervalFuture
)

// intervalState places an edge's valid-time interval relative to an instant.
// An edge with no interval is current, which is what every edge written
// without temporal facts means. An unparseable bound is ignored rather than
// read as a date: a garbled valid_to must not quietly end a fact.
func intervalState(props map[string]any, at time.Time) intervalStateKind {
	if to, ok := parseFactTime(edgePropString(props, temporalValidToProp)); ok && !to.After(at) {
		return intervalEnded
	}
	if from, ok := parseFactTime(edgePropString(props, temporalValidFromProp)); ok && from.After(at) {
		return intervalFuture
	}
	return intervalCurrent
}

// parseFactTime reads the timestamps temporal facts are written with.
func parseFactTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// nullableTime scans a TIMESTAMP column that may come back as a time, a
// string, or NULL depending on the driver and on who wrote the row.
type nullableTime struct{ Time time.Time }

func (n *nullableTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		n.Time = time.Time{}
	case time.Time:
		n.Time = v.UTC()
	case []byte:
		n.Time = parseStoreTime(string(v))
	case string:
		n.Time = parseStoreTime(v)
	default:
		return fmt.Errorf("unsupported timestamp %T", src)
	}
	return nil
}

func parseStoreTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 -0700",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	if t, ok := parseFactTime(s); ok {
		return t
	}
	return time.Time{}
}

// --- tool surface ------------------------------------------------------------

// ToolVerifyClaimsRequest is the verify_claims tool's input.
type ToolVerifyClaimsRequest struct {
	Claims       []Claim  `json:"claims"`
	SingleValued []string `json:"single_valued,omitempty"`
	// At is an RFC 3339 valid-time instant; empty means now.
	At       string `json:"at,omitempty"`
	WithText bool   `json:"with_text,omitempty"`
}

// VerifyClaimsTool serves the MCP tool of the same name.
func (db *DB) VerifyClaimsTool(ctx context.Context, req ToolVerifyClaimsRequest) (VerifyClaimsResult, error) {
	if len(req.Claims) == 0 {
		return VerifyClaimsResult{}, fmt.Errorf("verify_claims: at least one claim is required")
	}
	opts := VerifyClaimsOptions{SingleValued: req.SingleValued, WithText: req.WithText}
	if strings.TrimSpace(req.At) != "" {
		at, ok := parseFactTime(strings.TrimSpace(req.At))
		if !ok {
			return VerifyClaimsResult{}, fmt.Errorf("verify_claims: at %q is not an RFC 3339 time", req.At)
		}
		opts.At = at
	}
	res, err := db.VerifyClaims(ctx, req.Claims, opts)
	if err != nil {
		return VerifyClaimsResult{}, err
	}
	return *res, nil
}
