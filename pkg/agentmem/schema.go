package agentmem

const schemaSQL = `
CREATE TABLE IF NOT EXISTS agentmem_memories (
	id              TEXT PRIMARY KEY,
	scope_type      TEXT NOT NULL,
	scope_id        TEXT NOT NULL DEFAULT '',
	bank_id         TEXT NOT NULL,
	type            TEXT NOT NULL,
	content         TEXT NOT NULL,
	importance      REAL NOT NULL DEFAULT 0.5,
	source_type     TEXT NOT NULL DEFAULT 'user_input',
	confidence      REAL NOT NULL DEFAULT 0,
	valid_from      DATETIME,
	valid_to        DATETIME,
	superseded_by   TEXT NOT NULL DEFAULT '',
	conflicting     INTEGER NOT NULL DEFAULT 0,
	archived        INTEGER NOT NULL DEFAULT 0,
	archived_at     DATETIME,
	archive_reason  TEXT NOT NULL DEFAULT '',
	access_count    INTEGER NOT NULL DEFAULT 0,
	last_accessed   DATETIME,
	created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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
	at        DATETIME NOT NULL,
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
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agentmem_mental_models (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	content     TEXT NOT NULL,
	tags        TEXT NOT NULL DEFAULT '[]',
	updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agentmem_context_slots (
	bank_id    TEXT NOT NULL,
	slot       TEXT NOT NULL,
	content    TEXT NOT NULL DEFAULT '',
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (bank_id, slot)
);
`

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
