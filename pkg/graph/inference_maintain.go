package graph

// Automatic maintenance of materialized inferences.
//
// Before this, inferred triples were refreshed by hand: RefreshRDFSInferences
// recomputed them when someone remembered to call it. Every write in between
// — a stored triple deleted, an upsert_relations call that changed the
// property-graph facts FindTriples projects — left inferences derived from
// facts that were no longer true, and a SPARQL query read them back as true.
//
// The maintainer consumes the change feed (changefeed.go) and applies each
// batch of committed changes to an in-memory materialization with DRed
// (inference_dred.go), writing back only what changed.
//
// # Asynchronous, with a watermark
//
// Maintenance runs after commit, on its own goroutine, not inside the writer's
// transaction. Writes reach the store through dozens of paths and processes,
// and there is no single commit point to hook; and on SQLite, doing the
// maintenance inside the write would hold the database's only write lock for
// the duration of the reasoning. A writer pays nothing. A reader that needs
// its own write's consequences waits for the watermark: InferenceAppliedSeq
// is the change-log seq every inference has been brought up to, and
// WaitForInference blocks until it passes a given seq. The watermark is
// committed in the same transaction as the inferences it describes, so it can
// never claim more than the store holds.
//
// # One maintainer per database
//
// Several processes may open one brain — the MCP server, a CLI, the gRPC
// server. Each runs a maintainer, and a lease row elects one of them to do the
// work; the others only watch. A holder that dies is replaced when its lease
// expires, and the replacement rebuilds from the store, so nothing depends on
// the dead process's memory. Every write the holder makes is fenced on the
// lease in the same transaction, so a holder that stalled past its lease
// cannot overwrite its successor.
//
// # Free when there is nothing to infer
//
// No rule fires without schema vocabulary (see isInferenceVocabulary), and the
// shared brain declares none. Until a vocabulary statement is committed the
// maintainer is dormant: it holds no materialization, takes no lease and
// writes nothing — it reads each batch of events, sees no vocabulary in them,
// and moves its cursor in memory. Writing nothing matters on SQLite beyond the
// cost: a transaction that has read and then tries to write fails at once if
// another connection committed in between, so a background writer would turn
// into sporadic SQLITE_BUSY errors in whatever else the process was doing.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const inferenceMaintenanceName = "rdfs"

// InferenceMaintenanceConfig tunes StartInferenceMaintenance. The zero value
// is the default.
type InferenceMaintenanceConfig struct {
	// Options are the inference options the materialization is kept with.
	Options InferenceOptions
	// PollInterval is how often an idle maintainer looks for new events.
	// Default 25ms; WaitForInference wakes it at once.
	PollInterval time.Duration
	// LeaseTTL is how long a holder keeps the lease without renewing it, and
	// so how long a dead holder blocks maintenance. Default 30s; a holder
	// renews every third of it.
	LeaseTTL time.Duration
	// BatchLimit caps the events read per page. Default 2000. Pages read in
	// one step are applied as one batch, so a burst of writes is reasoned
	// over once rather than event by event.
	BatchLimit int
	// Holder names this maintainer in the lease. Default: random.
	Holder string
	// Logf receives errors the loop recovers from. Default log.Printf.
	Logf func(format string, args ...any)
}

func (c InferenceMaintenanceConfig) withDefaults() InferenceMaintenanceConfig {
	if c.PollInterval <= 0 {
		c.PollInterval = 25 * time.Millisecond
	}
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = 30 * time.Second
	}
	if c.BatchLimit <= 0 {
		c.BatchLimit = 2000
	}
	if c.Holder == "" {
		var b [8]byte
		_, _ = rand.Read(b[:])
		c.Holder = "maintainer-" + hex.EncodeToString(b[:])
	}
	if c.Logf == nil {
		c.Logf = log.Printf
	}
	return c
}

// InferenceMaintenanceStats reports what a maintainer has done.
type InferenceMaintenanceStats struct {
	Holding        bool   `json:"holding"`
	Active         bool   `json:"active"` // false: no schema vocabulary, nothing held
	AppliedSeq     int64  `json:"applied_seq"`
	Batches        int64  `json:"batches"`
	Baselines      int64  `json:"baselines"`
	Recomputes     int64  `json:"recomputes"`
	Upserted       int64  `json:"upserted"`
	Deleted        int64  `json:"deleted"`
	LastBatchNanos int64  `json:"last_batch_nanos"`
	LastError      string `json:"last_error,omitempty"`
}

// InferenceMaintainer keeps the inferred triples of one store current.
type InferenceMaintainer struct {
	g      *GraphStore
	cfg    InferenceMaintenanceConfig
	cancel context.CancelFunc
	done   chan struct{}
	wake   chan struct{}

	// mu serializes steps with Exclusive.
	mu sync.Mutex
	// state is nil until the maintainer has looked at the store. With state
	// set and state.inference nil the maintainer is dormant: nothing to infer,
	// no lease. With state.inference set it is active and holds the lease.
	state *maintenanceState
	// wantLease is set when the store has something to maintain and this
	// maintainer has not yet won the lease to do it.
	wantLease    bool
	holding      bool
	renewed      time.Time // last successful acquire or renewal
	leaseChecked time.Time // last time a non-holder looked at the lease

	applied  atomic.Int64
	progress chan struct{} // closed and replaced on every watermark advance
	progMu   sync.Mutex

	batches, baselines, recomputes, upserted, deleted, lastBatch atomic.Int64
	lastErr                                                      atomic.Value
}

