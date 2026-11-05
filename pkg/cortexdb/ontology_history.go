package cortexdb

// Ontology history: every saved version of a schema, and which one was in
// effect when. The storage rules (what is appended, in which transaction, what
// survives a delete) are written at the top of ontology_storage.go; this file
// holds the tables, the writes, and the reads behind ontology_history,
// ontology_get(version / as_of) and ontology_diff(from_version / to_version).
//
// Who saved or switched a version comes from the same place every other write
// takes its writer from: the identity a server put on the context with
// WithWriter (the authenticated key). An embedded library or a server without
// keys has none, and the column is then empty rather than invented.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// Why an activation interval ended.
const (
	ontologyEndReplaced    = "replaced"    // a new version of the same schema took over
	ontologyEndSwitched    = "switched"    // another schema was activated
	ontologyEndDeactivated = "deactivated" // the schema was deactivated, nothing took over
	ontologyEndDeleted     = "deleted"     // the schema was deleted while active
)

const (
	ontologyHistoryDefaultLimit = 50
	ontologyHistoryMaxLimit     = 500
)

// ontologyHistoryNow is the instant a write is stamped with. Microsecond
// precision and UTC because PostgreSQL TIMESTAMP stops at microseconds and a
// stored instant that does not round-trip would put a boundary on the wrong
// side of an as_of that names it.
func ontologyHistoryNow() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

