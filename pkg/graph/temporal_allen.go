package graph

// Allen's thirteen interval relations.
//
// Two facts with validity intervals are related in exactly one of thirteen
// ways, and naming which one is the difference between a diff that lists two
// changed edges and a diff that says "the runbook's claim ended when the
// incident's began". "Ended when it began" is `meets`; "was already false while
// the incident ran" is `before`; "was believed throughout" is `contains`. A
// reader can act on those. Two timestamps side by side they have to compare
// themselves.
//
// Pure: no store, no context, no SQL. That is deliberate — this is the one part
// of point-in-time reads that is arithmetic rather than storage, and it is
// testable exhaustively against a table of thirteen cases.

import "time"

// Interval is a half-open span [From, To), matching how valid_from and valid_to
// are compared in SQL: a version that ends at the instant the next begins has
// no gap and no overlap.
//
// A zero From is unbounded in the past and a zero To is unbounded in the
// future, which is what NULL means in those columns. The asymmetry is real —
// the same zero value means opposite things at the two ends — so the
// comparisons below never touch time.Time.Before directly.
type Interval struct {
	From time.Time `json:"from,omitzero"`
	To   time.Time `json:"to,omitzero"`
}

// IntervalOf builds an interval from a row's validity columns.
func IntervalOf(validFrom, validTo time.Time) Interval {
	return Interval{From: validFrom, To: validTo}
}

// Valid reports whether the interval spans anything. A zero-length or inverted
// span is not an interval Allen's relations are defined over.
func (i Interval) Valid() bool {
	if i.From.IsZero() || i.To.IsZero() {
		return true // unbounded at one end spans something by construction
	}
	return i.From.Before(i.To)
}

// AllenRelation names how one interval sits against another.
type AllenRelation string

// The thirteen, in inverse pairs plus equals. The names are Allen's; the
// direction is always "a is <relation> b".
const (
	AllenBefore       AllenRelation = "before"        // a ends strictly before b starts
	AllenAfter        AllenRelation = "after"         // inverse of before
	AllenMeets        AllenRelation = "meets"         // a ends exactly where b starts
	AllenMetBy        AllenRelation = "met_by"        // inverse of meets
	AllenOverlaps     AllenRelation = "overlaps"      // a starts first, they share a stretch, a ends first
	AllenOverlappedBy AllenRelation = "overlapped_by" // inverse of overlaps
	AllenStarts       AllenRelation = "starts"        // same start, a ends first
	AllenStartedBy    AllenRelation = "started_by"    // inverse of starts
	AllenDuring       AllenRelation = "during"        // a sits strictly inside b
	AllenContains     AllenRelation = "contains"      // inverse of during
	AllenFinishes     AllenRelation = "finishes"      // same end, a starts later
	AllenFinishedBy   AllenRelation = "finished_by"   // inverse of finishes
	AllenEquals       AllenRelation = "equals"        // same start and same end

	// AllenUndefined is returned for an interval that spans nothing. Not an
	// error return: the caller of a diff wants the pairs it can name and the
	// ones it cannot, not a failed query because one row was degenerate.
	AllenUndefined AllenRelation = ""
)

// Inverse returns the relation that holds in the other direction, so a caller
// holding `a overlaps b` can state `b overlapped_by a` without recomputing.
func (r AllenRelation) Inverse() AllenRelation {
	switch r {
	case AllenBefore:
		return AllenAfter
	case AllenAfter:
		return AllenBefore
	case AllenMeets:
		return AllenMetBy
	case AllenMetBy:
		return AllenMeets
	case AllenOverlaps:
		return AllenOverlappedBy
	case AllenOverlappedBy:
		return AllenOverlaps
	case AllenStarts:
		return AllenStartedBy
	case AllenStartedBy:
		return AllenStarts
	case AllenDuring:
		return AllenContains
	case AllenContains:
		return AllenDuring
	case AllenFinishes:
		return AllenFinishedBy
	case AllenFinishedBy:
		return AllenFinishes
	case AllenEquals:
		return AllenEquals
	}
	return AllenUndefined
}

