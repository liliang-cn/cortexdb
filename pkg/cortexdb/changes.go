package cortexdb

// The change feed and automatic inference maintenance, wired into DB.
//
// Both are on by default. The feed because it is only useful if it was on
// when the change happened: a consumer that turns up next month and finds the
// log was never written has nothing to resume from. Inference maintenance
// because the alternative default is wrong answers: every write between two
// manual refreshes leaves inferred triples that SPARQL reads back as facts,
// and nobody who has not read the refresh API knows to call it. Its cost when
// there is nothing to infer — the shared brain declares no schema — is one
// indexed read of the log per batch of writes and no state; see
// pkg/graph/inference_maintain.go.
//
// The plumbing lives in pkg/graph (changefeed.go, inference_maintain.go,
// inference_dred.go). This file is configuration, lifecycle, and the
// changes_since tool.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ChangeFeedOptions configures the change feed.
type ChangeFeedOptions struct {
	// Disabled leaves the feed uninstalled by this process. Triggers another
	// process installed stay in place; remove them with
	// Graph().DisableChangeFeed. Disabling the feed also disables automatic
	// inference, which reads it.
	Disabled bool
	// Retention bounds the log. The zero value is
	// graph.DefaultChangeFeedRetention: seven days or 500,000 events.
	Retention graph.ChangeFeedRetention
	// PruneInterval is how often retention is applied. Default one hour.
	PruneInterval time.Duration
}

// AutoInferenceOptions configures automatic inference maintenance.
type AutoInferenceOptions struct {
	// Disabled turns maintenance off in this process. Inferred triples then
	// change only through RefreshKnowledgeGraphInference, as before, unless
	// another process sharing the database maintains them.
	Disabled bool
	// Config tunes the maintainer; the zero value is its default.
	Config graph.InferenceMaintenanceConfig
}

// WithChangeFeed configures the change feed.
func WithChangeFeed(opts ChangeFeedOptions) Option {
	return func(db *DB) { db.changes.feed = opts }
}

// WithAutoInference turns automatic inference maintenance on or off.
func WithAutoInference(enabled bool) Option {
	return func(db *DB) { db.changes.inference.Disabled = !enabled }
}

// WithAutoInferenceOptions configures automatic inference maintenance.
func WithAutoInferenceOptions(opts AutoInferenceOptions) Option {
	return func(db *DB) { db.changes.inference = opts }
}

// changeRuntime is the per-DB state of the feed and the maintainer.
type changeRuntime struct {
	feed       ChangeFeedOptions
	inference  AutoInferenceOptions
	cancel     context.CancelFunc
	janitor    chan struct{}
	maintainer *graph.InferenceMaintainer
}

// startChangeRuntime installs the feed and starts the janitor and the
// maintainer. Called once by Open, after options are applied.
func (db *DB) startChangeRuntime(ctx context.Context) error {
	if db.changes.feed.Disabled {
		return nil
	}
	// The ontology table is otherwise created on first use, after the feed
	// would have looked for it; created here, its writes are logged from the
	// first one.
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return fmt.Errorf("ontology table: %w", err)
	}
	if err := db.graph.EnsureChangeFeed(ctx); err != nil {
		return err
	}
	if isMemoryDSN(db.store.Config().Path) {
		// An in-memory SQLite database exists once per connection, and the
		// pool opens a new connection for whichever goroutine finds the last
		// one busy. A background worker would therefore read and write an
		// empty database of its own — and, worse, leave behind a second
		// connection the caller's next query may land on. The feed is
		// installed (on the connection the caller uses) and readable; pruning
		// and inference maintenance are left to explicit calls.
		return nil
	}
	bg, cancel := context.WithCancel(context.Background())
	db.changes.cancel = cancel
	retention := db.changes.feed.Retention
	if retention == (graph.ChangeFeedRetention{}) {
		retention = graph.DefaultChangeFeedRetention
	}
	interval := db.changes.feed.PruneInterval
	if interval <= 0 {
		interval = time.Hour
	}
	db.changes.janitor = make(chan struct{})
	go func() {
		defer close(db.changes.janitor)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if _, err := db.graph.PruneChanges(bg, retention); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("cortexdb: prune change log: %v", err)
			}
			select {
			case <-bg.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	if db.changes.inference.Disabled {
		return nil
	}
	m, err := db.graph.StartInferenceMaintenance(bg, db.changes.inference.Config)
	if err != nil {
		cancel()
		<-db.changes.janitor
		return fmt.Errorf("start inference maintenance: %w", err)
	}
	db.changes.maintainer = m
	return nil
}

