package agentmem

import (
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// The DDL, once, with the two type names the databases spell differently left
// as verbs.
//
// Nothing else in these eight tables needs a fork: there is no BLOB (agentmem
// stores no vectors), no AUTOINCREMENT and no SERIAL (every primary key is a
// caller-supplied TEXT id or an explicit revision sequence), so the seeded-id
// vs. sequence trap that bit the other packages on this branch has nothing to
// bite here.
//
//   - DATETIME is SQLite's spelling and not a type PostgreSQL has. TIMESTAMPTZ
//     rather than TIMESTAMP because every value written is already UTC and a
//     naive column would silently lose that on the way back.
//   - REAL is four bytes in PostgreSQL, so an importance of 0.8 comes back as
//     0.800000011920929 — the kind of divergence a threshold comparison finds
//     and a round-trip test does not. DOUBLE PRECISION matches what SQLite's
//     REAL already is.
func schemaSQL(d sqldialect.Dialect) string {
	ts, float := "DATETIME", "REAL"
	if d.Kind() == sqldialect.Postgres {
		ts, float = "TIMESTAMPTZ", "DOUBLE PRECISION"
	}
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS agentmem_memories (
	id              TEXT PRIMARY KEY,
	scope_type      TEXT NOT NULL,
	scope_id        TEXT NOT NULL DEFAULT '',
	bank_id         TEXT NOT NULL,
	type            TEXT NOT NULL,
	content         TEXT NOT NULL,
	importance      %[2]s NOT NULL DEFAULT 0.5,
	source_type     TEXT NOT NULL DEFAULT 'user_input',
	confidence      %[2]s NOT NULL DEFAULT 0,
	valid_from      %[1]s,
	valid_to        %[1]s,
	superseded_by   TEXT NOT NULL DEFAULT '',
	conflicting     INTEGER NOT NULL DEFAULT 0,
	archived        INTEGER NOT NULL DEFAULT 0,
	archived_at     %[1]s,
	archive_reason  TEXT NOT NULL DEFAULT '',
	access_count    INTEGER NOT NULL DEFAULT 0,
	last_accessed   %[1]s,
	created_at      %[1]s NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at      %[1]s NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS agentmem_memories_bank      ON agentmem_memories(bank_id);
CREATE INDEX IF NOT EXISTS agentmem_memories_type      ON agentmem_memories(type);
CREATE INDEX IF NOT EXISTS agentmem_memories_scope     ON agentmem_memories(scope_type, scope_id);
CREATE INDEX IF NOT EXISTS agentmem_memories_archived  ON agentmem_memories(archived);

CREATE TABLE IF NOT EXISTS agentmem_tags (
	memory_id TEXT NOT NULL,
	tag       TEXT NOT NULL,
	PRIMARY KEY (memory_id, tag)
);
CREATE INDEX IF NOT EXISTS agentmem_tags_tag ON agentmem_tags(tag);

CREATE TABLE IF NOT EXISTS agentmem_keywords (
	memory_id TEXT NOT NULL,
	keyword   TEXT NOT NULL,
	PRIMARY KEY (memory_id, keyword)
);
CREATE INDEX IF NOT EXISTS agentmem_keywords_kw ON agentmem_keywords(keyword);

CREATE TABLE IF NOT EXISTS agentmem_evidence (
	memory_id   TEXT NOT NULL,
	evidence_id TEXT NOT NULL,
	PRIMARY KEY (memory_id, evidence_id)
);
CREATE INDEX IF NOT EXISTS agentmem_evidence_evidence ON agentmem_evidence(evidence_id);

CREATE TABLE IF NOT EXISTS agentmem_revisions (
	memory_id TEXT NOT NULL,
	seq       INTEGER NOT NULL,
	at        %[1]s NOT NULL,
	by        TEXT NOT NULL DEFAULT '',
	summary   TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (memory_id, seq)
);

CREATE TABLE IF NOT EXISTS agentmem_bank_config (
	bank_id    TEXT PRIMARY KEY,
	mission    TEXT NOT NULL DEFAULT '',
	directives TEXT NOT NULL DEFAULT '[]',
	skepticism INTEGER NOT NULL DEFAULT 0,
	literalism INTEGER NOT NULL DEFAULT 0,
	empathy    INTEGER NOT NULL DEFAULT 0,
	updated_at %[1]s NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agentmem_mental_models (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	content     TEXT NOT NULL,
	tags        TEXT NOT NULL DEFAULT '[]',
	updated_at  %[1]s NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agentmem_context_slots (
	bank_id    TEXT NOT NULL,
	slot       TEXT NOT NULL,
	content    TEXT NOT NULL DEFAULT '',
	updated_at %[1]s NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (bank_id, slot)
);
`, ts, float)
}

// trigram tokenizer is preferred (handles CJK and arbitrary substrings).
// We try it first; if the SQLite build does not support it we fall back to unicode61.
const ftsTrigram = `
CREATE VIRTUAL TABLE IF NOT EXISTS agentmem_fts USING fts5(
	content,
	tags,
	keywords,
	memory_id UNINDEXED,
	tokenize = 'trigram'
);
`

const ftsUnicode61 = `
CREATE VIRTUAL TABLE IF NOT EXISTS agentmem_fts USING fts5(
	content,
	tags,
	keywords,
	memory_id UNINDEXED,
	tokenize = 'unicode61 remove_diacritics 2'
);
`

// PostgreSQL has no FTS5, so agentmem_fts is an ordinary table there.
//
// Keeping the name and the four columns is what lets every writer in crud.go —
// the DELETE, the INSERT, the Clear — stay one statement for both backends;
// only the read in search.go forks. The columns are the denormalised haystack
// the SQLite virtual table already held, so the two backends are searching
// literally the same text.
//
// memory_id is the primary key here where FTS5 had it UNINDEXED, because a
// real table can enforce the one-row-per-memory that upsertFTS assumes and the
// virtual table could not.
const ftsPostgres = `
CREATE TABLE IF NOT EXISTS agentmem_fts (
	memory_id TEXT PRIMARY KEY,
	content   TEXT NOT NULL DEFAULT '',
	tags      TEXT NOT NULL DEFAULT '',
	keywords  TEXT NOT NULL DEFAULT ''
);
`

// The index that makes the substring search in search.go a lookup instead of a
// scan — the counterpart of FTS5's trigram tokenizer, not a workaround for the
// lack of one. Never fatal: pg_trgm is a contrib extension and a managed
// instance may refuse it to this account, in which case the same queries still
// return the same rows, just linearly.
var ftsPostgresIndexes = []string{
	`CREATE EXTENSION IF NOT EXISTS pg_trgm`,
	`CREATE INDEX IF NOT EXISTS agentmem_fts_content_trgm  ON agentmem_fts USING gin (content gin_trgm_ops)`,
	`CREATE INDEX IF NOT EXISTS agentmem_fts_tags_trgm     ON agentmem_fts USING gin (tags gin_trgm_ops)`,
	`CREATE INDEX IF NOT EXISTS agentmem_fts_keywords_trgm ON agentmem_fts USING gin (keywords gin_trgm_ops)`,
}