type maintenanceState struct {
	cursor     int64
	projection bool
	// explicit and inference are nil while the store has no vocabulary.
	explicit  *explicitFacts
	inference *maintainedInference
	// flushed is the watermark last written to the store; a dormant
	// maintainer advances its cursor without writing every time.
	flushed     int64
	flushedTime time.Time
}

// StartInferenceMaintenance starts maintaining this store's inferred triples
// until ctx is done or Close is called. It installs the change feed if needed.
func (g *GraphStore) StartInferenceMaintenance(ctx context.Context, cfg InferenceMaintenanceConfig) (*InferenceMaintainer, error) {
	if err := g.EnsureChangeFeed(ctx); err != nil {
		return nil, err
	}
	if err := g.ensureMaintenanceTable(ctx); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &InferenceMaintainer{
		g:        g,
		cfg:      cfg.withDefaults(),
		cancel:   cancel,
		done:     make(chan struct{}),
		wake:     make(chan struct{}, 1),
		progress: make(chan struct{}),
	}
	g.feed.mu.Lock()
	g.feed.maintainer = m
	g.feed.mu.Unlock()
	go m.loop(ctx)
	return m, nil
}

// Close stops the maintainer and releases its lease if it holds it, so
// another process can take over at once instead of after the lease expires.
func (m *InferenceMaintainer) Close() error {
	m.cancel()
	<-m.done
	m.g.feed.mu.Lock()
	if m.g.feed.maintainer == m {
		m.g.feed.maintainer = nil
	}
	m.g.feed.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.holding {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.releaseLease(ctx)
}

// Stats reports what the maintainer has done so far.
func (m *InferenceMaintainer) Stats() InferenceMaintenanceStats {
	m.mu.Lock()
	holding, active := m.holding, m.state != nil && m.state.inference != nil
	m.mu.Unlock()
	s := InferenceMaintenanceStats{
		Holding: holding, Active: active,
		AppliedSeq: m.applied.Load(), Batches: m.batches.Load(), Baselines: m.baselines.Load(),
		Recomputes: m.recomputes.Load(), Upserted: m.upserted.Load(), Deleted: m.deleted.Load(),
		LastBatchNanos: m.lastBatch.Load(),
	}
	if err, ok := m.lastErr.Load().(string); ok {
		s.LastError = err
	}
	return s
}

// Inconsistencies is the OWL 2 RL contradiction report of the inferences as
// last maintained (see InferenceInconsistency), the same list a full refresh
// of the same store would return. It is empty while the maintainer is dormant
// or does not hold the lease: a store without vocabulary has nothing to
// contradict, and a passive maintainer holds no materialization to report on.
func (m *InferenceMaintainer) Inconsistencies() []InferenceInconsistency {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil || m.state.inference == nil {
		return nil
	}
	return append([]InferenceInconsistency(nil), m.state.inference.inconsistencies...)
}

// Nudge asks the maintainer to look for new events now.
func (m *InferenceMaintainer) Nudge() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Exclusive runs fn while the maintainer is paused, then makes it rebuild its
// materialization from the store with opts. A full refresh made by hand goes
// through here so the maintainer neither races it nor keeps a materialization
// the refresh has just replaced.
func (m *InferenceMaintainer) Exclusive(opts InferenceOptions, fn func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.Options = opts
	m.state, m.wantLease = nil, false
	defer m.Nudge()
	return fn()
}

func (m *InferenceMaintainer) loop(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(m.cfg.PollInterval)
	defer ticker.Stop()
	for {
		m.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.wake:
		case <-m.g.feed.wakeChan():
		}
	}
}

func (m *InferenceMaintainer) fail(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	m.lastErr.Store(err.Error())
	m.cfg.Logf("cortexdb/graph: inference maintenance: %v", err)
}

func (m *InferenceMaintainer) advance(seq int64) {
	if seq <= m.applied.Load() {
		return
	}
	m.applied.Store(seq)
	m.progMu.Lock()
	close(m.progress)
	m.progress = make(chan struct{})
	m.progMu.Unlock()
}

func (m *InferenceMaintainer) progressChan() <-chan struct{} {
	m.progMu.Lock()
	defer m.progMu.Unlock()
	return m.progress
}