func isMemoryDSN(path string) bool {
	return strings.Contains(path, ":memory:") || strings.Contains(path, "mode=memory")
}

func (db *DB) stopChangeRuntime() {
	if db.changes.maintainer != nil {
		_ = db.changes.maintainer.Close()
		db.changes.maintainer = nil
	}
	if db.changes.cancel != nil {
		db.changes.cancel()
		<-db.changes.janitor
		db.changes.cancel = nil
	}
}

// withInferencePaused runs a hand-made refresh with this process's maintainer
// paused, and has the maintainer rebuild from the refreshed store with the
// refresh's options afterwards, so the two neither race nor disagree.
func (db *DB) withInferencePaused(opts graph.InferenceOptions, fn func() error) error {
	if db.changes.maintainer == nil {
		return fn()
	}
	return db.changes.maintainer.Exclusive(opts, fn)
}

// Changes returns up to limit events committed after the cursor, in commit
// order. See graph.GraphStore.Changes.
func (db *DB) Changes(ctx context.Context, after int64, limit int) ([]graph.ChangeEvent, error) {
	return db.graph.Changes(ctx, after, limit)
}

// ChangesHead is the seq of the newest committed event.
func (db *DB) ChangesHead(ctx context.Context) (int64, error) {
	return db.graph.ChangesHead(ctx)
}

// SubscribeChanges delivers every event committed after the cursor to handle,
// in commit order, until ctx ends or handle fails. See
// graph.GraphStore.SubscribeChanges.
func (db *DB) SubscribeChanges(ctx context.Context, after int64, handle func([]graph.ChangeEvent) error) error {
	return db.graph.SubscribeChanges(ctx, after, graph.SubscribeOptions{}, handle)
}

// PruneChanges applies the configured retention now.
func (db *DB) PruneChanges(ctx context.Context) (*graph.PruneChangesReport, error) {
	retention := db.changes.feed.Retention
	if retention == (graph.ChangeFeedRetention{}) {
		retention = graph.DefaultChangeFeedRetention
	}
	return db.graph.PruneChanges(ctx, retention)
}

// WaitForInference blocks until inferred triples reflect every write committed
// before the call, or ctx ends. Use it after a write whose consequences the
// next read must see.
func (db *DB) WaitForInference(ctx context.Context) error {
	return db.graph.WaitForInference(ctx, 0)
}

// InferenceAppliedSeq is the change-log seq inferred triples are current to.
func (db *DB) InferenceAppliedSeq(ctx context.Context) (int64, error) {
	return db.graph.InferenceAppliedSeq(ctx)
}

// InferenceMaintenanceStats reports this process's maintainer, and false if
// none runs here.
func (db *DB) InferenceMaintenanceStats() (graph.InferenceMaintenanceStats, bool) {
	if db.changes.maintainer == nil {
		return graph.InferenceMaintenanceStats{}, false
	}
	return db.changes.maintainer.Stats(), true
}

// InferenceInconsistencies is the OWL 2 RL contradiction report of the
// automatically maintained inferences, when this process maintains them.
func (db *DB) InferenceInconsistencies() []graph.InferenceInconsistency {
	if db.changes.maintainer == nil {
		return nil
	}
	return db.changes.maintainer.Inconsistencies()
}

// --- changes_since ---------------------------------------------------------

// ChangesSinceRequest pages through the change log.
type ChangesSinceRequest struct {
	// After is the cursor: the seq of the last event already handled. 0
	// starts at the oldest retained event; a negative value starts at the
	// head, returning nothing now and a cursor for what comes next.
	After int64 `json:"after"`
	// Limit caps the events scanned for this page. Default 100, at most 1000.
	Limit int `json:"limit,omitempty"`
	// Kinds keeps only these kinds (node, edge, triple, memory, knowledge,
	// ontology_schema). The cursor still advances past the rest.
	Kinds []string `json:"kinds,omitempty"`
	// Before reads backwards: the newest events with a seq below it, newest
	// first, with After as an exclusive lower bound. A negative value starts at
	// the head, the way a negative After does, which is how a reader that does
	// not know the head yet asks for the newest page. Kinds is applied in the
	// query rather than after the read, and NextCursor is then the Before for
	// the next, older page (the oldest seq returned). A cursor older than
	// retention is not an error here: what was pruned is not listed.
	Before int64 `json:"before,omitempty"`
	// Explain adds what the history tables say about each node and edge event
	// (superseded_by, replaces, the schema it was decided under) and Labels for
	// the nodes they name, in a fixed number of queries per page. See
	// graph.ChangesQuery.
	Explain bool `json:"explain,omitempty"`
}

