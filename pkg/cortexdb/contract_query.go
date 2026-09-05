package cortexdb

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Reading the knowledge contract back off the shelf.
//
// contract.go says what a producer must write for a record to be trusted and
// explained. Nothing read it. A contract only producers honour is a contract
// nobody can check: the keys go in, and the first time anybody looks at them
// is when a person opens a database browser.
//
// These are the two questions a reader actually asks, and the reason they are
// here rather than in each reader is that the answers must not differ between
// them. An agent recalling across collections and a person looking at a wall
// are the same query with different rendering; when they were separate, the
// wall was the one that quietly drifted.
//
// Both go through pkg/graph's property primitives, so they see nodes and edges
// alike. That matters more than it sounds: a graph's assertions are mostly
// edges, and a tally that counted only nodes would report a shelf far better
// established than it is.

// ContractTally is how much of the shelf stands on what.
//
// The five graded fields are the contract's closed set. The two that are not
// are the point of the type:
//
//   - Untagged is every record carrying no _grade at all. On a shelf that
//     predates the contract, or that one producer writes and another does not,
//     this is the largest number in the result, and a five-bar chart drawn
//     without it describes 3% of the data while looking like all of it.
//   - Unknown is every _grade this build does not recognise. A value here
//     means a producer is writing something the contract does not define —
//     a typo, a newer contract, or a vocabulary somebody invented. Silently
//     folding those into Untagged would hide the one case a maintainer has to
//     act on.
type ContractTally struct {
	Verified       graph.PropertyCount            `json:"verified"`
	SelfConsistent graph.PropertyCount            `json:"self_consistent"`
	Asserted       graph.PropertyCount            `json:"asserted"`
	Held           graph.PropertyCount            `json:"held"`
	Refused        graph.PropertyCount            `json:"refused"`
	Untagged       graph.PropertyCount            `json:"untagged"`
	Unknown        map[string]graph.PropertyCount `json:"unknown,omitempty"`
}

// ContractTally counts every node and edge by its _grade.
func (db *DB) ContractTally(ctx context.Context) (ContractTally, error) {
	counts, err := db.graph.PropertyCounts(ctx, KeyGrade)
	if err != nil {
		return ContractTally{}, fmt.Errorf("contract tally: %w", err)
	}
	var t ContractTally
	into := map[string]*graph.PropertyCount{
		GradeVerified:       &t.Verified,
		GradeSelfConsistent: &t.SelfConsistent,
		GradeAsserted:       &t.Asserted,
		GradeHeld:           &t.Held,
		GradeRefused:        &t.Refused,
		"":                  &t.Untagged,
	}
	for value, c := range counts {
		if dst, ok := into[value]; ok {
			*dst = c
			continue
		}
		if t.Unknown == nil {
			t.Unknown = map[string]graph.PropertyCount{}
		}
		t.Unknown[value] = c
	}
	return t, nil
}

// GradedRecord is one record with the contract keys a reader renders.
//
// The contract's other keys (_chunk, _model, _confidence, and whatever a
// producer adds under the prefix) are deliberately not here: this is what a
// wall shows and what an agent cites, and a struct that grew a field per key
// would be a second place to keep the contract in step. A caller wanting the
// rest has the record id.
type GradedRecord struct {
	ID      string `json:"id"`
	Edge    bool   `json:"edge"`
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`

	Grade    string `json:"grade"`
	State    string `json:"state,omitempty"`
	Why      string `json:"why,omitempty"`
	Source   string `json:"source,omitempty"`
	Producer string `json:"producer,omitempty"`
	At       string `json:"at,omitempty"`
	By       string `json:"by,omitempty"`
}

// GradedQuery narrows GradedRecords.
type GradedQuery struct {
	// Grades keeps records whose _grade is any of these. Empty is refused
	// rather than meaning "all": a reader asking for everything on a shared
	// shelf is asking for other people's records too, and the useful questions
	// all name a grade.
	Grades []string
	// Sources, if set, keeps only records from these _source values.
	Sources []string
	// Limit caps the rows. 0 means no cap.
	Limit int
}

// GradedRecords lists records by grade, with the keys a reader needs to say
// why it is showing them.
func (db *DB) GradedRecords(ctx context.Context, q GradedQuery) ([]GradedRecord, error) {
	if len(q.Grades) == 0 {
		return nil, fmt.Errorf("graded records: no grades named")
	}
	where := map[string][]string{KeyGrade: q.Grades}
	if len(q.Sources) > 0 {
		where[KeySource] = q.Sources
	}
	fetch := []string{KeyGrade, KeyState, KeyWhy, KeySource, KeyProducer, KeyAt, KeyBy}
	recs, err := db.graph.RecordsWithProperties(ctx, graph.PropertyRecordQuery{
		Where: where,
		Fetch: fetch,
		Limit: q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("graded records: %w", err)
	}
	out := make([]GradedRecord, 0, len(recs))
	for _, r := range recs {
		out = append(out, GradedRecord{
			ID: r.ID, Edge: r.Edge, Type: r.Type, Content: r.Content,
			From: r.From, To: r.To,
			Grade:    r.Properties[KeyGrade],
			State:    r.Properties[KeyState],
			Why:      r.Properties[KeyWhy],
			Source:   r.Properties[KeySource],
			Producer: r.Properties[KeyProducer],
			At:       r.Properties[KeyAt],
			By:       r.Properties[KeyBy],
		})
	}
	return out, nil
}

// NeedsAttention is everything held or refused, with its reason.
//
// It is a named call rather than a GradedQuery a caller assembles because it
// is the one question the contract exists to make answerable, and because the
// two grades belong together: held is "nobody has looked yet" and refused is
// "somebody looked and said no", and a reader working through a shelf wants
// both in one list, told apart by Grade. Splitting them into two calls makes
// it likely that only one gets rendered.
//
// Every record here carries a Why — ValidateContract requires it for both
// grades — so a caller can show what to do about each one rather than only
// that something is wrong.
func (db *DB) NeedsAttention(ctx context.Context, limit int) ([]GradedRecord, error) {
	return db.GradedRecords(ctx, GradedQuery{
		Grades: []string{GradeHeld, GradeRefused},
		Limit:  limit,
	})
}
