package liveview

// The inspector: one record, and everything the shelf can say about it.
//
// This is the join between the picture and the product. Until now a click on a
// node opened a panel with its label, its type, its id and the names of eight
// neighbours — everything the *layout* knew, and nothing the *store* did. The
// same record had a source file, a chunk, a producer, a grade, a validity, the
// text it was drawn from, whatever it was recorded as contradicting, and
// sometimes a decision somebody signed against it, and none of that could be
// reached from the thing on screen that stood for it.
//
// It is a fourth route onto the page, and unlike the other three it is neither
// polled nor pushed nor timed: it is fetched once, when a person asks about one
// record. That is what makes it affordable to answer expensively — a node read,
// an edge read, the supporting chunks and a decision query per click, rather
// than per tick.
//
// Like Contract and Ontology, the hook may be nil, and nil is an answer rather
// than an oversight. A side graph assembled in memory has no chunks and no
// ledger; the shared brain has both and cannot be asked for them through
// graph_list_all. The panel says which, in words, because "this view cannot
// look the record up" and "the record carries nothing" are different findings
// and the second is the one a reader will act on.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// recordChunkLimit caps the supporting text one panel is handed.
//
// A relation extracted from a long document can cite a dozen chunks and each
// is a paragraph. The panel is a column beside a scene it is also covering, and
// a reader who wants the whole document has the source. The cap is on the
// answer, not on the work: ChunkIDs carries every id, so the panel can say how
// many it is not showing.
const recordChunkLimit = 4

// recordDecisionLimit caps the ledger entries shown against one record.
const recordDecisionLimit = 5

// RecordText is one piece of the text a record was drawn from.
type RecordText struct {
	ChunkID    string `json:"chunk_id"`
	DocumentID string `json:"document_id,omitempty"`
	Content    string `json:"content"`
}

