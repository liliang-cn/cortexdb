package cortexdb

// Health checks for a graph that many writers share.
//
// graph_statistics answers "how big and how connected". It cannot answer the
// questions an operator of a shared brain actually loses sleep over, because
// each of them is about who wrote what and when, not about shape:
//
//   - growth by producer — one extractor suddenly writing forty times its usual
//     volume is either a new corpus or a loop, and only the owner can say which;
//   - the degree tail — a node that half the graph points at is usually an
//     extraction artefact ("Unknown", "the system", "it") that every traversal
//     then walks through;
//   - supersessions per day — facts being closed, versioned or retracted in
//     bulk is churn, and churn in a store that is supposed to accumulate means
//     two writers are overwriting each other;
//   - temporal invariants — a link that reaches one object holding two current
//     values at once is the exact inconsistency automatic supersession exists
//     to prevent, and nothing reported it when a writer bypassed it.
//
// Each check is a read, deterministic, and says whether it fired and why. The
// thresholds are options with defaults, not constants, because "spike" is
// relative to a store's own rhythm.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// GraphHealthOptions tunes the checks. Zero values take the defaults.
type GraphHealthOptions struct {
	// Now anchors the window. Zero means the current time.
	Now time.Time
	// WindowDays bounds the growth and supersession checks. Default 30.
	WindowDays int
	// TopN is how many nodes each degree list returns. Default 10.
	TopN int
	// SpikeFactor and MinSpike define a spike: a day at least SpikeFactor
	// times the median of the other active days, and at least MinSpike rows.
	// A series with fewer than three active days has no baseline and never
	// spikes. Defaults 5 and 20.
	SpikeFactor float64
	MinSpike    int
	// MaxSupersedesPerDay fires the supersession check on its own, baseline
	// or not. Default 200.
	MaxSupersedesPerDay int
	// HubFactor and MinHubDegree define a hub: a degree at least HubFactor
	// times the median degree of nodes that have any, and at least
	// MinHubDegree. Defaults 20 and 25.
	HubFactor    float64
	MinHubDegree int
	// SingleValued names relations the caller knows reach one object, in
	// addition to what the active ontology says.
	SingleValued []string
}

func (o GraphHealthOptions) withDefaults() GraphHealthOptions {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	o.Now = o.Now.UTC()
	if o.WindowDays <= 0 {
		o.WindowDays = 30
	}
	if o.TopN <= 0 {
		o.TopN = 10
	}
	if o.SpikeFactor <= 0 {
		o.SpikeFactor = 5
	}
	if o.MinSpike <= 0 {
		o.MinSpike = 20
	}
	if o.MaxSupersedesPerDay <= 0 {
		o.MaxSupersedesPerDay = 200
	}
	if o.HubFactor <= 0 {
		o.HubFactor = 20
	}
	if o.MinHubDegree <= 0 {
		o.MinHubDegree = 25
	}
	return o
}

// GraphHealthReport is every check's result.
type GraphHealthReport struct {
	Now          time.Time              `json:"now"`
	WindowDays   int                    `json:"window_days"`
	Healthy      bool                   `json:"healthy"`
	Alerts       []string               `json:"alerts"`
	Growth       GrowthHealthCheck      `json:"growth"`
	Degree       DegreeHealthCheck      `json:"degree"`
	Supersession SupersessionHealth     `json:"supersession"`
	Temporal     TemporalInvariantCheck `json:"temporal"`
}

// DayCount is one day's count, day as YYYY-MM-DD in UTC.
type DayCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// ProducerGrowth is one producer's writes in the window.
type ProducerGrowth struct {
	// Producer is the record's _producer, or "untagged".
	Producer string     `json:"producer"`
	Nodes    int        `json:"nodes"`
	Edges    int        `json:"edges"`
	Days     []DayCount `json:"days"`
	PeakDay  string     `json:"peak_day,omitempty"`
	Peak     int        `json:"peak"`
	Baseline float64    `json:"baseline_median"`
	Spike    bool       `json:"spike"`
}

// GrowthHealthCheck reports growth per producer.
type GrowthHealthCheck struct {
	Alert     bool             `json:"alert"`
	Producers []ProducerGrowth `json:"producers"`
}

// NodeDegree is one node in the degree tail.
type NodeDegree struct {
	NodeID string `json:"node_id"`
	Label  string `json:"label"`
	Degree int    `json:"degree"`
	Hub    bool   `json:"hub"`
}

