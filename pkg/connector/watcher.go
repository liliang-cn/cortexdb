package connector

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// WatcherOptions configures a Watcher.
type WatcherOptions struct {
	// SourceKey is the stable checkpoint key for this (source, tableset).
	SourceKey string
	// Desensitizer applies the signed MaskingPlan. Required.
	Desensitizer *Desensitizer
	// Mapping routes rows to RAG/KG. Required; every RAG table MUST set IDColumn
	// and KG delete targets MUST key on the primary key (validated).
	Mapping importflow.MappingPlan
	// Checkpoint persists resume position. Required.
	Checkpoint CheckpointStore
	// Columns gives each table's column list for the one-row import source. If a
	// table is absent, columns are inferred from the event row's value keys.
	Columns map[string][]importflow.Column
}

// Watcher consumes ChangeEvents and keeps RAG + KG in sync with the source.
type Watcher struct {
	db   *cortexdb.DB
	src  ChangeSource
	opts WatcherOptions
	im   *importflow.Importer
}

// NewWatcher validates options (incl. the stable-key precondition) and returns a
// Watcher. It does not start consuming until Run.
func NewWatcher(db *cortexdb.DB, src ChangeSource, opts WatcherOptions) (*Watcher, error) {
	if opts.Desensitizer == nil {
		return nil, fmt.Errorf("connector: watcher requires a Desensitizer")
	}
	if opts.Checkpoint == nil {
		return nil, fmt.Errorf("connector: watcher requires a CheckpointStore")
	}
	if opts.SourceKey == "" {
		return nil, fmt.Errorf("connector: watcher requires a SourceKey")
	}
	for table, tp := range opts.Mapping.Tables {
		if tp.RAG != nil && tp.RAG.IDColumn == "" {
			return nil, fmt.Errorf("connector: table %q routes to RAG without RAGPlan.IDColumn; CDC needs a stable key", table)
		}
	}
	return &Watcher{db: db, src: src, opts: opts, im: importflow.New(db)}, nil
}

// Run consumes changes until the source's Changes returns (polling: one batch;
// log-based: until ctx is cancelled), applying each event.
func (w *Watcher) Run(ctx context.Context) error {
	return w.src.Changes(ctx, func(e ChangeEvent) error {
		return w.apply(ctx, e)
	})
}

func (w *Watcher) apply(ctx context.Context, e ChangeEvent) error {
	switch e.Op {
	case OpInsert, OpUpdate:
		return w.applyUpsert(ctx, e)
	case OpDelete:
		return w.applyDelete(ctx, e)
	default:
		return fmt.Errorf("connector: unknown change op %d", e.Op)
	}
}

// applyUpsert desensitizes the row and runs it through importflow as a one-row
// source, which upserts by KnowledgeID (RAGPlan.IDColumn) and idempotent IRIs.
func (w *Watcher) applyUpsert(ctx context.Context, e ChangeEvent) error {
	cols := w.opts.Columns[e.Table]
	if cols == nil {
		cols = columnsFromRecord(e.Row)
	}
	src := Desensitized(singleRowSource(e.Table, cols, e.Row), w.opts.Desensitizer)
	_, err := w.im.Run(ctx, src, w.opts.Mapping)
	return err
}

// applyDelete removes the row's RAG chunk and entity triples. The key is
// desensitized first so a pseudonymized key resolves to the same token that was
// indexed.
func (w *Watcher) applyDelete(ctx context.Context, e ChangeEvent) error {
	dkey, err := w.desensitizeKey(ctx, e.Table, e.Key)
	if err != nil {
		return err
	}
	tp := w.opts.Mapping.Tables[e.Table]

	if tp.RAG != nil {
		if id := ragChunkID(e.Table, tp.RAG, dkey); id != "" {
			if _, err := w.db.DeleteKnowledge(ctx, cortexdb.KnowledgeDeleteRequest{KnowledgeID: id}); err != nil {
				return err
			}
		}
	}
	if tp.KG != nil {
		for _, iri := range entityIRIsForKey(tp.KG, dkey) {
			if err := w.deleteEntity(ctx, iri); err != nil {
				return err
			}
		}
	}
	return nil
}

// deleteEntity removes all triples where iri is subject (type/label/props/outgoing
// relations) and where it is object (incoming relations), leaving no dangling edge.
func (w *Watcher) deleteEntity(ctx context.Context, iri string) error {
	subj := graph.NewIRI(iri)
	if _, err := w.db.DeleteKnowledgeGraph(ctx, cortexdb.KnowledgeGraphDeleteRequest{
		Pattern: &cortexdb.KnowledgeGraphTriplePattern{Subject: &subj},
	}); err != nil {
		return err
	}
	obj := graph.NewIRI(iri)
	if _, err := w.db.DeleteKnowledgeGraph(ctx, cortexdb.KnowledgeGraphDeleteRequest{
		Pattern: &cortexdb.KnowledgeGraphTriplePattern{Object: &obj},
	}); err != nil {
		return err
	}
	return nil
}

// desensitizeKey runs the key columns through the desensitizer so reversible keys
// become the same token used at index time.
func (w *Watcher) desensitizeKey(ctx context.Context, table string, key map[string]string) (map[string]string, error) {
	rec := importflow.Record{Table: table, Values: map[string]string{}, Nulls: map[string]bool{}}
	for k, v := range key {
		rec.Values[k] = v
	}
	out, err := w.opts.Desensitizer.Apply(ctx, rec)
	if err != nil {
		return nil, err
	}
	return out.Values, nil
}

func columnsFromRecord(r importflow.Record) []importflow.Column {
	cols := make([]importflow.Column, 0, len(r.Values))
	for name := range r.Values {
		cols = append(cols, importflow.Column{Name: name})
	}
	return cols
}