// RecordDecision is one ledger entry standing against a record: who decided
// what, when, and why.
//
// A projection of cortexdb.DecisionRecord rather than the type itself, because
// the panel shows six fields and the record has fourteen, and because the two
// that matter here — whether this decision is the one that *verified* the
// record, and what it superseded — are read off the entry rather than carried
// on it.
type RecordDecision struct {
	ID      string `json:"id"`
	Kind    string `json:"kind,omitempty"`
	Actor   string `json:"actor,omitempty"`
	At      string `json:"at,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Note    string `json:"note,omitempty"`
	// Grade is the decision's own grade. A decision recorded as held is not
	// an account of anything yet, and a panel that listed it beside a verified
	// one would say the record had been settled when it had not.
	Grade string `json:"grade,omitempty"`
	// Supersedes is what this decision replaced — ids, and therefore links.
	Supersedes []string `json:"supersedes,omitempty"`
}

// RecordDetail is what the inspector draws.
//
// Available and Reason are the same distinction ContractReport draws and for
// the same reason: a source that cannot be asked and a record that carries
// nothing must not render alike. Found is the third state, and it is not the
// same as either — a node that is on screen but no longer in the store is a
// real finding about a graph being written under the view.
type RecordDetail struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Found     bool   `json:"found"`

	ID      string `json:"id"`
	Edge    bool   `json:"edge,omitempty"`
	Type    string `json:"type,omitempty"`
	Content string `json:"content,omitempty"`
	// From and To are an edge's ends: ids, and therefore links.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`

	// The contract's keys, verbatim. Empty means the record does not carry
	// the key, which stays distinguishable from carrying an empty value only
	// because the store reads them guarded — see graph.RecordsWithProperties.
	Grade    string `json:"grade,omitempty"`
	State    string `json:"state,omitempty"`
	Why      string `json:"why,omitempty"`
	Source   string `json:"source,omitempty"`
	Chunk    string `json:"chunk,omitempty"`
	Producer string `json:"producer,omitempty"`
	At       string `json:"at,omitempty"`
	By       string `json:"by,omitempty"`
	// Confidence is an extraction confidence, shown as detail and never as
	// evidence: contract.go is explicit that a model's confidence in its own
	// output is not evidence about the world.
	Confidence string `json:"confidence,omitempty"`

	// ValidFrom is when the fact became true in the world, RFC 3339, empty
	// when the record carries no validity — which every record written before
	// the temporal release does. It is not _at: _at is when the producer made
	// the record, and the two differ whenever anything is backdated.
	ValidFrom string `json:"valid_from,omitempty"`

	// Contradicts names the records this one cannot both-be-true with. Kept
	// readable rather than resolved into content, because they are ids and
	// therefore links, and because both records are still on the shelf — the
	// disagreement was kept, not deleted.
	Contradicts []string `json:"contradicts,omitempty"`

	// DocumentID and ChunkIDs are where the record came from, and Text is as
	// much of that text as the panel is given. MissingChunks is a citation
	// pointing at text that has since been deleted, which is exactly the sort
	// of thing worth surfacing rather than dropping.
	DocumentID    string       `json:"document_id,omitempty"`
	ChunkIDs      []string     `json:"chunk_ids,omitempty"`
	Text          []RecordText `json:"text,omitempty"`
	MissingChunks []string     `json:"missing_chunks,omitempty"`
	// Inferred says the record was derived rather than read, and Rule names
	// the derivation. The chunks then support the premises, not this record.
	Inferred bool   `json:"inferred,omitempty"`
	Rule     string `json:"rule,omitempty"`

	// Decisions are the ledger entries about this record, newest first. Empty
	// means nobody decided anything about it — which, for a record graded
	// verified, is itself worth seeing.
	Decisions []RecordDecision `json:"decisions,omitempty"`
	// Chain is filled only when the record *is* a decision: the decisions it
	// rests on, so a reader can walk back from an action to the facts under
	// it without leaving the panel.
	Chain []RecordDecision `json:"chain,omitempty"`
	// ChainTruncated says the walk stopped with decisions still unvisited. A
	// chain that quietly stopped reads as a complete account of why something
	// was done, which is the one thing it must never do.
	ChainTruncated bool `json:"chain_truncated,omitempty"`

	// Notes are things this view could not answer about a record it did find:
	// a provenance read that failed, a ledger query that errored. Reported
	// rather than swallowed, and rather than failing the whole panel — the
	// contract keys are already worth showing without them.
	Notes []string `json:"notes,omitempty"`
}

// unavailableRecord is the answer for a view that cannot look records up.
func unavailableRecord(reason string) RecordDetail {
	return RecordDetail{Reason: reason}
}

// notFoundRecord is the answer for a view that looked and found nothing.
//
// Available true, Found false: the source answered, and the answer is that the
// shelf no longer holds this. On a graph being written under the view that is
// a real event, not an error.
func notFoundRecord(id string) RecordDetail {
	return RecordDetail{Available: true, ID: id}
}