// DegreeHealthCheck reports the high out- and in-degree tail. Bookkeeping
// edges (has_chunk, mentions) are excluded: a long document has many chunks by
// construction, and that is not a hub.
type DegreeHealthCheck struct {
	Alert     bool         `json:"alert"`
	MedianOut float64      `json:"median_out"`
	MedianIn  float64      `json:"median_in"`
	TopOut    []NodeDegree `json:"top_out"`
	TopIn     []NodeDegree `json:"top_in"`
}

// SupersessionDay is one day of facts ending.
type SupersessionDay struct {
	Day string `json:"day"`
	// ClosedFacts are live edges whose valid_to fell on this day — a temporal
	// fact superseded or ended.
	ClosedFacts int `json:"closed_facts"`
	// Versions are edge versions replaced by a newer write.
	Versions int `json:"versions"`
	// Retractions are edges the store stopped believing.
	Retractions int  `json:"retractions"`
	Total       int  `json:"total"`
	Spike       bool `json:"spike"`
}

// SupersessionHealth reports supersessions per day.
type SupersessionHealth struct {
	Alert bool              `json:"alert"`
	Days  []SupersessionDay `json:"days"`
}

// TemporalViolation is one broken temporal invariant.
type TemporalViolation struct {
	// Kind is "overlapping_intervals" — a single-valued link with more than
	// one value holding at the same time, "multiple_open" being the case
	// where two or more never end — or "inverted_interval", a valid_to
	// before its valid_from.
	Kind     string   `json:"kind"`
	Subject  string   `json:"subject"`
	Relation string   `json:"relation"`
	EdgeIDs  []string `json:"edge_ids"`
	Objects  []string `json:"objects,omitempty"`
}

// TemporalInvariantCheck reports temporal invariant violations.
type TemporalInvariantCheck struct {
	Alert             bool                `json:"alert"`
	SingleValuedTypes []string            `json:"single_valued_types"`
	Violations        []TemporalViolation `json:"violations"`
}

var bookkeepingEdgeTypes = []string{"has_chunk", "mentions"}

// GraphHealth runs every check.
func (db *DB) GraphHealth(ctx context.Context, opts GraphHealthOptions) (*GraphHealthReport, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: graph health: nil db")
	}
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("cortexdb: graph health: %w", err)
	}
	opts = opts.withDefaults()
	since := opts.Now.AddDate(0, 0, -opts.WindowDays)
	report := &GraphHealthReport{Now: opts.Now, WindowDays: opts.WindowDays, Alerts: []string{}}

	var err error
	if report.Growth, err = db.healthGrowth(ctx, since, opts); err != nil {
		return nil, fmt.Errorf("cortexdb: graph health: growth: %w", err)
	}
	if report.Degree, err = db.healthDegree(ctx, opts); err != nil {
		return nil, fmt.Errorf("cortexdb: graph health: degree: %w", err)
	}
	if report.Supersession, err = db.healthSupersession(ctx, since, opts); err != nil {
		return nil, fmt.Errorf("cortexdb: graph health: supersession: %w", err)
	}
	if report.Temporal, err = db.healthTemporal(ctx, opts); err != nil {
		return nil, fmt.Errorf("cortexdb: graph health: temporal: %w", err)
	}
	for name, fired := range map[string]bool{
		"growth": report.Growth.Alert, "degree": report.Degree.Alert,
		"supersession": report.Supersession.Alert, "temporal": report.Temporal.Alert,
	} {
		if fired {
			report.Alerts = append(report.Alerts, name)
		}
	}
	sort.Strings(report.Alerts)
	report.Healthy = len(report.Alerts) == 0
	return report, nil
}

func dayKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

// spikeOf finds the peak day and whether it is a spike against the median of
// the other active days.
func spikeOf(days map[string]int, opts GraphHealthOptions) (peakDay string, peak int, baseline float64, spike bool) {
	keys := make([]string, 0, len(days))
	for k, n := range days {
		if n > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if days[k] > peak {
			peakDay, peak = k, days[k]
		}
	}
	others := make([]int, 0, len(keys))
	for _, k := range keys {
		if k != peakDay {
			others = append(others, days[k])
		}
	}
	if len(others) < 2 {
		return peakDay, peak, 0, false
	}
	baseline = medianInt(others)
	spike = peak >= opts.MinSpike && float64(peak) >= opts.SpikeFactor*baseline
	return peakDay, peak, baseline, spike
}

func medianInt(values []int) float64 {
	if len(values) == 0 {
		return 0
	}
	s := append([]int(nil), values...)
	sort.Ints(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return float64(s[mid])
	}
	return float64(s[mid-1]+s[mid]) / 2
}

