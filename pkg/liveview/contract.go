package liveview

// Reading the knowledge contract for the live view.
//
// pkg/cortexdb/contract.go says what a producer writes onto a record so the
// record can say how it is known; contract_query.go reads it back. This is the
// third route onto the page, and it is deliberately unlike the other two.
//
// Structure is polled and diffed because the graph moves under the view.
// Activity is pushed because a query changes nothing and no poll can find one.
// The contract is neither: it is two GROUP BYs over the whole store and one
// filtered read, and what it counts changes when a person clears a held record
// or a producer runs — human time, not frame time. So it is fetched by the
// panel on its own slow timer through its own endpoint, and never enters the
// hub. Folding it into the two-second structure poll would multiply the store's
// read load by three for a number nobody watches tick.
//
// The other reason it is separate: the scene draws at most the six hundred
// most-connected nodes, and this counts everything. A tally and a node count
// that disagree by two orders of magnitude are not a bug, and the report
// carries the store's own totals so the panel can say which it is showing.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// contractAttentionLimit caps the held-and-refused list the panel is sent.
//
// A panel is a few hundred pixels of a scene it is also covering, and nobody
// works through nine hundred records in one. The cap is on the answer, not on
// the work: the report carries the true total, so a reader is told what is
// waiting rather than shown a list that quietly stops.
const contractAttentionLimit = 25

// ContractInterval is how often the panel re-reads the contract.
//
// Two orders of magnitude slower than the structure poll, because it is two
// aggregate scans rather than a keyed read, and because the thing it measures
// moves on the timescale of somebody reviewing a record. A tally fifteen
// seconds out of date has never been the wrong answer to "how much of this
// stands on what".
const ContractInterval = 15 * time.Second

// Row kinds. Three, because untagged and unknown are not the same finding and
// the page must not have to work that out from a grade string it would then be
// keeping the contract's vocabulary in.
const (
	// ContractGraded is one of the contract's five closed values.
	ContractGraded = "graded"
	// ContractUntagged is every record carrying no _grade at all: a producer
	// that wrote nothing. On a shelf older than the contract this is the
	// largest number in the result.
	ContractUntagged = "untagged"
	// ContractUnknown is a _grade the contract does not define: a producer
	// writing something wrong. It is somebody's bug, and folding it in with
	// untagged would hide the one row a maintainer has to act on.
	ContractUnknown = "unknown"
)

// ContractRow is one line of the tally.
//
// Nodes and Edges stay apart rather than summed, for the reason
// graph.PropertyCount gives: an edge is an assertion about two things and a
// node is one thing, and a reader told "40 records" cannot tell a graph of 4
// nodes and 36 edges from its reverse.
type ContractRow struct {
	// Grade is the contract value, verbatim — empty for the untagged row, and
	// for an unknown row whatever the producer actually wrote, so the
	// maintainer can go and find it.
	Grade string `json:"grade"`
	Kind  string `json:"kind"`
	Nodes int    `json:"nodes"`
	Edges int    `json:"edges"`
}

// Total is the row's records, for scaling a bar against its neighbours.
func (r ContractRow) Total() int { return r.Nodes + r.Edges }

// ContractReport is what the panel draws: how much of the store stands on
// what, and what on it is waiting for a person.
//
// Available and Reason exist because "this view cannot read the contract" is a
// different answer from "nothing here is graded", and a panel that rendered
// both as an empty chart would be lying about the more common one. A side
// graph, or a brain too old to answer, sets Available false and says so.
type ContractReport struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`

	// Rows is the tally in display order: the contract's five, best-
	// established first, then untagged, then any unknown value. Ordering here
	// rather than in the page keeps the contract's vocabulary in Go, where it
	// is already written down once.
	//
	// When nothing at all carries a grade the five are omitted entirely and
	// only the untagged row survives. That is the honest shape for the state
	// a real machine is most likely in today: five empty bars describe a
	// measurement that was taken, and none was.
	Rows []ContractRow `json:"rows"`

	// Graded and Untagged are records, not rows: graded counts everything
	// carrying any _grade, an unrecognised one included.
	Graded   int `json:"graded"`
	Untagged int `json:"untagged"`
	// Nodes and Edges are the store's own totals, so the panel can say that it
	// counted the whole shelf and not the six hundred nodes on screen.
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`

	// Attention is held and refused together, each with the reason its
	// producer gave. Told apart by Grade, not split into two lists — a reader
	// working through a shelf wants both, and two lists makes it likely only
	// one gets rendered.
	Attention []cortexdb.GradedRecord `json:"attention"`
	Truncated bool                    `json:"truncated,omitempty"`
	// Total is everything held or refused, whether or not it is in Attention.
	Total int `json:"total"`

	// At is when this was read, so the panel can show its own staleness rather
	// than leave a slow number looking like a stuck one.
	At int64 `json:"at"`
}

// contractGradeOrder is the tally's reading order: what is established by
// something outside the producer first, down to what the producer refused.
// It is the contract's own ladder of standing, and the panel reads top to
// bottom.
var contractGradeOrder = []string{
	cortexdb.GradeVerified,
	cortexdb.GradeSelfConsistent,
	cortexdb.GradeAsserted,
	cortexdb.GradeHeld,
	cortexdb.GradeRefused,
}

