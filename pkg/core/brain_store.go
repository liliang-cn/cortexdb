package core

// What a brain needs from its store.
//
// Store describes a vector store. cortexdb.DB needs more than that — documents,
// sessions, chat history, a raw handle for the sibling packages that keep their
// own tables, and the dimension bookkeeping that keeps an index and its rows
// agreeing about how wide a vector is. Those grew on SQLiteStore as concrete
// methods, so DB held a *core.SQLiteStore and could hold nothing else.
//
// This is that surface, written down. It is not a design: it is the set of
// methods DB actually calls, found by listing them. Naming it is what lets DB
// take any backend, and what stops the next backend from discovering the
// requirements one compile error at a time.
//
// It is deliberately not merged into Store. Store is the contract for
// something that holds vectors, and plenty of useful stores would never need
// SyncUpsertedEmbeddings. This is the contract for something that can be a
// brain.

import (
	"context"
	"database/sql"
)

// BrainStore is Store plus everything else cortexdb.DB reaches for.
type BrainStore interface {
	Store

	// GetSimilarityFunc is how this store compares two vectors. The graph
	// layer scores its own candidates and must score them the same way, or a
	// hybrid result would rank differently from a plain search over the same
	// rows.
	GetSimilarityFunc() SimilarityFunc

	// Config reports how this store was built — vector width above all, which
	// callers compare against an embedder's output.
	Config() Config

	// GetDB hands out the raw handle. Sibling packages (agentmem, graphflow,
	// the graph itself) keep their own tables in the same database and manage
	// them directly; this is how they reach it. Callers must not close it.
	GetDB() *sql.DB

	// UpdateDocument replaces a document in place. Store has Create, Get and
	// Delete but not this one; DB needs it, which is the whole reason this
	// interface exists rather than Store being widened.
	UpdateDocument(ctx context.Context, doc *Document) error

	// GetByID and GetByDocID read embeddings back out rather than searching
	// for them — an id is not a query.
	GetByID(ctx context.Context, id string) (*Embedding, error)
	GetByDocID(ctx context.Context, docID string) ([]*Embedding, error)

	// UpsertBatchTx joins a transaction the caller already opened, so a write
	// that spans the vector store and a sibling package's tables is one
	// transaction rather than two that can half-fail.
	UpsertBatchTx(ctx context.Context, tx *sql.Tx, embs []*Embedding) error

	// UpsertBatchWithAdapt writes vectors that may not match the store's
	// dimension, adapting them to it. Separate from UpsertBatch because
	// silently reshaping a vector is a decision, not a detail.
	UpsertBatchWithAdapt(ctx context.Context, embs []*Embedding) error

	// SearchChatHistoryScored is SearchChatHistory with the scores kept.
	SearchChatHistoryScored(ctx context.Context, queryVec []float32, sessionID string, limit int) ([]ScoredMessage, error)

	// The dimension bookkeeping. An embedder swap leaves rows of the old width
	// behind, and a store that cannot find them cannot be repaired — which is
	// how a brain ends up silently unable to search part of itself.
	DimensionReport(ctx context.Context) (*DimensionReport, error)
	MismatchedEmbeddings(ctx context.Context, wantDim, limit int) ([]*Embedding, error)
	ReconcileCollectionDimensions(ctx context.Context, dim int) (int, error)

	// SyncDeletedEmbeddingIDs and SyncUpsertedEmbeddings keep an in-process
	// index in step with rows written by another path. They take no error
	// because an index that has drifted is a performance problem, not a
	// correctness one: the rows are already right.
	SyncDeletedEmbeddingIDs(ctx context.Context, ids []string)
	SyncUpsertedEmbeddings(ctx context.Context, embs []*Embedding)
}

// SQLiteStore is one, which is the claim DB has been relying on all along
// without anywhere to write it down.
var _ BrainStore = (*SQLiteStore)(nil)

// Backupper is a store that can copy itself. It is deliberately not part of
// BrainStore.
//
// BrainStore is the set of methods DB must have to be a brain at all: without
// UpdateDocument or GetDB there is nothing to run. Backup is not like that. A
// brain on PostgreSQL is perfectly complete without it, because backing up
// PostgreSQL is pg_dump or a base backup — a job for the operations team and
// its retention policy, not something this process should imitate by writing a
// file next to a database it does not own. Folding Backup into BrainStore would
// make every backend either implement that pretence or carry a permanent
// "not supported" stub, and the compile error that greets the next backend
// would be asking it for a capability it may have no business having.
//
// So it is optional, and callers type-assert. The cost is that the failure moves
// from compile time to run time; the payment for that is an error that names the
// backend, so an operator reading it learns which one it is and where its
// backups actually come from.
type Backupper interface {
	// Backup writes a consistent copy of the whole store to path. It must not
	// require quiescing writers: a backup that needs the server stopped is a
	// different operational thing, and callers of this one are not stopping it.
	Backup(ctx context.Context, path string) error
}

// SQLiteStore is one. PostgresStore is deliberately not.
var _ Backupper = (*SQLiteStore)(nil)