func (m *InferenceMaintainer) step(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	if m.state == nil {
		if !m.wantLease {
			if err := m.observe(ctx); err != nil {
				m.fail(fmt.Errorf("observe: %w", err))
				return
			}
		}
		if m.wantLease {
			held, err := m.holdLease(ctx)
			if err != nil {
				m.fail(err)
				return
			}
			if !held {
				return // another process maintains this store
			}
			if err := m.baseline(ctx); err != nil {
				m.state = nil
				m.fail(fmt.Errorf("baseline: %w", err))
				return
			}
		}
	}
	if m.state.inference != nil {
		held, err := m.holdLease(ctx)
		if err != nil {
			m.fail(err)
			return
		}
		if !held || m.state.projection != m.g.PropertyGraphProjectionEnabled() {
			m.state, m.wantLease = nil, true
			return
		}
	}
	// Read everything committed so far, then apply it as one batch.
	var events []ChangeEvent
	for {
		after := m.state.cursor
		if len(events) > 0 {
			after = events[len(events)-1].Seq
		}
		page, err := m.g.Changes(ctx, after, m.cfg.BatchLimit)
		if errors.Is(err, ErrChangesPruned) {
			// Events this maintainer never saw are gone: look again.
			m.state, m.wantLease = nil, false
			return
		}
		if err != nil {
			m.fail(err)
			return
		}
		events = append(events, page...)
		if len(page) < m.cfg.BatchLimit || len(events) >= 20*m.cfg.BatchLimit {
			break
		}
	}
	if len(events) == 0 {
		m.flushWatermark(ctx)
		return
	}
	start := time.Now()
	if err := m.apply(ctx, events); err != nil {
		m.state, m.wantLease = nil, false
		m.fail(err)
		return
	}
	m.lastBatch.Store(int64(time.Since(start)))
	m.batches.Add(1)
}

// observe looks at the store without writing to it, and either goes dormant
// or asks for the lease.
func (m *InferenceMaintainer) observe(ctx context.Context) error {
	head, err := m.g.ChangesHead(ctx)
	if err != nil {
		return err
	}
	work, err := m.g.storeNeedsInferenceMaintenance(ctx)
	if err != nil {
		return err
	}
	if work {
		m.wantLease = true
		return nil
	}
	m.state = &maintenanceState{cursor: head, projection: m.g.PropertyGraphProjectionEnabled()}
	m.advance(head)
	return nil
}