// Relate names how a sits against b. Exactly one relation holds for any two
// intervals that span anything, which is the property the whole algebra rests
// on and the reason the switch below has no default case that means "several".
func Relate(a, b Interval) AllenRelation {
	if !a.Valid() || !b.Valid() {
		return AllenUndefined
	}

	// The endpoint comparisons, each aware of which zero means which infinity.
	starts := cmpStart(a.From, b.From)
	ends := cmpEnd(a.To, b.To)

	switch c := cmpEndStart(a.To, b.From); {
	case c < 0:
		return AllenBefore
	case c == 0:
		return AllenMeets
	}
	switch c := cmpEndStart(b.To, a.From); {
	case c < 0:
		return AllenAfter
	case c == 0:
		return AllenMetBy
	}

	switch {
	case starts == 0 && ends == 0:
		return AllenEquals
	case starts == 0 && ends < 0:
		return AllenStarts
	case starts == 0 && ends > 0:
		return AllenStartedBy
	case ends == 0 && starts > 0:
		return AllenFinishes
	case ends == 0 && starts < 0:
		return AllenFinishedBy
	case starts > 0 && ends < 0:
		return AllenDuring
	case starts < 0 && ends > 0:
		return AllenContains
	case starts < 0 && ends < 0:
		return AllenOverlaps
	default: // starts > 0 && ends > 0
		return AllenOverlappedBy
	}
}

// cmpStart compares two start points, where zero is the beginning of time.
func cmpStart(a, b time.Time) int {
	switch {
	case a.IsZero() && b.IsZero():
		return 0
	case a.IsZero():
		return -1
	case b.IsZero():
		return 1
	}
	return cmpTime(a, b)
}

// cmpEnd compares two end points, where zero is the end of time.
func cmpEnd(a, b time.Time) int {
	switch {
	case a.IsZero() && b.IsZero():
		return 0
	case a.IsZero():
		return 1
	case b.IsZero():
		return -1
	}
	return cmpTime(a, b)
}

// cmpEndStart compares an end point against a start point — the one comparison
// that crosses the two conventions, and the one an ordinary Before/After would
// get backwards for an unbounded interval.
func cmpEndStart(end, start time.Time) int {
	if end.IsZero() { // runs forever, so it cannot end before anything starts
		return 1
	}
	if start.IsZero() { // started before the beginning, so nothing ends before it
		return 1
	}
	return cmpTime(end, start)
}

func cmpTime(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	}
	return 0
}

// IntervalRelation is one named pair, as a diff or a snapshot reports it.
type IntervalRelation struct {
	// Subject is what both facts are about — the node both edges leave from.
	// Two intervals with nothing in common are not worth relating.
	Subject  string        `json:"subject"`
	A        string        `json:"a"`
	B        string        `json:"b"`
	Relation AllenRelation `json:"relation"`
	AFrom    time.Time     `json:"a_from,omitzero"`
	ATo      time.Time     `json:"a_to,omitzero"`
	BFrom    time.Time     `json:"b_from,omitzero"`
	BTo      time.Time     `json:"b_to,omitzero"`
}

// RelateEdges names the temporal relation between every pair of edges that
// share a subject.
//
// Bounded by maxPairs because the pair count is quadratic in the edges per
// subject: a node with two hundred facts about it yields twenty thousand pairs,
// which is a tool response nobody reads and a model cannot use.
func RelateEdges(edges []*GraphEdge, maxPairs int) []IntervalRelation {
	if maxPairs <= 0 {
		maxPairs = 50
	}
	bySubject := map[string][]*GraphEdge{}
	order := make([]string, 0, len(edges))
	for _, e := range edges {
		if e == nil {
			continue
		}
		if _, seen := bySubject[e.FromNodeID]; !seen {
			order = append(order, e.FromNodeID)
		}
		bySubject[e.FromNodeID] = append(bySubject[e.FromNodeID], e)
	}

	out := make([]IntervalRelation, 0)
	for _, subject := range order {
		group := bySubject[subject]
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				if len(out) >= maxPairs {
					return out
				}
				a, b := group[i], group[j]
				ai := IntervalOf(a.ValidFrom, a.ValidTo)
				bi := IntervalOf(b.ValidFrom, b.ValidTo)
				out = append(out, IntervalRelation{
					Subject:  subject,
					A:        a.ID,
					B:        b.ID,
					Relation: Relate(ai, bi),
					AFrom:    ai.From, ATo: ai.To,
					BFrom: bi.From, BTo: bi.To,
				})
			}
		}
	}
	return out
}
