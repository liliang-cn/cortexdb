package cortexdb

// How a memory's embedding is spelled for the column it goes into.
//
// pkg/cortexdb writes `messages` directly — SaveMemory needs an upsert with a
// caller-chosen id, which the store's AddMessage does not offer — and it wrote
// the SQLite spelling of a vector unconditionally: a length-prefixed blob from
// internal/encoding. On PostgreSQL that column is `vector(N)`, and the blob
// arrives as a hex string the type refuses to parse:
//
//	invalid input syntax for type vector: "\x000300005..."
//
// So every memory_save failed on PostgreSQL, which also took out
// knowledge_memory_remember and anything that consolidates into memory — the
// shared brain's whole write path, on the backend it was ported for.
//
// The two representations do round-trip through []byte: PostgreSQL hands the
// column back as its text form, `[0.1,-0.2,…]`, so a row read and written
// unchanged (an update that did not touch the content) stays valid without
// being decoded and re-encoded.

import (
	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// encodeMemoryVector renders a vector for this backend's messages.vector.
func encodeMemoryVector(d sqldialect.Dialect, vec []float32) ([]byte, error) {
	if len(vec) == 0 {
		return nil, nil
	}
	if d != nil && d.Kind() == sqldialect.Postgres {
		return []byte(core.PgVectorLiteral(vec)), nil
	}
	return encoding.EncodeVector(vec)
}

// memoryVectorArg binds an encoded vector as the parameter its column expects.
//
// The bytes have to reach PostgreSQL as text, not as bytea: a []byte parameter
// against a `vector` column is a type mismatch however correct its contents.
// A nil stays nil, so "no embedder" still writes NULL rather than an empty
// vector pgvector has no value for.
func memoryVectorArg(d sqldialect.Dialect, encoded []byte) any {
	if len(encoded) == 0 {
		return nil
	}
	if d != nil && d.Kind() == sqldialect.Postgres {
		return string(encoded)
	}
	return encoded
}