// localRecord reads one record straight off an open database.
//
// Four reads at most, and only the first is unconditional: the node, then the
// edge if it was not a node, then its provenance, then the ledger. Everything
// after the first is allowed to fail without failing the panel — a store with
// no chunks and no decisions still has a grade and a source to show, and a
// panel that returned an error instead would hide them.
func localRecord(db *cortexdb.DB) func(context.Context, string) (RecordDetail, error) {
	return func(ctx context.Context, id string) (RecordDetail, error) {
		id = strings.TrimSpace(id)
		if id == "" {
			return RecordDetail{}, fmt.Errorf("record: no id")
		}
		if err := db.Graph().InitGraphSchema(ctx); err != nil {
			return RecordDetail{}, fmt.Errorf("record: %w", err)
		}

		out := RecordDetail{Available: true, ID: id}
		var props map[string]any

		if node, err := db.Graph().GetNode(ctx, id); err == nil && node != nil {
			out.Found = true
			out.Type = node.NodeType
			out.Content = node.Content
			props = node.Properties
			out.ValidFrom = rfc3339OrEmpty(node.ValidFrom)
		} else if edges, eerr := db.Graph().GetEdgesBatch(ctx, []string{id}); eerr == nil && len(edges) == 1 && edges[0] != nil {
			e := edges[0]
			out.Found = true
			out.Edge = true
			out.Type = e.EdgeType
			out.From = e.FromNodeID
			out.To = e.ToNodeID
			props = e.Properties
			out.ValidFrom = rfc3339OrEmpty(e.ValidFrom)
		}
		if !out.Found {
			return notFoundRecord(id), nil
		}

		readContractKeys(&out, props)

		if out.Edge {
			// FactProvenanceFor is edge-shaped on purpose: a fact is a
			// relation, and "says who?" is a question about the relation
			// rather than about either end. withText because the whole point
			// of the panel is to put the words next to the claim.
			prov, err := db.FactProvenanceFor(ctx, id, true)
			switch {
			case err != nil:
				out.Notes = append(out.Notes, "provenance: "+err.Error())
			case prov != nil:
				out.DocumentID = prov.DocumentID
				out.ChunkIDs = prov.ChunkIDs
				out.MissingChunks = prov.Missing
				out.Inferred = prov.Inferred
				out.Rule = prov.Rule
				if out.Source == "" {
					out.Source = prov.Source
				}
				for _, c := range prov.Chunks {
					if len(out.Text) >= recordChunkLimit {
						break
					}
					out.Text = append(out.Text, RecordText{
						ChunkID: c.ID, DocumentID: c.DocumentID, Content: c.Content})
				}
			}
		} else {
			// A node has no provenance API — the citation machinery is about
			// facts — but the ingester writes the same two keys onto entity
			// nodes, so the same question can be answered from its properties.
			out.DocumentID = propText(props, "document_id")
			out.ChunkIDs = propStrings(props, "chunk_ids")
			if out.DocumentID == "" {
				if docs := propStrings(props, "source_document_ids"); len(docs) > 0 {
					out.DocumentID = docs[0]
				}
			}
			loadRecordChunks(ctx, db, &out)
		}

		loadRecordDecisions(ctx, db, &out)
		return out, nil
	}
}

// readContractKeys lifts the contract off a record's properties.
//
// The keys are pkg/cortexdb's constants, not strings written again here: the
// contract is one vocabulary and this is a reader of it, the same way
// GradedRecords is.
func readContractKeys(out *RecordDetail, props map[string]any) {
	out.Grade = propText(props, cortexdb.KeyGrade)
	out.State = propText(props, cortexdb.KeyState)
	out.Why = propText(props, cortexdb.KeyWhy)
	out.Source = propText(props, cortexdb.KeySource)
	out.Chunk = propText(props, cortexdb.KeyChunk)
	out.Producer = propText(props, cortexdb.KeyProducer)
	out.At = propText(props, cortexdb.KeyAt)
	out.By = propText(props, cortexdb.KeyBy)
	out.Confidence = propText(props, cortexdb.KeyConfidence)
	out.Contradicts = propStrings(props, cortexdb.KeyContradicts)
}

// loadRecordChunks fetches a node's supporting text.
func loadRecordChunks(ctx context.Context, db *cortexdb.DB, out *RecordDetail) {
	if len(out.ChunkIDs) == 0 {
		return
	}
	want := out.ChunkIDs
	if len(want) > recordChunkLimit {
		want = want[:recordChunkLimit]
	}
	got, err := db.GraphRAGTools().GetChunks(ctx, cortexdb.ToolGetChunksRequest{
		ChunkIDs: want,
		// The text is the point; the neighbourhood is already on screen.
		DisableGraph: true,
	})
	if err != nil {
		out.Notes = append(out.Notes, "supporting text: "+err.Error())
		return
	}
	found := map[string]bool{}
	for _, c := range got.Chunks {
		found[c.ID] = true
		out.Text = append(out.Text, RecordText{
			ChunkID: c.ID, DocumentID: c.DocumentID, Content: c.Content})
	}
	for _, id := range want {
		if !found[id] {
			out.MissingChunks = append(out.MissingChunks, id)
		}
	}
}