// ChangesSinceResponse is one page of the change log.
type ChangesSinceResponse struct {
	Events []graph.ChangeEvent `json:"events"`
	// NextCursor is the After to pass for the next page.
	NextCursor int64 `json:"next_cursor"`
	// Head is the newest committed seq; More says whether the page stopped
	// short of it.
	Head int64 `json:"head"`
	More bool  `json:"more"`
	// Pruned is set when After is older than retention kept. The events in
	// between are gone; resume from PrunedThrough after rebuilding whatever
	// was derived from them.
	Pruned        bool  `json:"pruned,omitempty"`
	PrunedThrough int64 `json:"pruned_through,omitempty"`
	// InferenceAppliedSeq is how far inferred triples have caught up.
	InferenceAppliedSeq int64 `json:"inference_applied_seq"`
	// Labels names the nodes the events refer to, by id, when Explain was set.
	Labels map[string]string `json:"labels,omitempty"`
}

// ChangesSince answers changes_since.
func (db *DB) ChangesSince(ctx context.Context, req ChangesSinceRequest) (*ChangesSinceResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	head, err := db.graph.ChangesHead(ctx)
	if err != nil {
		return nil, err
	}
	applied, err := db.graph.InferenceAppliedSeq(ctx)
	if err != nil {
		return nil, err
	}
	resp := &ChangesSinceResponse{Events: []graph.ChangeEvent{}, Head: head, InferenceAppliedSeq: applied}
	after := req.After
	if req.Before != 0 {
		// Newest first. A negative After here is not "start at the head" but
		// nothing worth reading below; read from the beginning.
		page, err := db.graph.ReadChanges(ctx, graph.ChangesQuery{
			After: max(after, 0), Before: req.Before, Limit: limit, Kinds: req.Kinds, Explain: req.Explain,
		})
		if err != nil {
			return nil, err
		}
		resp.Events, resp.More, resp.PrunedThrough, resp.Labels = page.Events, page.More, page.PrunedThrough, page.Labels
		if resp.Events == nil {
			resp.Events = []graph.ChangeEvent{}
		}
		resp.NextCursor = req.Before
		if n := len(resp.Events); n > 0 {
			resp.NextCursor = resp.Events[n-1].Seq
		} else if req.Before < 0 {
			resp.NextCursor = head + 1
		}
		return resp, nil
	}
	if after < 0 {
		resp.NextCursor = head
		return resp, nil
	}
	page, err := db.graph.ReadChanges(ctx, graph.ChangesQuery{After: after, Limit: limit, Kinds: req.Kinds, Explain: req.Explain})
	var pruned *graph.ChangesPrunedError
	if errors.As(err, &pruned) {
		resp.Pruned, resp.PrunedThrough, resp.NextCursor = true, pruned.PrunedThrough, pruned.PrunedThrough
		resp.More = pruned.PrunedThrough < head
		return resp, nil
	}
	if err != nil {
		return nil, err
	}
	resp.NextCursor = after
	if page.Scanned > 0 {
		resp.NextCursor = page.LastScanned
	}
	resp.More = page.Scanned == limit && resp.NextCursor < head
	resp.Events, resp.Labels = page.Events, page.Labels
	if resp.Events == nil {
		resp.Events = []graph.ChangeEvent{}
	}
	return resp, nil
}

