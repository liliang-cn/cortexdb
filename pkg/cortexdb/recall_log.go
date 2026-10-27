package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// The recall log: what was asked, what came back, how each hit was found, and
// which records keep coming back or never do.
//
// It is a table in the database file rather than only the JSON-lines trace,
// because the questions asked of it are queries — every recall that reached
// this memory, the most-recalled records, a scoped key's own recalls — and a
// trace file is a log on one machine, rotated away, that a client of a shared
// brain cannot read.
//
// It stores queries, which can hold anything a person typed, so it is off
// unless asked for (WithRecallLog, CORTEXDB_RECALL_LOG=1), keeps CORTEXDB_
// RECALL_LOG_DAYS of it (30) and at most CORTEXDB_RECALL_LOG_MAX_ROWS rows
// (100000), and a key with a scope sees only the recalls it made. Feedback a
// person gives on a hit (useful, noise) is kept past the retention, with the
// query it was about, because it is the input of the recall eval's golden set
// (ExportRecallFeedback).

// RecallLogOptions configures the recall log.
type RecallLogOptions struct {
	// Retention is how long a recall is kept; 0 means 30 days.
	Retention time.Duration
	// MaxRows bounds the recalls kept; 0 means 100000.
	MaxRows int
}

const (
	defaultRecallLogRetention = 30 * 24 * time.Hour
	defaultRecallLogMaxRows   = 100000
	// recallLogPruneEvery is how many recalls are logged between prunes.
	recallLogPruneEvery = 64
	recallLogTitleRunes = 160
)

// WithRecallLog turns the recall log on.
func WithRecallLog(o RecallLogOptions) Option {
	return func(db *DB) {
		if o.Retention <= 0 {
			o.Retention = defaultRecallLogRetention
		}
		if o.MaxRows <= 0 {
			o.MaxRows = defaultRecallLogMaxRows
		}
		db.recallLog = &recallLogRuntime{opts: o}
	}
}

// RecallLogOptionsFromEnv reads CORTEXDB_RECALL_LOG, CORTEXDB_RECALL_LOG_DAYS
// and CORTEXDB_RECALL_LOG_MAX_ROWS. Off returns no option.
func RecallLogOptionsFromEnv() ([]Option, error) {
	v := strings.TrimSpace(os.Getenv("CORTEXDB_RECALL_LOG"))
	if v == "" {
		return nil, nil
	}
	on, err := strconv.ParseBool(v)
	if err != nil {
		return nil, fmt.Errorf("invalid CORTEXDB_RECALL_LOG %q: want 1 or 0", v)
	}
	if !on {
		return nil, nil
	}
	var o RecallLogOptions
	if d := strings.TrimSpace(os.Getenv("CORTEXDB_RECALL_LOG_DAYS")); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid CORTEXDB_RECALL_LOG_DAYS %q: want a whole number of days", d)
		}
		o.Retention = time.Duration(n) * 24 * time.Hour
	}
	if r := strings.TrimSpace(os.Getenv("CORTEXDB_RECALL_LOG_MAX_ROWS")); r != "" {
		n, err := strconv.Atoi(r)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid CORTEXDB_RECALL_LOG_MAX_ROWS %q: want a positive number", r)
		}
		o.MaxRows = n
	}
	return []Option{WithRecallLog(o)}, nil
}

type recallLogRuntime struct {
	opts RecallLogOptions

	mu      sync.Mutex
	ready   bool
	inserts int
}

func (db *DB) recallLogOn() bool { return db.recallLog != nil }

// RecallLogEnabled reports whether recalls are being logged.
func (db *DB) RecallLogEnabled() bool { return db.recallLogOn() }