// loadRecordDecisions answers "and what did anybody decide about this".
//
// Precedents keyed on the subject rather than a query of its own: a decision
// names what it is about in its own `subject` property, and Precedents is the
// call that reads that property back. It is spelled "precedents" because the
// question it was written for was "what was decided this way before", and this
// is the same question narrowed to one subject — asking it here rather than
// writing a second walk is what keeps the ledger with one reader.
//
// A record that is itself a decision gets its chain instead of a search for
// decisions about it, because a decision's account of itself is what it rests
// on, and DecisionChain is the walk that gives it.
func loadRecordDecisions(ctx context.Context, db *cortexdb.DB, out *RecordDetail) {
	if out.Type == cortexdb.DecisionNodeType {
		chain, err := db.DecisionChain(ctx, out.ID, 0)
		if err != nil {
			out.Notes = append(out.Notes, "decision chain: "+err.Error())
			return
		}
		out.ChainTruncated = chain.Truncated
		for i, d := range chain.Decisions {
			// The root is the record itself; the panel is already showing it.
			if i == 0 && cortexdb.DecisionID(d.ID) == cortexdb.DecisionID(out.ID) {
				continue
			}
			if len(out.Chain) >= recordDecisionLimit {
				out.ChainTruncated = true
				break
			}
			out.Chain = append(out.Chain, recordDecisionFrom(d))
		}
		return
	}

	// An edge is never a decision's subject — RecordDecision refuses a subject
	// that is not a node — so asking would be a scan that can only come back
	// empty. Saying nothing is the honest answer, and the panel says it.
	if out.Edge {
		return
	}
	decisions, err := db.Precedents(ctx, cortexdb.PrecedentsQuery{
		Subject: out.ID,
		Limit:   recordDecisionLimit,
	})
	if err != nil {
		out.Notes = append(out.Notes, "decisions: "+err.Error())
		return
	}
	for _, d := range decisions {
		out.Decisions = append(out.Decisions, recordDecisionFrom(d))
	}
}

func recordDecisionFrom(d cortexdb.DecisionRecord) RecordDecision {
	return RecordDecision{
		ID: d.ID, Kind: d.Kind, Actor: d.Actor, At: d.At,
		Verdict: d.Verdict, Note: d.Note, Grade: d.Grade,
		Supersedes: d.Supersedes,
	}
}

// propText reads one property as text.
//
// A property map is map[string]any because that is what the JSON column
// decodes to, and the contract's values are strings — but a producer that
// wrote a number or a boolean has still written something a reader must see
// rather than have silently dropped. _chunk in particular is documented as an
// index and is routinely written as a number.
func propText(props map[string]any, key string) string {
	if props == nil {
		return ""
	}
	v, ok := props[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case float64:
		// Whole numbers print as whole numbers: a chunk index of 3 must not
		// arrive at the panel as "3e+00".
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		return ""
	}
}

// propStrings reads one property as a list of ids.
//
// _contradicts is documented as a JSON array, and the store round-trips
// properties through encoding/json, so it arrives as []any of strings. It is
// also written as a comma-joined string by at least one producer's convenience
// path, so both are accepted — a disagreement that cannot be read is a
// disagreement that was deleted, which is the one thing the contract says must
// not happen to it.
func propStrings(props map[string]any, key string) []string {
	if props == nil {
		return nil
	}
	switch t := props[key].(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []string:
		return t
	case string:
		var out []string
		for _, part := range strings.Split(t, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

// rfc3339OrEmpty renders a validity instant, or nothing for a record that
// carries none — which is every record written before the temporal release,
// and is read as "always been true" rather than as a missing field.
func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