func changeToolDefinitions() []ToolDefinition {
	return []ToolDefinition{{
		Name: "changes_since",
		Description: "Read the brain's change log: every committed write to graph nodes and edges, RDF triples (explicit and inferred), " +
			"memories, knowledge documents and ontology schemas, in commit order, one event per changed row with a before/after summary, " +
			"op (insert, update, delete, supersede, merge) and, where known, the reason and producer. Pass after=0 for the oldest retained " +
			"event, or the next_cursor of the previous page to continue; a cursor never skips an event and never repeats one. " +
			"inference_applied_seq says how far inferred triples have caught up with the log. Reads only.",
		InputSchema: toolObjectSchema(nil, map[string]any{
			"after":   toolIntegerSchema("Cursor: the seq of the last event already handled. 0 = oldest retained; negative = start at the head."),
			"limit":   toolIntegerSchema("Events to scan for this page (default 100, max 1000)."),
			"kinds":   toolStringArraySchema("Only these kinds: node, edge, triple, memory, knowledge, ontology_schema. The cursor still advances past the rest."),
			"before":  toolIntegerSchema("Read backwards: the newest events with a seq below this, newest first (negative = from the head). after is then a lower bound and next_cursor is the before for the next older page."),
			"explain": map[string]any{"type": "boolean", "description": "Add superseded_by, replaces and schema_id/schema_version to node and edge events, and labels for the nodes they name."},
		}),
	}}
}

func (t *GraphRAGToolbox) callChangeTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	if name != "changes_since" {
		return nil, false, nil
	}
	var req ChangesSinceRequest
	if len(input) > 0 {
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
	}
	resp, err := t.db.ChangesSince(ctx, req)
	return resp, true, err
}

// addChangeMCPTools registers changes_since beside its definition, for the
// reason addTemporalMCPTools gives.
func addChangeMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, db *DB) {
	addGraphRAGMCPTool(server, definitions["changes_since"], func(ctx context.Context, req ChangesSinceRequest) (changesSinceMCPResponse, error) {
		resp, err := db.ChangesSince(ctx, req)
		if err != nil {
			return changesSinceMCPResponse{}, err
		}
		return newChangesSinceMCPResponse(resp), nil
	})
}

// changeEventMCP is graph.ChangeEvent as the MCP tool returns it. Before and
// After are json.RawMessage there, which the inferred output schema reads as
// a byte array ("null or array"), so every event with a row summary failed
// output validation. Typed as any, they carry the same raw JSON and get an
// open schema.
type changeEventMCP struct {
	Seq      int64     `json:"seq"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Op       string    `json:"op"`
	ID       string    `json:"id"`
	Reason   string    `json:"reason,omitempty"`
	Producer string    `json:"producer,omitempty"`
	Before   any       `json:"before,omitempty"`
	After    any       `json:"after,omitempty"`

	SupersededBy  *graph.ChangeRef `json:"superseded_by,omitempty"`
	Replaces      *graph.ChangeRef `json:"replaces,omitempty"`
	SchemaID      string           `json:"schema_id,omitempty"`
	SchemaVersion int              `json:"schema_version,omitempty"`
}

// changesSinceMCPResponse is ChangesSinceResponse with changeEventMCP events.
type changesSinceMCPResponse struct {
	Events              []changeEventMCP  `json:"events"`
	NextCursor          int64             `json:"next_cursor"`
	Head                int64             `json:"head"`
	More                bool              `json:"more"`
	Pruned              bool              `json:"pruned,omitempty"`
	PrunedThrough       int64             `json:"pruned_through,omitempty"`
	InferenceAppliedSeq int64             `json:"inference_applied_seq"`
	Labels              map[string]string `json:"labels,omitempty"`
}

func newChangesSinceMCPResponse(resp *ChangesSinceResponse) changesSinceMCPResponse {
	out := changesSinceMCPResponse{
		Events:              make([]changeEventMCP, 0, len(resp.Events)),
		NextCursor:          resp.NextCursor,
		Head:                resp.Head,
		More:                resp.More,
		Pruned:              resp.Pruned,
		PrunedThrough:       resp.PrunedThrough,
		InferenceAppliedSeq: resp.InferenceAppliedSeq,
		Labels:              resp.Labels,
	}
	for _, ev := range resp.Events {
		wire := changeEventMCP{
			Seq: ev.Seq, At: ev.At, Kind: ev.Kind, Op: ev.Op, ID: ev.ID,
			Reason: ev.Reason, Producer: ev.Producer,
			SupersededBy: ev.SupersededBy, Replaces: ev.Replaces,
			SchemaID: ev.SchemaID, SchemaVersion: ev.SchemaVersion,
		}
		if len(ev.Before) > 0 {
			wire.Before = ev.Before
		}
		if len(ev.After) > 0 {
			wire.After = ev.After
		}
		out.Events = append(out.Events, wire)
	}
	return out
}