// ensureRecallLog creates the tables once. Not latched on failure, so one
// cancelled context does not turn the log off for the life of the DB.
func (db *DB) ensureRecallLog(ctx context.Context) error {
	rt := db.recallLog
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.ready {
		return nil
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS recall_log (
			id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL DEFAULT '',
			ts_ms BIGINT NOT NULL,
			key_id TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '',
			agent TEXT NOT NULL DEFAULT '',
			tool TEXT NOT NULL DEFAULT '',
			query TEXT NOT NULL,
			planner TEXT NOT NULL DEFAULT '',
			latency_ms BIGINT NOT NULL DEFAULT 0,
			hit_count INTEGER NOT NULL DEFAULT 0,
			degraded TEXT NOT NULL DEFAULT '',
			hits TEXT NOT NULL DEFAULT '[]',
			explain_json TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_recall_log_ts ON recall_log(ts_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_recall_log_key ON recall_log(key_id, ts_ms)`,
		`CREATE TABLE IF NOT EXISTS recall_log_hits (
			recall_id TEXT NOT NULL,
			record_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			rank INTEGER NOT NULL,
			ts_ms BIGINT NOT NULL,
			key_id TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (recall_id, kind, record_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_recall_log_hits_record ON recall_log_hits(record_id, ts_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_recall_log_hits_ts ON recall_log_hits(ts_ms)`,
		`CREATE TABLE IF NOT EXISTS recall_feedback (
			recall_id TEXT NOT NULL,
			record_id TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT '',
			verdict TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			key_id TEXT NOT NULL DEFAULT '',
			query TEXT NOT NULL DEFAULT '',
			plan TEXT NOT NULL DEFAULT '',
			ts_ms BIGINT NOT NULL,
			PRIMARY KEY (recall_id, record_id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.exec(ctx, stmt); err != nil {
			return fmt.Errorf("recall log schema: %w", err)
		}
	}
	rt.ready = true
	return nil
}

// RecallLogHit is one hit as the log keeps it: enough to show it in a
// timeline without reading the record.
type RecallLogHit struct {
	Kind string `json:"kind"` // memory or knowledge
	ID   string `json:"id"`
	// NodeID is the record's node in the graph, for lighting it up.
	NodeID     string   `json:"node_id,omitempty"`
	Rank       int      `json:"rank"`
	Title      string   `json:"title,omitempty"`
	Score      float64  `json:"score"`
	Similarity float64  `json:"similarity,omitempty"`
	Coverage   float64  `json:"coverage,omitempty"`
	Via        []string `json:"via,omitempty"`
	Writer     string   `json:"writer,omitempty"`
	Source     string   `json:"source,omitempty"`
	Project    string   `json:"project,omitempty"`
	Date       string   `json:"date,omitempty"`
	// Verdict is the feedback given on this hit in this recall, if any.
	Verdict string `json:"verdict,omitempty"`
}

// RecallLogEntry is one logged recall.
type RecallLogEntry struct {
	ID        string         `json:"id"`
	RequestID string         `json:"request_id,omitempty"`
	At        time.Time      `json:"at"`
	Key       string         `json:"key,omitempty"`
	Host      string         `json:"host,omitempty"`
	Project   string         `json:"project,omitempty"`
	Agent     string         `json:"agent,omitempty"`
	Tool      string         `json:"tool,omitempty"`
	Query     string         `json:"query"`
	Planner   string         `json:"planner,omitempty"`
	LatencyMs int64          `json:"latency_ms"`
	HitCount  int            `json:"hit_count"`
	Degraded  []string       `json:"degraded,omitempty"`
	Hits      []RecallLogHit `json:"hits"`
	// Explain is returned by recall_log_get only.
	Explain *RecallExplain `json:"explain,omitempty"`
}

func metaString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// recallLogHits is what the log keeps of resp's hits. Called before an
// index recall cuts them.
func recallLogHits(resp *KnowledgeMemoryRecallResponse) []RecallLogHit {
	out := make([]RecallLogHit, 0, len(resp.Memories)+len(resp.Knowledge))
	for i, h := range resp.Memories {
		m := h.Memory.Metadata
		date := metaString(m, "date")
		if date == "" && !h.Memory.CreatedAt.IsZero() {
			date = h.Memory.CreatedAt.UTC().Format("2006-01-02")
		}
		out = append(out, RecallLogHit{
			Kind: "memory", ID: h.Memory.ID, NodeID: memoryGraphNodeID(h.Memory.ID), Rank: i + 1,
			Title: firstLine(h.Memory.Content, recallLogTitleRunes), Score: h.Score, Similarity: h.Similarity, Coverage: h.Coverage, Via: h.Via,
			Writer: metaString(m, metadataWrittenBy), Source: metaString(m, "source", "agent"), Project: metaString(m, "project"), Date: date,
		})
	}
	for i, h := range resp.Knowledge {
		title := h.Title
		if title == "" {
			title = h.Snippet
		}
		out = append(out, RecallLogHit{
			Kind: "knowledge", ID: h.KnowledgeID, NodeID: "doc:" + h.KnowledgeID, Rank: i + 1,
			Title: firstLine(title, recallLogTitleRunes), Score: h.Score,
			Writer: h.Metadata[metadataWrittenBy], Source: firstNonEmpty(h.Author, h.SourceURL, h.Metadata["source"]), Project: h.Metadata["project"],
			Date: firstNonEmpty(h.Metadata["updated"], h.Metadata["date"]),
		})
	}
	return out
}

// callerKeyOf is the key a call came from: the confined key's own id, else
// the writer the key stamped on ctx.
func callerKeyOf(ctx context.Context) string {
	if scope, ok := callerScopeOf(ctx); ok && scope.Key != "" {
		return scope.Key
	}
	return writerOf(ctx)
}

// logRecall records one recall. It never fails the recall: a log that
// cannot be written is reported and skipped.
func (db *DB) logRecall(ctx context.Context, req KnowledgeMemoryRecallRequest, resp *KnowledgeMemoryRecallResponse, explain *RecallExplain, latency time.Duration) {
	if !db.recallLogOn() || resp == nil {
		return
	}
	// Detached from the caller: the recall is answered whether or not the
	// caller is still waiting, and its record should be too.
	ctx = context.WithoutCancel(ctx)
	if err := db.writeRecallLog(ctx, req, resp, explain, latency); err != nil {
		log.Printf("cortexdb: recall log: %v", err)
	}
}

func (db *DB) writeRecallLog(ctx context.Context, req KnowledgeMemoryRecallRequest, resp *KnowledgeMemoryRecallResponse, explain *RecallExplain, latency time.Duration) error {
	if err := db.ensureRecallLog(ctx); err != nil {
		return err
	}
	call := observability.CallFrom(ctx)
	client := call.Client()
	hits := recallLogHits(resp)
	hitsJSON, err := json.Marshal(hits)
	if err != nil {
		return err
	}
	explainJSON := ""
	if explain != nil {
		if b, err := json.Marshal(explain); err == nil {
			explainJSON = string(b)
		}
	}
	id := uuid.NewString()
	now := time.Now().UTC().UnixMilli()
	key := callerKeyOf(ctx)
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := db.txExec(ctx, tx, `INSERT INTO recall_log
		(id, request_id, ts_ms, key_id, host, project, agent, tool, query, planner, latency_ms, hit_count, degraded, hits, explain_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, call.ID(), now, key, client.Host, client.Project, client.Agent, "knowledge_memory_recall",
		resp.Query, req.Planner, latency.Milliseconds(), len(hits), strings.Join(resp.Degraded, ","), string(hitsJSON), explainJSON); err != nil {
		return err
	}
	for _, h := range hits {
		if _, err := db.txExec(ctx, tx, `INSERT INTO recall_log_hits (recall_id, record_id, kind, rank, ts_ms, key_id) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (recall_id, kind, record_id) DO NOTHING`, id, h.ID, h.Kind, h.Rank, now, key); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	rt := db.recallLog
	rt.mu.Lock()
	rt.inserts++
	prune := rt.inserts%recallLogPruneEvery == 1
	rt.mu.Unlock()
	if prune {
		return db.pruneRecallLog(ctx)
	}
	return nil
}

// pruneRecallLog drops recalls past the retention and past the row bound.
// Feedback stays: it is the golden set's input and carries its own query.
func (db *DB) pruneRecallLog(ctx context.Context) error {
	cutoff := time.Now().Add(-db.recallLog.opts.Retention).UnixMilli()
	var nth sql.NullInt64
	err := db.queryRow(ctx, `SELECT ts_ms FROM recall_log ORDER BY ts_ms DESC LIMIT 1 OFFSET ?`, db.recallLog.opts.MaxRows).Scan(&nth)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if nth.Valid && nth.Int64 >= cutoff {
		cutoff = nth.Int64 + 1
	}
	for _, table := range []string{"recall_log", "recall_log_hits"} {
		if _, err := db.exec(ctx, `DELETE FROM `+table+` WHERE ts_ms < ?`, cutoff); err != nil {
			return err
		}
	}
	return nil
}

// errRecallLogOff is what the recall-log tools answer on a brain that does
// not keep one.
var errRecallLogOff = errors.New("the recall log is off on this brain: start it with CORTEXDB_RECALL_LOG=1 (it stores queries; see CORTEXDB_RECALL_LOG_DAYS)")

// recallLogScope is the key filter for a reader: a confined key reads its
// own recalls only; any other caller reads every recall, or the ones of the
// key it names.
func recallLogScope(ctx context.Context, asked string) (string, bool) {
	if scope, ok := callerScopeOf(ctx); ok {
		return scope.Key, true
	}
	return strings.TrimSpace(asked), asked != ""
}

// RecallLogListRequest asks for logged recalls, newest first.
type RecallLogListRequest struct {
	Limit int `json:"limit,omitempty"`
	// Before is the at_ms of the oldest recall of the previous page.
	Before int64 `json:"before,omitempty"`
	// RecordID keeps recalls that returned this memory or document.
	RecordID string `json:"record_id,omitempty"`
	// Contains keeps recalls whose query contains this text.
	Contains string `json:"contains,omitempty"`
	// Key keeps the recalls of one key; a confined key is always held to its own.
	Key     string `json:"key,omitempty"`
	Project string `json:"project,omitempty"`
	// Empty keeps only recalls that returned nothing.
	Empty bool `json:"empty,omitempty"`
}

// RecallLogListResponse is a page of recalls.
type RecallLogListResponse struct {
	Recalls []RecallLogEntry `json:"recalls"`
	// NextBefore continues the list, 0 at its end.
	NextBefore int64 `json:"next_before,omitempty"`
}

const recallLogColumns = `id, request_id, ts_ms, key_id, host, project, agent, tool, query, planner, latency_ms, hit_count, degraded, hits`

func scanRecallLogEntry(scan func(...any) error, withExplain bool) (RecallLogEntry, int64, error) {
	var e RecallLogEntry
	var ts int64
	var degraded, hits, explain string
	dest := []any{&e.ID, &e.RequestID, &ts, &e.Key, &e.Host, &e.Project, &e.Agent, &e.Tool, &e.Query, &e.Planner, &e.LatencyMs, &e.HitCount, &degraded, &hits}
	if withExplain {
		dest = append(dest, &explain)
	}
	if err := scan(dest...); err != nil {
		return e, 0, err
	}
	e.At = time.UnixMilli(ts).UTC()
	if degraded != "" {
		e.Degraded = strings.Split(degraded, ",")
	}
	_ = json.Unmarshal([]byte(hits), &e.Hits)
	if e.Hits == nil {
		e.Hits = []RecallLogHit{}
	}
	if explain != "" {
		e.Explain = &RecallExplain{}
		if err := json.Unmarshal([]byte(explain), e.Explain); err != nil {
			e.Explain = nil
		}
	}
	return e, ts, nil
}

// ListRecalls returns logged recalls, newest first.
func (db *DB) ListRecalls(ctx context.Context, req RecallLogListRequest) (*RecallLogListResponse, error) {
	if !db.recallLogOn() {
		return nil, errRecallLogOff
	}
	if err := db.ensureRecallLog(ctx); err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := []string{"1 = 1"}
	var args []any
	if key, ok := recallLogScope(ctx, req.Key); ok {
		where, args = append(where, "key_id = ?"), append(args, key)
	}
	if req.Before > 0 {
		where, args = append(where, "ts_ms < ?"), append(args, req.Before)
	}
	if s := strings.TrimSpace(req.Contains); s != "" {
		where, args = append(where, "LOWER(query) LIKE ?"), append(args, "%"+strings.ToLower(escapeLike(s))+"%")
		where[len(where)-1] += ` ESCAPE '\'`
	}
	if p := strings.TrimSpace(req.Project); p != "" {
		where, args = append(where, "project = ?"), append(args, p)
	}
	if req.Empty {
		where = append(where, "hit_count = 0")
	}
	if id := strings.TrimSpace(req.RecordID); id != "" {
		where, args = append(where, "id IN (SELECT recall_id FROM recall_log_hits WHERE record_id = ?)"), append(args, id)
	}
	rows, err := db.query(ctx, `SELECT `+recallLogColumns+` FROM recall_log WHERE `+strings.Join(where, " AND ")+
		` ORDER BY ts_ms DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return nil, fmt.Errorf("list recalls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	resp := &RecallLogListResponse{Recalls: []RecallLogEntry{}}
	var last int64
	for rows.Next() {
		e, ts, err := scanRecallLogEntry(rows.Scan, false)
		if err != nil {
			return nil, err
		}
		if len(resp.Recalls) == limit {
			resp.NextBefore = last
			break
		}
		resp.Recalls = append(resp.Recalls, e)
		last = ts
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := db.attachVerdicts(ctx, resp.Recalls); err != nil {
		return nil, err
	}
	return resp, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// RecallLogGetRequest names one logged recall.
type RecallLogGetRequest struct {
	RecallID string `json:"recall_id"`
}

// GetRecall returns one logged recall with its explain record.
func (db *DB) GetRecall(ctx context.Context, req RecallLogGetRequest) (*RecallLogEntry, error) {
	if !db.recallLogOn() {
		return nil, errRecallLogOff
	}
	if err := db.ensureRecallLog(ctx); err != nil {
		return nil, err
	}
	q := `SELECT ` + recallLogColumns + `, explain_json FROM recall_log WHERE id = ?`
	args := []any{strings.TrimSpace(req.RecallID)}
	if key, ok := recallLogScope(ctx, ""); ok {
		q, args = q+" AND key_id = ?", append(args, key)
	}
	e, _, err := scanRecallLogEntry(db.queryRow(ctx, q, args...).Scan, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("recall %q not found", req.RecallID)
	}
	if err != nil {
		return nil, err
	}
	list := []RecallLogEntry{e}
	if err := db.attachVerdicts(ctx, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// attachVerdicts fills in the feedback given on each hit.
func (db *DB) attachVerdicts(ctx context.Context, entries []RecallLogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]any, len(entries))
	marks := make([]string, len(entries))
	at := map[string]int{}
	for i, e := range entries {
		ids[i], marks[i], at[e.ID] = e.ID, "?", i
	}
	rows, err := db.query(ctx, `SELECT recall_id, record_id, verdict FROM recall_feedback WHERE recall_id IN (`+strings.Join(marks, ",")+`)`, ids...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var recallID, recordID, verdict string
		if err := rows.Scan(&recallID, &recordID, &verdict); err != nil {
			return err
		}
		e := &entries[at[recallID]]
		for j := range e.Hits {
			if e.Hits[j].ID == recordID {
				e.Hits[j].Verdict = verdict
			}
		}
	}
	return rows.Err()
}

// RecallStatsRequest asks how often records were recalled.
type RecallStatsRequest struct {
	Limit int `json:"limit,omitempty"`
	// RecordID asks about one record: its count, last recall and queries.
	RecordID string `json:"record_id,omitempty"`
	Key      string `json:"key,omitempty"`
}

// RecordRecallStat is how often one record came back.
type RecordRecallStat struct {
	Kind    string    `json:"kind"`
	ID      string    `json:"id"`
	NodeID  string    `json:"node_id,omitempty"`
	Title   string    `json:"title,omitempty"`
	Count   int       `json:"count"`
	LastAt  time.Time `json:"last_at"`
	Queries []string  `json:"queries,omitempty"`
}

// NeverRecalledRecord is a memory no recall has returned.
type NeverRecalledRecord struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

// RecallStatsResponse is the per-record view of the log.
type RecallStatsResponse struct {
	Since        time.Time          `json:"since"`
	Recalls      int                `json:"recalls"`
	EmptyRecalls int                `json:"empty_recalls"`
	Degraded     int                `json:"degraded_recalls"`
	MostRecalled []RecordRecallStat `json:"most_recalled"`
	Record       *RecordRecallStat  `json:"record,omitempty"`
	// NeverRecalled are memories never returned by any recall (their
	// recall_count is zero), oldest first. Left out for a confined key, whose
	// view of the log is its own recalls only.
	NeverRecalled []NeverRecalledRecord `json:"never_recalled,omitempty"`
}

// RecallStats reports which records recalls reach, and which they never do.
func (db *DB) RecallStats(ctx context.Context, req RecallStatsRequest) (*RecallStatsResponse, error) {
	if !db.recallLogOn() {
		return nil, errRecallLogOff
	}
	if err := db.ensureRecallLog(ctx); err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 || limit > 500 {
		limit = 25
	}
	keyClause, keyArgs := "", []any(nil)
	key, scoped := recallLogScope(ctx, req.Key)
	if scoped {
		keyClause, keyArgs = " AND key_id = ?", []any{key}
	}
	resp := &RecallStatsResponse{Since: time.Now().Add(-db.recallLog.opts.Retention).UTC(), MostRecalled: []RecordRecallStat{}}
	if err := db.queryRow(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN hit_count = 0 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN degraded <> '' THEN 1 ELSE 0 END), 0)
		FROM recall_log WHERE 1 = 1`+keyClause, keyArgs...).Scan(&resp.Recalls, &resp.EmptyRecalls, &resp.Degraded); err != nil {
		return nil, fmt.Errorf("recall stats: %w", err)
	}
	stat := func(q string, args ...any) ([]RecordRecallStat, error) {
		rows, err := db.query(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var out []RecordRecallStat
		for rows.Next() {
			var s RecordRecallStat
			var last int64
			if err := rows.Scan(&s.Kind, &s.ID, &s.Count, &last); err != nil {
				return nil, err
			}
			s.LastAt = time.UnixMilli(last).UTC()
			s.NodeID = recordNodeID(s.Kind, s.ID)
			out = append(out, s)
		}
		return out, rows.Err()
	}
	most, err := stat(`SELECT kind, record_id, COUNT(*), MAX(ts_ms) FROM recall_log_hits WHERE 1 = 1`+keyClause+
		` GROUP BY kind, record_id ORDER BY COUNT(*) DESC, MAX(ts_ms) DESC LIMIT ?`, append(keyArgs, limit)...)
	if err != nil {
		return nil, fmt.Errorf("most recalled: %w", err)
	}
	if err := db.fillStatTitles(ctx, most, keyClause, keyArgs, 0); err != nil {
		return nil, err
	}
	resp.MostRecalled = append(resp.MostRecalled, most...)

	if id := strings.TrimSpace(req.RecordID); id != "" {
		one, err := stat(`SELECT kind, record_id, COUNT(*), MAX(ts_ms) FROM recall_log_hits WHERE record_id = ?`+keyClause+
			` GROUP BY kind, record_id`, append([]any{id}, keyArgs...)...)
		if err != nil {
			return nil, err
		}
		if len(one) > 0 {
			if err := db.fillStatTitles(ctx, one, keyClause, keyArgs, 10); err != nil {
				return nil, err
			}
			resp.Record = &one[0]
		} else {
			resp.Record = &RecordRecallStat{ID: id}
		}
	}

	if !scoped {
		never, err := db.neverRecalledMemories(ctx, limit)
		if err != nil {
			return nil, err
		}
		resp.NeverRecalled = never
	}
	return resp, nil
}

func recordNodeID(kind, id string) string {
	if kind == "memory" {
		return memoryGraphNodeID(id)
	}
	return "doc:" + id
}

// fillStatTitles reads each record's title from its latest logged recall,
// and up to queries of the queries that reached it.
func (db *DB) fillStatTitles(ctx context.Context, stats []RecordRecallStat, keyClause string, keyArgs []any, queries int) error {
	for i := range stats {
		s := &stats[i]
		n := queries
		if n <= 0 {
			n = 3
		}
		rows, err := db.query(ctx, `SELECT l.query, l.hits FROM recall_log l JOIN recall_log_hits h ON h.recall_id = l.id
			WHERE h.record_id = ? AND h.kind = ?`+strings.ReplaceAll(keyClause, "key_id", "l.key_id")+` ORDER BY l.ts_ms DESC LIMIT ?`,
			append(append([]any{s.ID, s.Kind}, keyArgs...), n)...)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for rows.Next() {
			var query, hits string
			if err := rows.Scan(&query, &hits); err != nil {
				_ = rows.Close()
				return err
			}
			if s.Title == "" {
				var list []RecallLogHit
				if json.Unmarshal([]byte(hits), &list) == nil {
					for _, h := range list {
						if h.ID == s.ID && h.Kind == s.Kind {
							s.Title = h.Title
							break
						}
					}
				}
			}
			if !seen[query] {
				seen[query] = true
				s.Queries = append(s.Queries, query)
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// neverRecalledMemories lists memories whose recall_count is zero, oldest
// first: the ones that have waited longest for a recall that never came.
func (db *DB) neverRecalledMemories(ctx context.Context, limit int) ([]NeverRecalledRecord, error) {
	all, err := db.ListAllMemories(ctx)
	if err != nil {
		return nil, err
	}
	var out []NeverRecalledRecord
	for _, m := range all {
		if memoryRecallCount(m.Metadata) > 0 || memorySuperseded(m) {
			continue
		}
		out = append(out, NeverRecalledRecord{ID: m.ID, NodeID: memoryGraphNodeID(m.ID), Title: firstLine(m.Content, recallLogTitleRunes), CreatedAt: m.CreatedAt})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Feedback verdicts.
const (
	RecallVerdictUseful = "useful"
	RecallVerdictNoise  = "noise"
	// RecallVerdictClear removes a verdict.
	RecallVerdictClear = "clear"
)

// RecallFeedbackRequest marks one hit of one recall.
type RecallFeedbackRequest struct {
	RecallID string `json:"recall_id"`
	RecordID string `json:"record_id"`
	Verdict  string `json:"verdict"`
	Note     string `json:"note,omitempty"`
}

// RecallFeedbackResponse confirms a mark.
type RecallFeedbackResponse struct {
	RecallID string `json:"recall_id"`
	RecordID string `json:"record_id"`
	Verdict  string `json:"verdict"`
}

// RecallFeedback marks a hit of a logged recall useful or noise. A confined
// key may mark only its own recalls.
func (db *DB) RecallFeedback(ctx context.Context, req RecallFeedbackRequest) (*RecallFeedbackResponse, error) {
	verdict := strings.ToLower(strings.TrimSpace(req.Verdict))
	switch verdict {
	case RecallVerdictUseful, RecallVerdictNoise, RecallVerdictClear:
	default:
		return nil, fmt.Errorf("verdict must be useful, noise or clear, not %q", req.Verdict)
	}
	entry, err := db.GetRecall(ctx, RecallLogGetRequest{RecallID: req.RecallID})
	if err != nil {
		return nil, err
	}
	var hit *RecallLogHit
	for i := range entry.Hits {
		if entry.Hits[i].ID == req.RecordID {
			hit = &entry.Hits[i]
		}
	}
	if hit == nil {
		return nil, fmt.Errorf("recall %q did not return %q", req.RecallID, req.RecordID)
	}
	if verdict == RecallVerdictClear {
		if _, err := db.exec(ctx, `DELETE FROM recall_feedback WHERE recall_id = ? AND record_id = ?`, entry.ID, hit.ID); err != nil {
			return nil, err
		}
		return &RecallFeedbackResponse{RecallID: entry.ID, RecordID: hit.ID, Verdict: verdict}, nil
	}
	plan := ""
	if entry.Explain != nil {
		if b, err := json.Marshal(entry.Explain.Plan); err == nil {
			plan = string(b)
		}
	}
	if _, err := db.exec(ctx, `INSERT INTO recall_feedback (recall_id, record_id, kind, verdict, note, key_id, query, plan, ts_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (recall_id, record_id) DO UPDATE SET verdict = excluded.verdict, note = excluded.note, key_id = excluded.key_id, ts_ms = excluded.ts_ms`,
		entry.ID, hit.ID, hit.Kind, verdict, firstLine(req.Note, 500), callerKeyOf(ctx), entry.Query, plan, time.Now().UTC().UnixMilli()); err != nil {
		return nil, fmt.Errorf("save feedback: %w", err)
	}
	return &RecallFeedbackResponse{RecallID: entry.ID, RecordID: hit.ID, Verdict: verdict}, nil
}

// RecallEvalCase is one case of the recall eval's golden set, in the format
// cortexdb-mcp-stdio --recall-eval reads.
type RecallEvalCase struct {
	ID          string   `json:"id"`
	Category    string   `json:"category,omitempty"`
	Query       string   `json:"query"`
	EntityNames []string `json:"entity_names,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Expected    []string `json:"expected,omitempty"`
	Forbidden   []string `json:"forbidden,omitempty"`
	Note        string   `json:"note,omitempty"`
}

// RecallFeedbackExportRequest asks for the feedback as golden cases.
type RecallFeedbackExportRequest struct {
	// SinceMs keeps feedback given at or after this time.
	SinceMs int64 `json:"since_ms,omitempty"`
}

// RecallFeedbackExportResponse is the feedback as golden cases: one per
// recall that has any, useful hits expected and noise forbidden.
type RecallFeedbackExportResponse struct {
	Cases []RecallEvalCase `json:"cases"`
}

// ExportRecallFeedback turns the marks people gave on the recalls page into
// golden-set cases. A confined key exports only its own.
func (db *DB) ExportRecallFeedback(ctx context.Context, req RecallFeedbackExportRequest) (*RecallFeedbackExportResponse, error) {
	if !db.recallLogOn() {
		return nil, errRecallLogOff
	}
	if err := db.ensureRecallLog(ctx); err != nil {
		return nil, err
	}
	q := `SELECT recall_id, record_id, verdict, note, query, plan FROM recall_feedback WHERE ts_ms >= ?`
	args := []any{req.SinceMs}
	if key, ok := recallLogScope(ctx, ""); ok {
		q, args = q+" AND key_id = ?", append(args, key)
	}
	rows, err := db.query(ctx, q+` ORDER BY recall_id, ts_ms`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	resp := &RecallFeedbackExportResponse{Cases: []RecallEvalCase{}}
	byRecall := map[string]int{}
	for rows.Next() {
		var recallID, recordID, verdict, note, query, plan string
		if err := rows.Scan(&recallID, &recordID, &verdict, &note, &query, &plan); err != nil {
			return nil, err
		}
		i, ok := byRecall[recallID]
		if !ok {
			c := RecallEvalCase{ID: "feedback-" + recallID, Category: "feedback", Query: query}
			var p RecallExplainPlan
			if plan != "" && json.Unmarshal([]byte(plan), &p) == nil {
				c.Keywords, c.EntityNames = p.Keywords, p.EntityNames
			}
			resp.Cases = append(resp.Cases, c)
			i = len(resp.Cases) - 1
			byRecall[recallID] = i
		}
		c := &resp.Cases[i]
		if verdict == RecallVerdictUseful {
			c.Expected = append(c.Expected, recordID)
		} else {
			c.Forbidden = append(c.Forbidden, recordID)
		}
		if note != "" {
			c.Note = strings.TrimSpace(c.Note + "; " + note)
			c.Note = strings.TrimPrefix(c.Note, "; ")
		}
	}
	return resp, rows.Err()
}