// ensureOntologyHistoryTables creates the two append-only tables and gives a
// database that predates them a starting point: its current rows become their
// own versions, and the active one gets an open interval. Called under
// ontologySchemaMu from ensureOntologySchemaTable.
//
// The backfill is idempotent and tolerates two processes opening the same
// database at once: the primary key and the partial unique index make the
// second insert a no-op (ON CONFLICT DO NOTHING without a target works on both
// SQLite and PostgreSQL). The history before the upgrade is not recoverable,
// so a backfilled version carries no change summary.
func (db *DB) ensureOntologyHistoryTables(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ontology_schema_versions (
			schema_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			strict_actions INTEGER NOT NULL DEFAULT 0,
			metadata TEXT,
			definition TEXT NOT NULL,
			saved_at TIMESTAMP NOT NULL,
			saved_by TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (schema_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS ontology_activations (
			id ` + db.Dialect().AutoIncrementPK() + `,
			schema_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			active_from TIMESTAMP NOT NULL,
			active_to TIMESTAMP,
			activated_by TEXT NOT NULL DEFAULT '',
			ended_by TEXT NOT NULL DEFAULT '',
			end_reason TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_activations_schema ON ontology_activations(schema_id, active_from)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_activations_open ON ontology_activations(schema_id) WHERE active_to IS NULL`,
		`INSERT INTO ontology_schema_versions (schema_id, version, name, description, strict_actions, metadata, definition, saved_at)
			SELECT id, version, name, description, strict_actions, metadata, definition, COALESCE(updated_at, created_at, CURRENT_TIMESTAMP)
			FROM ontology_schemas_v2 WHERE 1 = 1
			ON CONFLICT DO NOTHING`,
		`INSERT INTO ontology_activations (schema_id, version, active_from)
			SELECT id, version, COALESCE(updated_at, created_at, CURRENT_TIMESTAMP)
			FROM ontology_schemas_v2 s WHERE is_active = 1 AND NOT EXISTS (
				SELECT 1 FROM ontology_activations a WHERE a.schema_id = s.id AND a.active_to IS NULL)
			ON CONFLICT DO NOTHING`,
	}
	for _, stmt := range stmts {
		if _, err := db.exec(ctx, stmt); err != nil {
			return fmt.Errorf("ontology history schema: %w", err)
		}
	}
	return nil
}

// OntologyChangeSummary is what a version changed against the one before it,
// computed with DiffOntologySchemas when the version was saved. Counts only;
// ontology_diff returns the changes themselves. DefinitionChanged also covers
// the kinds the diff does not look at (actions, interfaces, object sets), so a
// save that touched only those still reads as a change.
type OntologyChangeSummary struct {
	// Initial marks the first version of a schema: there is nothing to
	// compare it with.
	Initial            bool           `json:"initial,omitempty"`
	DefinitionChanged  bool           `json:"definition_changed"`
	Changes            int            `json:"changes"`
	Breaking           int            `json:"breaking"`
	HasBreakingChanges bool           `json:"has_breaking_changes"`
	ByKind             map[string]int `json:"by_kind,omitempty"`
}

func summarizeOntologyChange(previous *OntologySchema, next OntologySchema, nextDefinition string) *OntologyChangeSummary {
	if previous == nil {
		return &OntologyChangeSummary{Initial: true, DefinitionChanged: true}
	}
	summary := &OntologyChangeSummary{}
	if prev, err := json.Marshal(ontologySchemaDefinitionOf(*previous)); err == nil {
		summary.DefinitionChanged = string(prev) != nextDefinition
	} else {
		summary.DefinitionChanged = true
	}
	diff := DiffOntologySchemas(*previous, next)
	summary.Changes = len(diff.Changes)
	summary.HasBreakingChanges = diff.HasBreakingChanges
	for _, change := range diff.Changes {
		if change.Breaking {
			summary.Breaking++
		}
		if summary.ByKind == nil {
			summary.ByKind = map[string]int{}
		}
		summary.ByKind[change.Kind]++
	}
	return summary
}

func ontologySchemaDefinitionOf(schema OntologySchema) ontologySchemaDefinition {
	return ontologySchemaDefinition{
		Enforcement:      schema.Enforcement,
		ObjectTypes:      schema.ObjectTypes,
		LinkTypes:        schema.LinkTypes,
		InterfaceTypes:   schema.InterfaceTypes,
		SharedProperties: schema.SharedProperties,
		ActionTypes:      schema.ActionTypes,
		ObjectSets:       schema.ObjectSets,
	}
}

func (db *DB) latestOntologyVersionTx(ctx context.Context, tx *sql.Tx, schemaID string) (int, error) {
	var latest sql.NullInt64
	if err := db.querierQueryRow(ctx, tx,
		`SELECT MAX(version) FROM ontology_schema_versions WHERE schema_id = ?`, schemaID).Scan(&latest); err != nil {
		return 0, fmt.Errorf("read latest ontology version: %w", err)
	}
	return int(latest.Int64), nil
}

func (db *DB) recordOntologyVersionTx(ctx context.Context, tx *sql.Tx, schemaID string, version int, name, description string,
	strictActions bool, metadataJSON, definitionJSON string, savedAt time.Time, savedBy string, summary *OntologyChangeSummary) error {
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encode ontology change summary: %w", err)
	}
	if _, err := db.txExec(ctx, tx, `
		INSERT INTO ontology_schema_versions (schema_id, version, name, description, strict_actions, metadata, definition, saved_at, saved_by, summary)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		schemaID, version, name, description, boolToInt(strictActions), metadataJSON, definitionJSON,
		savedAt, savedBy, string(summaryJSON)); err != nil {
		return fmt.Errorf("record ontology version: %w", err)
	}
	return nil
}

// recordOntologyActivationTx keeps the activation intervals in step with the
// live table, inside the same transaction as the change to it. Saving an
// active schema always opens a new interval, because the version in effect has
// changed even when the schema id has not.
func (db *DB) recordOntologyActivationTx(ctx context.Context, tx *sql.Tx, schemaID string, version int,
	wasActive, nowActive bool, at time.Time, by string) error {
	if nowActive {
		if err := db.closeOntologyActivationsTx(ctx, tx, schemaID, true, at, by, ontologyEndSwitched); err != nil {
			return err
		}
		if err := db.closeOntologyActivationsTx(ctx, tx, schemaID, false, at, by, ontologyEndReplaced); err != nil {
			return err
		}
		if _, err := db.txExec(ctx, tx,
			`INSERT INTO ontology_activations (schema_id, version, active_from, activated_by) VALUES (?, ?, ?, ?)`,
			schemaID, version, at, by); err != nil {
			return fmt.Errorf("open ontology activation: %w", err)
		}
		return nil
	}
	if wasActive {
		return db.closeOntologyActivationsTx(ctx, tx, schemaID, false, at, by, ontologyEndDeactivated)
	}
	return nil
}

// closeOntologyActivationsTx ends open intervals: those of schemaID, or, with
// others, those of every other schema.
func (db *DB) closeOntologyActivationsTx(ctx context.Context, tx *sql.Tx, schemaID string, others bool, at time.Time, by, reason string) error {
	op := "="
	if others {
		op = "<>"
	}
	if _, err := db.txExec(ctx, tx,
		`UPDATE ontology_activations SET active_to = ?, ended_by = ?, end_reason = ? WHERE active_to IS NULL AND schema_id `+op+` ?`,
		at, by, reason, schemaID); err != nil {
		return fmt.Errorf("close ontology activation: %w", err)
	}
	return nil
}

// OntologyActivation is one interval during which a schema version was the
// active validator. ActiveTo is nil while it still is.
type OntologyActivation struct {
	SchemaID    string     `json:"schema_id"`
	Version     int        `json:"version"`
	ActiveFrom  time.Time  `json:"active_from"`
	ActiveTo    *time.Time `json:"active_to,omitempty"`
	ActivatedBy string     `json:"activated_by,omitempty"`
	EndedBy     string     `json:"ended_by,omitempty"`
	// EndReason is replaced, switched, deactivated or deleted.
	EndReason string `json:"end_reason,omitempty"`
}

func scanOntologyActivation(scanner ontologyRowScanner) (OntologyActivation, error) {
	var (
		a  OntologyActivation
		to sql.NullTime
	)
	if err := scanner.Scan(&a.SchemaID, &a.Version, &a.ActiveFrom, &to, &a.ActivatedBy, &a.EndedBy, &a.EndReason); err != nil {
		return a, err
	}
	a.ActiveFrom = a.ActiveFrom.UTC()
	if to.Valid {
		t := to.Time.UTC()
		a.ActiveTo = &t
	}
	return a, nil
}

const ontologyActivationColumns = `schema_id, version, active_from, active_to, activated_by, ended_by, end_reason`

// OntologyVersionInfo is one stored version as ontology_history lists it:
// no definition (ontology_get with version returns that), but who saved it,
// what it changed, and when it was in effect.
type OntologyVersionInfo struct {
	Version       int                    `json:"version"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description,omitempty"`
	StrictActions bool                   `json:"strict_actions,omitempty"`
	SavedAt       time.Time              `json:"saved_at"`
	SavedBy       string                 `json:"saved_by,omitempty"`
	Summary       *OntologyChangeSummary `json:"summary,omitempty"`
	// Active is true while this version is the active validator.
	Active bool `json:"active"`
	// Activations are the intervals this version was active, oldest first.
	Activations []OntologyActivation `json:"activations"`
}

// OntologyHistoryRequest lists the versions of one schema, newest first unless
// OldestFirst is set. Limit defaults to 50 (max 500); Offset pages.
type OntologyHistoryRequest struct {
	SchemaID    string `json:"schema_id"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
	OldestFirst bool   `json:"oldest_first,omitempty"`
}

// OntologyHistoryResponse is one page of a schema's history.
type OntologyHistoryResponse struct {
	SchemaID string `json:"schema_id"`
	// Exists is false for a schema that was deleted but whose history remains.
	Exists         bool                  `json:"exists"`
	CurrentVersion int                   `json:"current_version,omitempty"`
	Total          int                   `json:"total"`
	HasMore        bool                  `json:"has_more"`
	Versions       []OntologyVersionInfo `json:"versions"`
}

// OntologyHistory lists every stored version of a schema with its change
// summary and the intervals it was active. Read-only.
func (db *DB) OntologyHistory(ctx context.Context, req OntologyHistoryRequest) (*OntologyHistoryResponse, error) {
	schemaID := strings.TrimSpace(req.SchemaID)
	if schemaID == "" {
		return nil, fmt.Errorf("schema_id is required")
	}
	if req.Limit < 0 || req.Offset < 0 {
		return nil, fmt.Errorf("limit and offset cannot be negative")
	}
	limit := req.Limit
	if limit == 0 {
		limit = ontologyHistoryDefaultLimit
	}
	if limit > ontologyHistoryMaxLimit {
		limit = ontologyHistoryMaxLimit
	}
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}

	var total int
	if err := db.queryRow(ctx, `SELECT COUNT(*) FROM ontology_schema_versions WHERE schema_id = ?`, schemaID).Scan(&total); err != nil {
		return nil, fmt.Errorf("count ontology versions: %w", err)
	}
	if total == 0 {
		return nil, fmt.Errorf("ontology schema %q has no history: %w", schemaID, core.ErrNotFound)
	}
	resp := &OntologyHistoryResponse{SchemaID: schemaID, Total: total, Versions: []OntologyVersionInfo{}}
	if current, err := db.loadOntologySchema(ctx, schemaID); err == nil {
		resp.Exists = true
		resp.CurrentVersion = current.Version
	} else if !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}

	order := "DESC"
	if req.OldestFirst {
		order = "ASC"
	}
	rows, err := db.query(ctx, `SELECT version, name, description, strict_actions, saved_at, saved_by, summary
		FROM ontology_schema_versions WHERE schema_id = ? ORDER BY version `+order+` LIMIT ? OFFSET ?`,
		schemaID, limit, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("list ontology versions: %w", err)
	}
	for rows.Next() {
		var (
			info        OntologyVersionInfo
			description sql.NullString
			strict      int
			summaryRaw  string
		)
		if err := rows.Scan(&info.Version, &info.Name, &description, &strict, &info.SavedAt, &info.SavedBy, &summaryRaw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		info.Description = description.String
		info.StrictActions = strict != 0
		info.SavedAt = info.SavedAt.UTC()
		info.Activations = []OntologyActivation{}
		if summaryRaw != "" {
			var summary OntologyChangeSummary
			if err := json.Unmarshal([]byte(summaryRaw), &summary); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("decode ontology change summary: %w", err)
			}
			info.Summary = &summary
		}
		resp.Versions = append(resp.Versions, info)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	resp.HasMore = req.Offset+len(resp.Versions) < total

	activations, err := db.ontologyActivations(ctx, schemaID)
	if err != nil {
		return nil, err
	}
	byVersion := map[int][]OntologyActivation{}
	for _, a := range activations {
		byVersion[a.Version] = append(byVersion[a.Version], a)
	}
	for i := range resp.Versions {
		v := &resp.Versions[i]
		if list := byVersion[v.Version]; list != nil {
			v.Activations = list
			for _, a := range list {
				if a.ActiveTo == nil {
					v.Active = true
				}
			}
		}
	}
	return resp, nil
}

// ontologyActivations returns a schema's intervals, oldest first.
func (db *DB) ontologyActivations(ctx context.Context, schemaID string) ([]OntologyActivation, error) {
	rows, err := db.query(ctx, `SELECT `+ontologyActivationColumns+
		` FROM ontology_activations WHERE schema_id = ? ORDER BY active_from, id`, schemaID)
	if err != nil {
		return nil, fmt.Errorf("list ontology activations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []OntologyActivation
	for rows.Next() {
		a, err := scanOntologyActivation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ontologyActivationAt finds the interval that contains at: the schema (and
// version) that was the active validator then. schemaID empty means whichever
// schema it was. Intervals never overlap, so the newest one that began at or
// before the instant is the only candidate; the instant belongs to it unless
// it had already ended. Compared in Go, not SQL: SQLite stores instants as
// text, and a text comparison of instants is only right while every one is
// written with the same number of fractional digits.
func (db *DB) ontologyActivationAt(ctx context.Context, schemaID string, at time.Time) (*OntologyActivation, error) {
	query := `SELECT ` + ontologyActivationColumns + ` FROM ontology_activations`
	var args []any
	if schemaID != "" {
		query += ` WHERE schema_id = ?`
		args = append(args, schemaID)
	}
	rows, err := db.query(ctx, query+` ORDER BY active_from DESC, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("find ontology activation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		a, err := scanOntologyActivation(rows)
		if err != nil {
			return nil, err
		}
		if a.ActiveFrom.After(at) {
			continue
		}
		if a.ActiveTo == nil || at.Before(*a.ActiveTo) {
			return &a, nil
		}
		// The newest interval that began by then had already ended, and
		// intervals do not overlap: nothing was active.
		return nil, nil
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

// getOntologyVersionSchemaTx loads one stored version as a schema. Version 0
// is never stored, so it reads as not found.
func (db *DB) getOntologyVersionSchemaTx(ctx context.Context, q ontologyRowQuerier, schemaID string, version int) (*OntologySchema, error) {
	row := db.querierQueryRow(ctx, q, `SELECT schema_id, name, version, description, strict_actions, metadata, definition, saved_at
		FROM ontology_schema_versions WHERE schema_id = ? AND version = ?`, schemaID, version)
	var (
		schema        OntologySchema
		description   sql.NullString
		metadataJSON  sql.NullString
		definitionRaw string
		strict        int
		savedAt       time.Time
	)
	if err := row.Scan(&schema.SchemaID, &schema.Name, &schema.Version, &description, &strict, &metadataJSON, &definitionRaw, &savedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, core.ErrNotFound
		}
		return nil, err
	}
	return buildOntologySchema(&schema, description, strict != 0, metadataJSON, definitionRaw, false, savedAt.UTC(), savedAt.UTC())
}

// loadOntologyVersion is a stored version of schemaID, with Active set to
// whether that version is the active validator right now.
func (db *DB) loadOntologyVersion(ctx context.Context, schemaID string, version int) (*OntologySchema, error) {
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}
	schema, err := db.getOntologyVersionSchemaTx(ctx, db.store.GetDB(), schemaID, version)
	if errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("ontology schema %q has no version %d: %w", schemaID, version, core.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if live, err := db.getOntologySchemaTx(ctx, db.store.GetDB(), schemaID); err == nil {
		schema.Active = live.Active && live.Version == version
	} else if !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	return schema, nil
}

// parseOntologyInstant reads an RFC 3339 instant. A malformed one is refused:
// reading it as "now" would answer a question nobody asked.
func parseOntologyInstant(field, value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not an RFC 3339 instant (for example 2026-03-05T15:00:00Z): %w", field, value, err)
	}
	return t.UTC(), nil
}

// ontologyGetVersioned answers ontology_get when version or as_of is given.
func (db *DB) ontologyGetVersioned(ctx context.Context, req OntologyGetRequest) (*OntologyGetResponse, error) {
	if req.Version != 0 && strings.TrimSpace(req.AsOf) != "" {
		return nil, fmt.Errorf("version and as_of cannot both be given: one names a stored version, the other the version in effect at an instant")
	}
	schemaID := strings.TrimSpace(req.SchemaID)
	if req.Version < 0 {
		return nil, fmt.Errorf("version must be positive")
	}
	if req.Version > 0 {
		if schemaID == "" {
			return nil, fmt.Errorf("schema_id is required")
		}
		schema, err := db.loadOntologyVersion(ctx, schemaID, req.Version)
		if err != nil {
			return nil, err
		}
		return &OntologyGetResponse{Schema: *schema}, nil
	}

	at, err := parseOntologyInstant("as_of", req.AsOf)
	if err != nil {
		return nil, err
	}
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}
	interval, err := db.ontologyActivationAt(ctx, schemaID, at)
	if err != nil {
		return nil, err
	}
	if interval == nil {
		who := "any ontology schema"
		if schemaID != "" {
			who = fmt.Sprintf("ontology schema %q", schemaID)
		}
		return nil, fmt.Errorf("%s was not active at %s: %w", who, at.Format(time.RFC3339Nano), core.ErrNotFound)
	}
	schema, err := db.loadOntologyVersion(ctx, interval.SchemaID, interval.Version)
	if err != nil {
		return nil, err
	}
	return &OntologyGetResponse{Schema: *schema, ActiveInterval: interval}, nil
}

// ontologyDiffVersions answers ontology_diff for two stored versions. from is
// the "before" side. An omitted to_version is the schema's latest version.
func (db *DB) ontologyDiffVersions(ctx context.Context, req OntologyDiffRequest) (*OntologyDiffResponse, error) {
	schemaID := strings.TrimSpace(req.SchemaID)
	if schemaID == "" {
		return nil, fmt.Errorf("schema_id is required")
	}
	if req.FromVersion <= 0 {
		return nil, fmt.Errorf("from_version is required (and positive) to compare stored versions")
	}
	if req.ToVersion < 0 {
		return nil, fmt.Errorf("to_version must be positive")
	}
	if ontologyCandidateGiven(req.Candidate) {
		return nil, fmt.Errorf("give either candidate or from_version/to_version, not both")
	}
	before, err := db.loadOntologyVersion(ctx, schemaID, req.FromVersion)
	if err != nil {
		return nil, err
	}
	var after *OntologySchema
	if req.ToVersion > 0 {
		after, err = db.loadOntologyVersion(ctx, schemaID, req.ToVersion)
	} else {
		var latest int
		if err = db.queryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM ontology_schema_versions WHERE schema_id = ?`, schemaID).Scan(&latest); err != nil {
			return nil, fmt.Errorf("read latest ontology version: %w", err)
		}
		after, err = db.loadOntologyVersion(ctx, schemaID, latest)
	}
	if err != nil {
		return nil, err
	}
	return &OntologyDiffResponse{Diff: DiffOntologySchemas(*before, *after)}, nil
}

func ontologyCandidateGiven(c OntologySchema) bool {
	return strings.TrimSpace(c.SchemaID) != "" || len(c.ObjectTypes) > 0 || len(c.LinkTypes) > 0 ||
		len(c.InterfaceTypes) > 0 || len(c.SharedProperties) > 0 || len(c.ActionTypes) > 0 || len(c.ObjectSets) > 0
}