func sortedDays(days map[string]int) []DayCount {
	out := make([]DayCount, 0, len(days))
	for d, n := range days {
		out = append(out, DayCount{Day: d, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}

// healthGrowth counts live nodes and edges recorded in the window by
// producer. Recorded time is the store's own clock; rows written before the
// bitemporal columns existed fall back to created_at.
func (db *DB) healthGrowth(ctx context.Context, since time.Time, opts GraphHealthOptions) (GrowthHealthCheck, error) {
	type acc struct {
		nodes, edges int
		days         map[string]int
	}
	byProducer := map[string]*acc{}
	scan := func(table string, isEdge bool) error {
		rows, err := db.query(ctx, `SELECT COALESCE(properties, ''), recorded_at, created_at FROM `+table)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var raw string
			var recorded, created nullableTime
			if err := rows.Scan(&raw, &recorded, &created); err != nil {
				return err
			}
			at := recorded.Time
			if at.IsZero() {
				at = created.Time
			}
			if at.IsZero() || at.Before(since) || at.After(opts.Now) {
				continue
			}
			producer := "untagged"
			if strings.Contains(raw, KeyProducer) {
				if p := edgePropString(decodeEdgeProperties(raw), KeyProducer); p != "" {
					producer = p
				}
			}
			a := byProducer[producer]
			if a == nil {
				a = &acc{days: map[string]int{}}
				byProducer[producer] = a
			}
			if isEdge {
				a.edges++
			} else {
				a.nodes++
			}
			a.days[dayKey(at)]++
		}
		return rows.Err()
	}
	if err := scan("graph_nodes", false); err != nil {
		return GrowthHealthCheck{}, err
	}
	if err := scan("graph_edges", true); err != nil {
		return GrowthHealthCheck{}, err
	}

	out := GrowthHealthCheck{Producers: make([]ProducerGrowth, 0, len(byProducer))}
	for name, a := range byProducer {
		g := ProducerGrowth{Producer: name, Nodes: a.nodes, Edges: a.edges, Days: sortedDays(a.days)}
		g.PeakDay, g.Peak, g.Baseline, g.Spike = spikeOf(a.days, opts)
		out.Alert = out.Alert || g.Spike
		out.Producers = append(out.Producers, g)
	}
	sort.Slice(out.Producers, func(i, j int) bool {
		ti := out.Producers[i].Nodes + out.Producers[i].Edges
		tj := out.Producers[j].Nodes + out.Producers[j].Edges
		if ti != tj {
			return ti > tj
		}
		return out.Producers[i].Producer < out.Producers[j].Producer
	})
	return out, nil
}

func (db *DB) healthDegree(ctx context.Context, opts GraphHealthOptions) (DegreeHealthCheck, error) {
	holes, args := sqlPlaceholders(bookkeepingEdgeTypes)
	count := func(column string) (map[string]int, error) {
		rows, err := db.query(ctx, db.Dialect().Rebind(`
			SELECT `+column+`, COUNT(*) FROM graph_edges
			WHERE COALESCE(edge_type, '') NOT IN (`+holes+`)
			GROUP BY `+column), args...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		out := map[string]int{}
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				return nil, err
			}
			out[id] = n
		}
		return out, rows.Err()
	}
	outDeg, err := count("from_node_id")
	if err != nil {
		return DegreeHealthCheck{}, err
	}
	inDeg, err := count("to_node_id")
	if err != nil {
		return DegreeHealthCheck{}, err
	}

	res := DegreeHealthCheck{}
	tail := func(deg map[string]int) ([]NodeDegree, float64, bool) {
		values := make([]int, 0, len(deg))
		for _, n := range deg {
			values = append(values, n)
		}
		median := medianInt(values)
		ids := make([]string, 0, len(deg))
		for id := range deg {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			if deg[ids[i]] != deg[ids[j]] {
				return deg[ids[i]] > deg[ids[j]]
			}
			return ids[i] < ids[j]
		})
		if len(ids) > opts.TopN {
			ids = ids[:opts.TopN]
		}
		alert := false
		list := make([]NodeDegree, 0, len(ids))
		for _, id := range ids {
			n := deg[id]
			hub := n >= opts.MinHubDegree && float64(n) >= opts.HubFactor*math.Max(median, 1)
			alert = alert || hub
			list = append(list, NodeDegree{NodeID: id, Degree: n, Hub: hub})
		}
		return list, median, alert
	}
	var outAlert, inAlert bool
	res.TopOut, res.MedianOut, outAlert = tail(outDeg)
	res.TopIn, res.MedianIn, inAlert = tail(inDeg)
	res.Alert = outAlert || inAlert

	ids := make([]string, 0, len(res.TopOut)+len(res.TopIn))
	for _, n := range append(append([]NodeDegree{}, res.TopOut...), res.TopIn...) {
		ids = append(ids, n.NodeID)
	}
	if len(ids) > 0 {
		nodes, err := db.graph.GetNodesBatch(ctx, ids)
		if err != nil {
			return DegreeHealthCheck{}, err
		}
		labels := map[string]string{}
		for _, n := range nodes {
			if n != nil {
				labels[n.ID] = graphNodeLabel(n.ID, n.Content)
			}
		}
		for _, list := range [][]NodeDegree{res.TopOut, res.TopIn} {
			for i := range list {
				list[i].Label = firstNonEmpty(labels[list[i].NodeID], list[i].NodeID)
			}
		}
	}
	return res, nil
}