// storeNeedsInferenceMaintenance reports whether the store holds schema
// vocabulary, or inferred triples that without it are stale.
func (g *GraphStore) storeNeedsInferenceMaintenance(ctx context.Context) (bool, error) {
	vocabulary, err := g.storeHasInferenceVocabulary(ctx)
	if err != nil || vocabulary {
		return vocabulary, err
	}
	var one int
	err = g.queryRow(ctx, `SELECT 1 FROM kg_triples WHERE inferred = 1 AND COALESCE(inference_rule, '') NOT LIKE ? LIMIT 1`,
		SHACLTripleRuleNamePrefix+"%").Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// apply brings the store in line with one batch of events.
func (m *InferenceMaintainer) apply(ctx context.Context, events []ChangeEvent) error {
	last := events[len(events)-1].Seq
	st := m.state
	if st.inference == nil {
		if eventsCarryVocabulary(events) {
			// Vocabulary arrived: take the lease and materialize from the
			// store as it now stands.
			m.state, m.wantLease = nil, true
			m.Nudge()
			return nil
		}
		st.cursor = last
		m.advance(last)
		return nil
	}
	for _, ev := range events {
		if err := applyChangeEvent(st.explicit, ev, st.projection); err != nil {
			return fmt.Errorf("event %d: %w", ev.Seq, err)
		}
	}
	diff, stats := st.inference.update(st.explicit.drainChanged())
	if stats.Recomputed {
		m.recomputes.Add(1)
	}
	if diff.empty() {
		// Nothing to write: the watermark moves in memory and reaches the
		// store with the next write or flush.
		st.cursor = last
		m.advance(last)
		return nil
	}
	if err := m.persist(ctx, diff, last); err != nil {
		return err
	}
	st.inference.commit(diff)
	st.cursor = last
	st.flushed, st.flushedTime = last, time.Now()
	m.advance(last)
	if st.explicit.vocabulary == 0 && len(st.inference.persisted) == 0 {
		// The last vocabulary statement went and took every inference with
		// it; nothing is left to hold.
		st.explicit, st.inference = nil, nil
		return m.releaseLease(ctx)
	}
	return nil
}

// flushWatermark writes an active maintainer's watermark to the store, at most
// once a second: other processes wait on it, and a batch that changed no
// inference has nothing else to write.
func (m *InferenceMaintainer) flushWatermark(ctx context.Context) {
	st := m.state
	if st == nil || st.inference == nil || st.flushed == st.cursor || time.Since(st.flushedTime) < time.Second {
		return
	}
	res, err := m.g.exec(ctx, `UPDATE inference_maintenance SET applied_seq = ? WHERE name = ? AND holder = ?`,
		st.cursor, inferenceMaintenanceName, m.cfg.Holder)
	if err != nil {
		m.fail(err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		m.state, m.holding, m.wantLease = nil, false, true
		return
	}
	st.flushed, st.flushedTime = st.cursor, time.Now()
}

// eventsCarryVocabulary reports whether any event states a vocabulary triple.
func eventsCarryVocabulary(events []ChangeEvent) bool {
	for _, ev := range events {
		if ev.Kind != ChangeKindTriple || len(ev.After) == 0 {
			continue
		}
		triple, explicit, err := decodeTripleState(ev.ID, ev.After)
		if err == nil && explicit && isInferenceVocabulary(triple) {
			return true
		}
	}
	return false
}

// baseline builds the materialization from the store. Callers hold m.mu and
// the lease.
//
// The head is read first and the store after it, so the store already holds
// everything up to the head and possibly more. Replaying events after the head
// over that is harmless: each event sets a row to the state it names, so a
// row is right once its last event is applied, whichever state it started in.
func (m *InferenceMaintainer) baseline(ctx context.Context) error {
	m.baselines.Add(1)
	head, err := m.g.ChangesHead(ctx)
	if err != nil {
		return err
	}
	st := &maintenanceState{cursor: head, projection: m.g.PropertyGraphProjectionEnabled()}
	vocabulary, err := m.g.storeHasInferenceVocabulary(ctx)
	if err != nil {
		return err
	}
	if !vocabulary {
		// Nothing can be inferred, so anything stored as inferred is stale.
		inferredOnly := true
		stale, err := m.g.findStoredTriples(ctx, TriplePattern{Inferred: &inferredOnly})
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(stale))
		for _, triple := range stale {
			if !isSHACLRuleTriple(triple) {
				ids = append(ids, triple.ID)
			}
		}
		if err := m.persist(ctx, inferenceDiff{Deletes: ids}, head); err != nil {
			return err
		}
		m.state, m.wantLease = st, false
		m.advance(head)
		return m.releaseLease(ctx)
	}

	explicitOnly := false
	triples, err := m.g.FindTriples(ctx, TriplePattern{Inferred: &explicitOnly})
	if err != nil {
		return err
	}
	explicit := newExplicitFacts()
	rows := make(map[string][]RDFTriple)
	for _, triple := range triples {
		if row, ok := projectedRowKey(triple.ID); ok {
			rows[row] = append(rows[row], triple)
			continue
		}
		explicit.set(triple.ID, triple, true)
	}
	for row, projected := range rows {
		explicit.setRow(row, projected)
	}
	explicit.drainChanged()

	inference := newMaintainedInference(explicit, m.cfg.Options)
	inferredOnly := true
	stored, err := m.g.findStoredTriples(ctx, TriplePattern{Inferred: &inferredOnly})
	if err != nil {
		return err
	}
	var duplicates []string
	for _, triple := range stored {
		if isSHACLRuleTriple(triple) {
			continue
		}
		key := inferenceContentKey(tripleWithoutInference(triple))
		if _, dup := inference.persisted[key]; dup {
			duplicates = append(duplicates, triple.ID)
			continue
		}
		inference.persisted[key] = persistedInference{ID: triple.ID, Rule: triple.Rule, Supports: uniqueSortedStrings(triple.SupportIDs)}
	}
	diff := inference.fullDiff()
	diff.Deletes = append(diff.Deletes, duplicates...)
	if err := m.persist(ctx, diff, head); err != nil {
		return err
	}
	inference.commit(diff)
	st.explicit, st.inference = explicit, inference
	st.flushed, st.flushedTime = head, time.Now()
	m.state, m.wantLease = st, false
	m.advance(head)
	return nil
}

// projectedRowKey names the property-graph row a projected triple id came
// from: "node:<id>" or "edge:<id>".
func projectedRowKey(id string) (string, bool) {
	if !strings.HasPrefix(id, projectedTripleIDPrefix) {
		return "", false
	}
	kind, rest, _ := strings.Cut(strings.TrimPrefix(id, projectedTripleIDPrefix), ":")
	escaped, _, _ := strings.Cut(rest, ":")
	name, ok := unescapeProjectionName(escaped)
	if !ok {
		return "", false
	}
	if kind == "edge" {
		return "edge:" + name, true
	}
	return "node:" + name, true
}

// storeHasInferenceVocabulary asks the store whether any explicit stored
// triple is vocabulary. Projected triples never are: the projection writes only
// rdf:type to cxt: classes, rdfs:label and cxp:/cxr: predicates.
//
// LIKE rather than a range over the namespace: under a PostgreSQL locale
// collation '#' and '$' do not sort by code point, so a range that is exact on
// SQLite silently matches nothing there. The query runs once per baseline, not
// per write, so the scan LIKE may cost is paid at startup only.
func (g *GraphStore) storeHasInferenceVocabulary(ctx context.Context) (bool, error) {
	var one int
	err := g.queryRow(ctx, `SELECT 1 FROM kg_triples WHERE inferred = 0 AND (
			predicate_value IN (?, ?, ?, ?)
			OR predicate_value LIKE ?
			OR (predicate_value = ? AND object_kind = 'iri' AND (
				object_value LIKE ? OR object_value LIKE ? OR object_value = ?)))
		LIMIT 1`,
		rdfsSubClassOfIRI, rdfsSubPropertyOfIRI, rdfsDomainIRI, rdfsRangeIRI,
		owlNamespace+"%",
		rdfTypeIRI, owlNamespace+"%", rdfsNamespace+"%", rdfPropertyIRI,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// --- events into explicit facts ------------------------------------------

type tripleState struct {
	SubjectKind string          `json:"subject_kind"`
	Subject     string          `json:"subject"`
	Predicate   string          `json:"predicate"`
	ObjectKind  string          `json:"object_kind"`
	Object      string          `json:"object"`
	Datatype    *string         `json:"datatype"`
	Language    *string         `json:"language"`
	GraphKind   *string         `json:"graph_kind"`
	Graph       *string         `json:"graph"`
	Inferred    json.Number     `json:"inferred"`
	Rule        json.RawMessage `json:"rule"`
}

// decodeTripleState rebuilds the triple a kg_triples row holds, exactly as
// scanTriple reads it, and reports whether the row is explicit.
func decodeTripleState(id string, raw json.RawMessage) (RDFTriple, bool, error) {
	var s tripleState
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&s); err != nil {
		return RDFTriple{}, false, err
	}
	triple := RDFTriple{
		ID:        id,
		Subject:   RDFTerm{Kind: s.SubjectKind, Value: s.Subject},
		Predicate: RDFTerm{Kind: RDFTermIRI, Value: s.Predicate},
		Object:    RDFTerm{Kind: s.ObjectKind, Value: s.Object},
	}
	if s.Datatype != nil {
		triple.Object.Datatype = *s.Datatype
	}
	if s.Language != nil {
		triple.Object.Language = *s.Language
	}
	if s.GraphKind != nil {
		graph := RDFTerm{Kind: *s.GraphKind}
		if s.Graph != nil {
			graph.Value = *s.Graph
		}
		triple.Graph = &graph
	}
	inferred, _ := s.Inferred.Int64()
	return triple, inferred == 0, nil
}

type rowState struct {
	NodeType   *string         `json:"node_type"`
	Properties json.RawMessage `json:"properties"`
	From       string          `json:"from"`
	To         string          `json:"to"`
	EdgeType   *string         `json:"edge_type"`
}

// propertiesText is the properties column as the projection reads it: the
// stored text, which Changes re-embedded as JSON.
func (s rowState) propertiesText() string {
	raw := strings.TrimSpace(string(s.Properties))
	if raw == "" || raw == "null" {
		return ""
	}
	if strings.HasPrefix(raw, `"`) {
		var text string
		if err := json.Unmarshal([]byte(raw), &text); err == nil {
			return text
		}
	}
	return raw
}

// applyChangeEvent applies one event to the explicit facts.
func applyChangeEvent(f *explicitFacts, ev ChangeEvent, projection bool) error {
	switch ev.Kind {
	case ChangeKindTriple:
		if len(ev.After) == 0 {
			f.set(ev.ID, RDFTriple{}, false)
			return nil
		}
		triple, explicit, err := decodeTripleState(ev.ID, ev.After)
		if err != nil {
			return err
		}
		if strings.HasPrefix(ev.ID, projectedTripleIDPrefix) {
			// A stored id that collides with the projection's namespace
			// would be read back as projected; refuse to guess.
			return nil
		}
		f.set(ev.ID, triple, explicit)
	case ChangeKindNode:
		if !projection {
			return nil
		}
		var triples []RDFTriple
		if len(ev.After) > 0 {
			var s rowState
			if err := json.Unmarshal(ev.After, &s); err != nil {
				return err
			}
			nodeType := ""
			if s.NodeType != nil {
				nodeType = *s.NodeType
			}
			if !(strings.HasPrefix(ev.ID, "rdf:") && isRDFMirrorKind(nodeType)) {
				triples = projectNodeTriples(ev.ID, nodeType, s.propertiesText())
			}
		}
		f.setRow("node:"+ev.ID, triples)
	case ChangeKindEdge:
		if !projection {
			return nil
		}
		var triples []RDFTriple
		if len(ev.After) > 0 {
			var s rowState
			if err := json.Unmarshal(ev.After, &s); err != nil {
				return err
			}
			edgeType := ""
			if s.EdgeType != nil {
				edgeType = *s.EdgeType
			}
			if edgeType != "" && !rdfFlagSet(s.propertiesText()) {
				triples = []RDFTriple{projectedTriple("edge:"+escapeProjectionName(ev.ID),
					NewIRI(PropertyGraphNodeIRI(s.From)),
					NewIRI(PropertyRelNamespace+escapeProjectionName(edgeType)),
					NewIRI(PropertyGraphNodeIRI(s.To)))}
			}
		}
		f.setRow("edge:"+ev.ID, triples)
	}
	return nil
}

func isRDFMirrorKind(nodeType string) bool {
	for _, kind := range projectionRDFMirrorKind {
		if kind == nodeType {
			return true
		}
	}
	return false
}

// rdfFlagSet mirrors the projection's JSONFlag test for the mirror marker.
func rdfFlagSet(properties string) bool {
	if !strings.Contains(properties, `"rdf"`) {
		return false
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(properties), &fields); err != nil {
		return false
	}
	switch v := fields["rdf"].(type) {
	case bool:
		return v
	case float64:
		return v == 1
	case string:
		return v == "true"
	}
	return false
}

// --- the store side ------------------------------------------------------

func (g *GraphStore) ensureMaintenanceTable(ctx context.Context) error {
	_, err := g.exec(ctx, `CREATE TABLE IF NOT EXISTS inference_maintenance (
		name TEXT PRIMARY KEY,
		holder TEXT,
		lease_until BIGINT NOT NULL DEFAULT 0,
		applied_seq BIGINT NOT NULL DEFAULT 0
	)`)
	if err != nil && g.isPostgres() && strings.Contains(err.Error(), "duplicate key") {
		// Two sessions creating the table at once; the other one won.
		return nil
	}
	return err
}

// holdLease takes or renews the lease when it is due. Callers hold m.mu.
//
// A maintainer that does not hold the lease only reads it, and tries to take
// it only once it has expired: the attempt is a write, and a write is what a
// dormant or passive maintainer must not do on every tick.
func (m *InferenceMaintainer) holdLease(ctx context.Context) (bool, error) {
	now := time.Now()
	if m.holding && now.Sub(m.renewed) < m.cfg.LeaseTTL/3 {
		return true, nil
	}
	g := m.g
	if !m.holding {
		if !m.leaseChecked.IsZero() && now.Sub(m.leaseChecked) < 20*m.cfg.PollInterval {
			return false, nil
		}
		m.leaseChecked = now
		var holder sql.NullString
		var until int64
		err := g.queryRow(ctx, `SELECT holder, lease_until FROM inference_maintenance WHERE name = ?`, inferenceMaintenanceName).Scan(&holder, &until)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if err == nil && holder.Valid && holder.String != "" && holder.String != m.cfg.Holder && until >= now.UnixMilli() {
			return false, nil
		}
	}
	until := now.Add(m.cfg.LeaseTTL).UnixMilli()
	res, err := g.exec(ctx, `UPDATE inference_maintenance SET holder = ?, lease_until = ?
		WHERE name = ? AND (holder = ? OR holder IS NULL OR lease_until < ?)`,
		m.cfg.Holder, until, inferenceMaintenanceName, m.cfg.Holder, now.UnixMilli())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		if _, err := g.exec(ctx, `INSERT INTO inference_maintenance (name, holder, lease_until, applied_seq)
			VALUES (?, ?, ?, 0) ON CONFLICT (name) DO NOTHING`, inferenceMaintenanceName, m.cfg.Holder, until); err != nil {
			return false, err
		}
		var holder sql.NullString
		if err := g.queryRow(ctx, `SELECT holder FROM inference_maintenance WHERE name = ?`, inferenceMaintenanceName).Scan(&holder); err != nil {
			return false, err
		}
		if holder.String == m.cfg.Holder {
			n = 1
		}
	}
	m.holding = n == 1
	if m.holding {
		m.renewed = now
	}
	return m.holding, nil
}

// releaseLease gives the lease up. Callers hold m.mu.
func (m *InferenceMaintainer) releaseLease(ctx context.Context) error {
	if !m.holding {
		return nil
	}
	m.holding = false
	_, err := m.g.exec(ctx, `UPDATE inference_maintenance SET holder = NULL, lease_until = 0 WHERE name = ? AND holder = ?`,
		inferenceMaintenanceName, m.cfg.Holder)
	return err
}

// persist writes a diff and the watermark in one transaction, fenced on the
// lease: if another maintainer took the lease, nothing is written.
//
// The maintainer yields to writers. Its transaction touches rows a writer's
// may also touch — the graph mirror nodes of RDF terms above all — in an
// order of its own, so on PostgreSQL the two can deadlock, and the database
// would then abort whichever it chose, possibly the writer's. So the
// maintainer waits for a lock at most maintainerLockTimeout and, if it cannot
// have it, rolls back, releasing everything it held, and tries again a little
// later: a deadlock between the two always ends with the maintainer's retry,
// never with a user's write failing.
func (m *InferenceMaintainer) persist(ctx context.Context, diff inferenceDiff, appliedSeq int64) error {
	g := m.g
	var namespaces []Namespace
	if len(diff.Upserts) > 0 {
		var err error
		if namespaces, err = g.ListNamespaces(ctx); err != nil {
			return err
		}
	}
	backoff := 5 * time.Millisecond
	for attempt := 1; ; attempt++ {
		err := m.persistOnce(ctx, diff, appliedSeq, namespaces)
		if err == nil || !isLockConflict(err) || attempt == 50 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 200*time.Millisecond {
			backoff *= 2
		}
	}
}

// maintainerLockTimeout bounds how long the maintainer waits for a row lock
// on PostgreSQL before giving way. Well under deadlock_timeout's default of
// one second, so that in a deadlock the maintainer's timeout fires before the
// database's detector picks a victim.
const maintainerLockTimeout = "100ms"

// isLockConflict reports a failure that retrying later resolves: a lock not
// granted in time, a deadlock, or SQLite reporting the database busy.
func isLockConflict(err error) bool {
	text := err.Error()
	for _, marker := range []string{"55P03", "40P01", "40001", "SQLITE_BUSY", "database is locked"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func (m *InferenceMaintainer) persistOnce(ctx context.Context, diff inferenceDiff, appliedSeq int64, namespaces []Namespace) error {
	g := m.g
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if g.isPostgres() {
		if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = '"+maintainerLockTimeout+"'"); err != nil {
			return err
		}
	}
	// The fence first: it is also the statement that takes SQLite's write
	// lock, before anything in this transaction reads.
	res, err := g.txExec(ctx, tx, `UPDATE inference_maintenance SET applied_seq = ? WHERE name = ? AND holder = ?`,
		appliedSeq, inferenceMaintenanceName, m.cfg.Holder)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		m.holding = false
		return errors.New("lease lost before persisting")
	}
	if err := g.deleteInferredTx(ctx, tx, diff.Deletes); err != nil {
		return err
	}
	if err := g.upsertInferredTx(ctx, tx, diff.Upserts, namespaces); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	m.upserted.Add(int64(len(diff.Upserts)))
	m.deleted.Add(int64(len(diff.Deletes)))
	return nil
}

// upsertInferredTx writes inferred triples and their graph mirror, a few
// statements per few hundred triples rather than four per triple: on
// PostgreSQL each statement is a round trip, and a batch that rederives fifty
// facts would otherwise spend its time on the network.
//
// It never overwrites an explicit row. An inferred and an explicit statement
// of the same content share an id, and a maintainer working from a batch that
// predates the explicit write must not demote it; the conflict clause updates
// only rows that are themselves inferred, and RETURNING names the rows it
// actually wrote, so the mirror is written for those alone.
func (g *GraphStore) upsertInferredTx(ctx context.Context, tx *sql.Tx, triples []RDFTriple, namespaces []Namespace) error {
	for start := 0; start < len(triples); start += 400 {
		chunk := triples[start:min(start+400, len(triples))]
		values := make([]string, 0, len(chunk))
		args := make([]any, 0, 12*len(chunk))
		for _, triple := range chunk {
			var graphKind, graphValue any
			if triple.Graph != nil {
				graphKind, graphValue = triple.Graph.Kind, triple.Graph.Value
			}
			var supports any
			if len(triple.SupportIDs) > 0 {
				payload, err := json.Marshal(triple.SupportIDs)
				if err != nil {
					return err
				}
				supports = string(payload)
			}
			values = append(values, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)")
			args = append(args, triple.ID, graphKind, graphValue, triple.Subject.Kind, triple.Subject.Value, triple.Predicate.Value,
				triple.Object.Kind, triple.Object.Value, nullIfEmpty(triple.Object.Datatype), nullIfEmpty(triple.Object.Language),
				nullIfEmpty(triple.Rule), supports)
		}
		rows, err := tx.QueryContext(ctx, g.dialect.Rebind(`
			INSERT INTO kg_triples (
				id, graph_kind, graph_value, subject_kind, subject_value, predicate_value,
				object_kind, object_value, object_datatype, object_language,
				inferred, inference_rule, support_ids
			) VALUES `+strings.Join(values, ", ")+`
			ON CONFLICT (id) DO UPDATE SET
				inference_rule = excluded.inference_rule,
				support_ids = excluded.support_ids
			WHERE kg_triples.inferred = 1 AND COALESCE(kg_triples.inference_rule, '') NOT LIKE '`+SHACLTripleRuleNamePrefix+`%'
			RETURNING id`), args...)
		if err != nil {
			return fmt.Errorf("upsert inferred triples: %w", err)
		}
		written := make(map[string]bool, len(chunk))
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			written[id] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		mirrored := make([]RDFTriple, 0, len(written))
		for _, triple := range chunk {
			if written[triple.ID] {
				mirrored = append(mirrored, triple)
			}
		}
		if err := g.mirrorInferredTx(ctx, tx, mirrored, namespaces); err != nil {
			return err
		}
	}
	return nil
}

// mirrorInferredTx writes the graph rows a stored triple is mirrored into:
// a node per term and an edge per triple, as upsertPreparedTripleTx does.
// Term nodes that already exist are left alone — their id is the term, so
// there is nothing to update, and not touching them keeps the maintainer off
// rows a writer is likely to be locking.
func (g *GraphStore) mirrorInferredTx(ctx context.Context, tx *sql.Tx, triples []RDFTriple, namespaces []Namespace) error {
	if len(triples) == 0 {
		return nil
	}
	terms := make(map[string]RDFTerm)
	for _, triple := range triples {
		terms[rdfNodeID(triple.Subject)] = triple.Subject
		terms[rdfNodeID(triple.Object)] = triple.Object
	}
	ids := make([]string, 0, len(terms))
	for id := range terms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, chunk := range chunkStrings(ids, 500) {
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := tx.QueryContext(ctx, g.dialect.Rebind(`SELECT id FROM graph_nodes WHERE id IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`), args...)
		if err != nil {
			return fmt.Errorf("find mirror nodes: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			delete(terms, id)
		}
		_ = rows.Close()
	}
	for _, id := range ids {
		term, missing := terms[id]
		if !missing {
			continue
		}
		if err := g.upsertRDFTermNodeWithLabelTx(ctx, tx, term, rdfTermLabelWithNamespaces(term, namespaces)); err != nil {
			return err
		}
	}
	for start := 0; start < len(triples); start += 400 {
		chunk := triples[start:min(start+400, len(triples))]
		values := make([]string, 0, len(chunk))
		args := make([]any, 0, 7*len(chunk))
		for _, triple := range chunk {
			properties, err := json.Marshal(rdfEdgeProperties(triple))
			if err != nil {
				return err
			}
			values = append(values, "(?, ?, ?, ?, ?, ?, ?)")
			args = append(args, triple.ID, rdfNodeID(triple.Subject), rdfNodeID(triple.Object),
				compactIRIWithNamespaces(triple.Predicate.Value, namespaces), 1.0, string(properties), nil)
		}
		if _, err := g.txExec(ctx, tx, `
			INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type, weight, properties, vector)
			VALUES `+strings.Join(values, ", ")+`
			ON CONFLICT(id) DO UPDATE SET
				from_node_id = excluded.from_node_id,
				to_node_id = excluded.to_node_id,
				edge_type = excluded.edge_type,
				weight = excluded.weight,
				properties = excluded.properties,
				vector = excluded.vector`, args...); err != nil {
			return fmt.Errorf("upsert inferred mirror edges: %w", err)
		}
	}
	return nil
}

// isSHACLRuleTriple reports a triple a SHACL rule inferred. Those are stored
// as inferred triples too, but ApplySHACLRules owns them — it retracts its own
// previous output on each run — and the RDFS/OWL maintainer must neither
// count them as its own nor delete them.
func isSHACLRuleTriple(triple RDFTriple) bool {
	return strings.HasPrefix(triple.Rule, SHACLTripleRuleNamePrefix)
}

// notSHACLRule is the SQL form of !isSHACLRuleTriple.
const notSHACLRule = `COALESCE(inference_rule, '') NOT LIKE '` + SHACLTripleRuleNamePrefix + `%'`

// deleteInferredTx removes inferred triples and their mirror edges, leaving
// any explicit row with the same id — and any SHACL rule's triple — alone.
//
// The inferred test is written (inferred + 0) = 1 so that it cannot use the
// index on inferred: SQLite's planner otherwise prefers that index to the
// primary key, and three deletes on a store with thirty thousand inferences
// became a scan of all of them — 30 ms where the key lookups take 0.3.
func (g *GraphStore) deleteInferredTx(ctx context.Context, tx *sql.Tx, ids []string) error {
	for _, chunk := range chunkStrings(uniqueSortedStrings(ids), 500) {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		if _, err := g.txExec(ctx, tx, `DELETE FROM graph_edges WHERE id IN (
			SELECT id FROM kg_triples WHERE id IN (`+placeholders+`) AND (inferred + 0) = 1 AND `+notSHACLRule+`)`, args...); err != nil {
			return fmt.Errorf("delete inferred mirror edges: %w", err)
		}
		if _, err := g.txExec(ctx, tx, `DELETE FROM kg_triples WHERE id IN (`+placeholders+`) AND (inferred + 0) = 1 AND `+notSHACLRule, args...); err != nil {
			return fmt.Errorf("delete inferred triples: %w", err)
		}
	}
	return nil
}

// --- the watermark -------------------------------------------------------

// InferenceAppliedSeq is the change-log seq the store's inferred triples have
// been brought up to, as committed by whichever process holds maintenance.
// Zero when maintenance has never run.
func (g *GraphStore) InferenceAppliedSeq(ctx context.Context) (int64, error) {
	if err := g.ensureMaintenanceTable(ctx); err != nil {
		return 0, err
	}
	g.feed.mu.Lock()
	local := g.feed.maintainer
	g.feed.mu.Unlock()
	var seq int64
	err := g.queryRow(ctx, `SELECT applied_seq FROM inference_maintenance WHERE name = ?`, inferenceMaintenanceName).Scan(&seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if local != nil && local.applied.Load() > seq {
		seq = local.applied.Load()
	}
	return seq, nil
}

// WaitForInference blocks until inferred triples reflect every change up to
// seq, or ctx ends. seq <= 0 means the change log's head now — "everything
// written before this call".
//
// When no maintainer anywhere holds the lease and the store has nothing to
// maintain — no schema vocabulary, no inferred triples — there is nothing to
// wait for and it returns at once. When there is something to maintain and
// nobody maintains it, it waits until ctx ends: returning would claim
// inferences are current when they are not.
func (g *GraphStore) WaitForInference(ctx context.Context, seq int64) error {
	if seq <= 0 {
		head, err := g.ChangesHead(ctx)
		if err != nil {
			return err
		}
		seq = head
	}
	g.feed.mu.Lock()
	local := g.feed.maintainer
	g.feed.mu.Unlock()
	poll := time.NewTicker(25 * time.Millisecond)
	defer poll.Stop()
	for {
		var progress <-chan struct{}
		if local != nil {
			progress = local.progressChan()
			if local.applied.Load() >= seq {
				return nil
			}
			local.Nudge()
		}
		applied, err := g.InferenceAppliedSeq(ctx)
		if err != nil {
			return err
		}
		if applied >= seq {
			return nil
		}
		if local == nil {
			live, err := g.inferenceHolderLive(ctx)
			if err != nil {
				return err
			}
			if !live {
				work, err := g.storeNeedsInferenceMaintenance(ctx)
				if err != nil {
					return err
				}
				if !work {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for inference to reach %d (at %d): %w", seq, applied, ctx.Err())
		case <-progress:
		case <-poll.C:
		}
	}
}

// inferenceHolderLive reports whether some maintainer holds an unexpired lease.
func (g *GraphStore) inferenceHolderLive(ctx context.Context) (bool, error) {
	var holder sql.NullString
	var until int64
	err := g.queryRow(ctx, `SELECT holder, lease_until FROM inference_maintenance WHERE name = ?`, inferenceMaintenanceName).Scan(&holder, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return holder.Valid && holder.String != "" && until >= time.Now().UnixMilli(), nil
}