// buildContractReport shapes a tally and a needs-attention answer into what
// the panel draws.
func buildContractReport(t cortexdb.ContractTally, att cortexdb.NeedsAttentionResponse) ContractReport {
	graded := map[string]ContractRow{
		cortexdb.GradeVerified:       {Grade: cortexdb.GradeVerified, Kind: ContractGraded, Nodes: t.Verified.Nodes, Edges: t.Verified.Edges},
		cortexdb.GradeSelfConsistent: {Grade: cortexdb.GradeSelfConsistent, Kind: ContractGraded, Nodes: t.SelfConsistent.Nodes, Edges: t.SelfConsistent.Edges},
		cortexdb.GradeAsserted:       {Grade: cortexdb.GradeAsserted, Kind: ContractGraded, Nodes: t.Asserted.Nodes, Edges: t.Asserted.Edges},
		cortexdb.GradeHeld:           {Grade: cortexdb.GradeHeld, Kind: ContractGraded, Nodes: t.Held.Nodes, Edges: t.Held.Edges},
		cortexdb.GradeRefused:        {Grade: cortexdb.GradeRefused, Kind: ContractGraded, Nodes: t.Refused.Nodes, Edges: t.Refused.Edges},
	}

	rep := ContractReport{
		Available: true,
		Untagged:  t.Untagged.Nodes + t.Untagged.Edges,
		Attention: att.Records,
		Truncated: att.Truncated,
		Total:     att.Total,
		At:        time.Now().UnixMilli(),
	}
	if rep.Attention == nil {
		rep.Attention = []cortexdb.GradedRecord{}
	}

	// Unknown values are sorted so the same store always draws the same panel;
	// a row that moves between refreshes reads as a change in the data.
	unknown := make([]string, 0, len(t.Unknown))
	for value := range t.Unknown {
		unknown = append(unknown, value)
	}
	sort.Strings(unknown)

	for _, g := range contractGradeOrder {
		rep.Graded += graded[g].Total()
	}
	for _, value := range unknown {
		rep.Graded += t.Unknown[value].Nodes + t.Unknown[value].Edges
	}

	// The five appear only once something has been graded. Drawing them at
	// zero over a shelf nobody has stamped says a measurement was taken and
	// came back empty, which is a stronger claim than the truth.
	if rep.Graded > 0 {
		for _, g := range contractGradeOrder {
			rep.Rows = append(rep.Rows, graded[g])
		}
	}
	if rep.Untagged > 0 {
		rep.Rows = append(rep.Rows, ContractRow{Kind: ContractUntagged, Nodes: t.Untagged.Nodes, Edges: t.Untagged.Edges})
	}
	// Last and marked: an unrecognised grade is a producer bug, and it belongs
	// where the eye lands after it has read the scale, not hidden among values
	// the contract does define.
	for _, value := range unknown {
		c := t.Unknown[value]
		rep.Rows = append(rep.Rows, ContractRow{Grade: value, Kind: ContractUnknown, Nodes: c.Nodes, Edges: c.Edges})
	}
	if rep.Rows == nil {
		rep.Rows = []ContractRow{}
	}

	for _, r := range rep.Rows {
		rep.Nodes += r.Nodes
		rep.Edges += r.Edges
	}
	return rep
}

// unavailableContract is the report for a view that cannot answer.
//
// It is a report rather than an HTTP error because the panel has something
// true to say either way, and a fetch that fails leaves it showing whatever it
// drew last — which after a source change is the previous graph's numbers.
func unavailableContract(reason string) ContractReport {
	return ContractReport{
		Reason:    reason,
		Rows:      []ContractRow{},
		Attention: []cortexdb.GradedRecord{},
		At:        time.Now().UnixMilli(),
	}
}

// localContract reads the contract straight off an open database.
func localContract(db *cortexdb.DB) func(context.Context) (ContractReport, error) {
	return func(ctx context.Context) (ContractReport, error) {
		tally, err := db.ContractTally(ctx)
		if err != nil {
			return ContractReport{}, fmt.Errorf("contract tally: %w", err)
		}
		att, err := db.NeedsAttentionTool(ctx, cortexdb.NeedsAttentionRequest{Limit: contractAttentionLimit})
		if err != nil {
			return ContractReport{}, fmt.Errorf("needs attention: %w", err)
		}
		return buildContractReport(tally, att), nil
	}
}

// remoteContract reads the contract off a shared brain.
//
// Through CallTool, the same door LoadRemote already uses, rather than the
// ContractService RPC: one route to a shared brain is one route to keep
// working, and a server old enough to lack the tools fails with a message
// naming them instead of a transport error the panel would have to translate.
func remoteContract(addr, token string) func(context.Context) (ContractReport, error) {
	return func(ctx context.Context) (ContractReport, error) {
		conn, err := dial(addr, token)
		if err != nil {
			return ContractReport{}, fmt.Errorf("connect to %s: %w", addr, err)
		}
		defer func() { _ = conn.Close() }()

		callCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		defer cancel()
		client := rpcv1.NewToolsServiceClient(conn)

		call := func(name string, args any, out any) error {
			body, merr := json.Marshal(args)
			if merr != nil {
				return merr
			}
			resp, cerr := client.CallTool(callCtx, &rpcv1.CallToolRequest{Name: name, ArgsJson: string(body)})
			if cerr != nil {
				return remoteToolErr(name, addr, cerr)
			}
			if uerr := json.Unmarshal([]byte(resp.GetResultJson()), out); uerr != nil {
				return fmt.Errorf("decode %s: %w", name, uerr)
			}
			return nil
		}

		var tally cortexdb.ContractTally
		if err := call("contract_tally", cortexdb.ContractTallyRequest{}, &tally); err != nil {
			return ContractReport{}, err
		}
		var att cortexdb.NeedsAttentionResponse
		if err := call("contract_needs_attention", cortexdb.NeedsAttentionRequest{Limit: contractAttentionLimit}, &att); err != nil {
			return ContractReport{}, err
		}
		return buildContractReport(tally, att), nil
	}
}