// healthSupersession counts, per day in the window, temporal facts closed on
// live edges, edge versions replaced, and edges retracted.
func (db *DB) healthSupersession(ctx context.Context, since time.Time, opts GraphHealthOptions) (SupersessionHealth, error) {
	days := map[string]*SupersessionDay{}
	bump := func(t time.Time, f func(*SupersessionDay)) {
		if t.IsZero() || t.Before(since) || t.After(opts.Now) {
			return
		}
		k := dayKey(t)
		d := days[k]
		if d == nil {
			d = &SupersessionDay{Day: k}
			days[k] = d
		}
		f(d)
		d.Total++
	}

	validTo := db.Dialect().JSONTextGuarded("properties", temporalValidToProp)
	rows, err := db.query(ctx, `SELECT `+validTo+` FROM graph_edges WHERE `+validTo+` IS NOT NULL`)
	if err != nil {
		return SupersessionHealth{}, err
	}
	for rows.Next() {
		var s *string
		if err := rows.Scan(&s); err != nil {
			_ = rows.Close()
			return SupersessionHealth{}, err
		}
		if s == nil {
			continue
		}
		if t, ok := parseFactTime(*s); ok {
			bump(t, func(d *SupersessionDay) { d.ClosedFacts++ })
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return SupersessionHealth{}, err
	}
	_ = rows.Close()

	hrows, err := db.query(ctx, `SELECT valid_to, retracted_at FROM graph_edge_history`)
	if err != nil {
		return SupersessionHealth{}, err
	}
	defer func() { _ = hrows.Close() }()
	for hrows.Next() {
		var vto, ret nullableTime
		if err := hrows.Scan(&vto, &ret); err != nil {
			return SupersessionHealth{}, err
		}
		if !ret.Time.IsZero() {
			bump(ret.Time, func(d *SupersessionDay) { d.Retractions++ })
			continue
		}
		bump(vto.Time, func(d *SupersessionDay) { d.Versions++ })
	}
	if err := hrows.Err(); err != nil {
		return SupersessionHealth{}, err
	}

	totals := map[string]int{}
	for k, d := range days {
		totals[k] = d.Total
	}
	peakDay, _, _, spike := spikeOf(totals, opts)
	out := SupersessionHealth{Days: make([]SupersessionDay, 0, len(days))}
	for _, d := range days {
		if d.Total > opts.MaxSupersedesPerDay || (spike && d.Day == peakDay) {
			d.Spike = true
			out.Alert = true
		}
		out.Days = append(out.Days, *d)
	}
	sort.Slice(out.Days, func(i, j int) bool { return out.Days[i].Day < out.Days[j].Day })
	return out, nil
}

// healthTemporal finds single-valued links whose values overlap in time, and
// intervals that end before they begin.
func (db *DB) healthTemporal(ctx context.Context, opts GraphHealthOptions) (TemporalInvariantCheck, error) {
	declared := map[string]bool{}
	for _, r := range opts.SingleValued {
		if k := normalizeOntologyLabel(r); k != "" {
			declared[k] = true
		}
	}
	holes, args := sqlPlaceholders(bookkeepingEdgeTypes)
	rows, err := db.query(ctx, db.Dialect().Rebind(`
		SELECT id, from_node_id, to_node_id, COALESCE(edge_type, ''), COALESCE(properties, '')
		FROM graph_edges WHERE COALESCE(edge_type, '') NOT IN (`+holes+`) ORDER BY id`), args...)
	if err != nil {
		return TemporalInvariantCheck{}, err
	}
	edges, err := scanClaimEdges(rows)
	_ = rows.Close()
	if err != nil {
		return TemporalInvariantCheck{}, err
	}

	out := TemporalInvariantCheck{SingleValuedTypes: []string{}, Violations: []TemporalViolation{}}
	single := map[string]bool{}
	decided := map[string]bool{}
	type interval struct {
		e        claimEdge
		from, to time.Time // zero = unbounded
	}
	groups := map[string][]interval{}
	for _, e := range edges {
		key := normalizeOntologyLabel(e.typ)
		from, hasFrom := parseFactTime(edgePropString(e.props, temporalValidFromProp))
		to, hasTo := parseFactTime(edgePropString(e.props, temporalValidToProp))
		if hasFrom && hasTo && to.Before(from) {
			out.Violations = append(out.Violations, TemporalViolation{
				Kind: "inverted_interval", Subject: e.from, Relation: e.typ, EdgeIDs: []string{e.id},
			})
		}
		if !decided[key] {
			decided[key] = true
			s, known := db.LinkSingleValued(ctx, e.typ)
			single[key] = declared[key] || (known && s)
			if single[key] {
				out.SingleValuedTypes = append(out.SingleValuedTypes, key)
			}
		}
		if !single[key] || edgePropString(e.props, KeyGrade) == GradeRefused {
			continue
		}
		iv := interval{e: e}
		if hasFrom {
			iv.from = from
		}
		if hasTo {
			iv.to = to
		}
		g := e.from + "\x00" + key
		groups[g] = append(groups[g], iv)
	}
	sort.Strings(out.SingleValuedTypes)

	groupKeys := make([]string, 0, len(groups))
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Strings(groupKeys)
	overlaps := func(a, b interval) bool {
		// [from, to) with zero meaning unbounded on that side.
		aEndsBeforeB := !a.to.IsZero() && !b.from.IsZero() && !a.to.After(b.from)
		bEndsBeforeA := !b.to.IsZero() && !a.from.IsZero() && !b.to.After(a.from)
		return !aEndsBeforeB && !bEndsBeforeA
	}
	for _, gk := range groupKeys {
		ivs := groups[gk]
		if len(ivs) < 2 {
			continue
		}
		involved := map[string]claimEdge{}
		for i := range ivs {
			for j := i + 1; j < len(ivs); j++ {
				if ivs[i].e.to == ivs[j].e.to || !overlaps(ivs[i], ivs[j]) {
					continue
				}
				involved[ivs[i].e.id] = ivs[i].e
				involved[ivs[j].e.id] = ivs[j].e
			}
		}
		if len(involved) == 0 {
			continue
		}
		v := TemporalViolation{Kind: "overlapping_intervals"}
		open := 0
		objects := map[string]bool{}
		for _, iv := range ivs {
			e, ok := involved[iv.e.id]
			if !ok {
				continue
			}
			v.Subject, v.Relation = e.from, e.typ
			v.EdgeIDs = append(v.EdgeIDs, e.id)
			if !objects[e.to] {
				objects[e.to] = true
				v.Objects = append(v.Objects, e.to)
			}
			if iv.to.IsZero() || iv.to.After(opts.Now) {
				open++
			}
		}
		if open > 1 {
			v.Kind = "multiple_open"
		}
		sort.Strings(v.EdgeIDs)
		sort.Strings(v.Objects)
		out.Violations = append(out.Violations, v)
	}
	out.Alert = len(out.Violations) > 0
	return out, nil
}

// --- tool surface ------------------------------------------------------------

// ToolGraphHealthRequest is the graph_health tool's input.
type ToolGraphHealthRequest struct {
	WindowDays          int      `json:"window_days,omitempty"`
	TopN                int      `json:"top_n,omitempty"`
	SpikeFactor         float64  `json:"spike_factor,omitempty"`
	MinSpike            int      `json:"min_spike,omitempty"`
	MaxSupersedesPerDay int      `json:"max_supersedes_per_day,omitempty"`
	HubFactor           float64  `json:"hub_factor,omitempty"`
	MinHubDegree        int      `json:"min_hub_degree,omitempty"`
	SingleValued        []string `json:"single_valued,omitempty"`
}

// GraphHealthTool serves the MCP tool of the same name.
func (db *DB) GraphHealthTool(ctx context.Context, req ToolGraphHealthRequest) (GraphHealthReport, error) {
	res, err := db.GraphHealth(ctx, GraphHealthOptions{
		WindowDays:          req.WindowDays,
		TopN:                req.TopN,
		SpikeFactor:         req.SpikeFactor,
		MinSpike:            req.MinSpike,
		MaxSupersedesPerDay: req.MaxSupersedesPerDay,
		HubFactor:           req.HubFactor,
		MinHubDegree:        req.MinHubDegree,
		SingleValued:        req.SingleValued,
	})
	if err != nil {
		return GraphHealthReport{}, err
	}
	return *res, nil
}
