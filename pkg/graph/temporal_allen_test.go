package graph

import (
	"testing"
	"time"
)

// The thirteen relations against a table, because "exactly one holds" is the
// property the algebra rests on and an implementation that gets twelve right is
// indistinguishable from one that gets thirteen right until the missing case
// shows up in somebody's incident review.
func TestAllenRelations(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 3, 1, h, 0, 0, 0, time.UTC) }
	iv := func(from, to int) Interval { return Interval{From: at(from), To: at(to)} }

	for _, tc := range []struct {
		name string
		a, b Interval
		want AllenRelation
	}{
		{"before", iv(1, 2), iv(3, 4), AllenBefore},
		{"after", iv(3, 4), iv(1, 2), AllenAfter},
		{"meets", iv(1, 2), iv(2, 3), AllenMeets},
		{"met_by", iv(2, 3), iv(1, 2), AllenMetBy},
		{"overlaps", iv(1, 3), iv(2, 4), AllenOverlaps},
		{"overlapped_by", iv(2, 4), iv(1, 3), AllenOverlappedBy},
		{"starts", iv(1, 2), iv(1, 3), AllenStarts},
		{"started_by", iv(1, 3), iv(1, 2), AllenStartedBy},
		{"during", iv(2, 3), iv(1, 4), AllenDuring},
		{"contains", iv(1, 4), iv(2, 3), AllenContains},
		{"finishes", iv(2, 3), iv(1, 3), AllenFinishes},
		{"finished_by", iv(1, 3), iv(2, 3), AllenFinishedBy},
		{"equals", iv(1, 3), iv(1, 3), AllenEquals},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Relate(tc.a, tc.b); got != tc.want {
				t.Errorf("Relate = %q, want %q", got, tc.want)
			}
			// Every relation has an inverse, and the inverse must be what
			// Relate says with the arguments swapped. A table that only checked
			// one direction would pass with the two halves of a pair confused.
			if got := Relate(tc.b, tc.a); got != tc.want.Inverse() {
				t.Errorf("Relate(b, a) = %q, want the inverse %q", got, tc.want.Inverse())
			}
		})
	}
}

// Unbounded ends are how a live row is stored: valid_to NULL means "still
// true", and it is not a very large timestamp — an ordinary comparison would
// read the zero time as the year 1 and call every open-ended fact ancient.
func TestAllenRelationsWithUnboundedEnds(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 3, 1, h, 0, 0, 0, time.UTC) }
	openEnd := Interval{From: at(2)} // still true
	openStart := Interval{To: at(2)} // always was, until 02:00
	everything := Interval{}         // always, forever
	bounded := Interval{From: at(3), To: at(4)}

	for _, tc := range []struct {
		name string
		a, b Interval
		want AllenRelation
	}{
		{"the claim that ended is met by the one that began", openStart, openEnd, AllenMeets},
		{"a live fact contains a bounded one that starts later", openEnd, bounded, AllenContains},
		{"the unbounded interval contains everything", everything, bounded, AllenContains},
		{"unbounded equals unbounded", everything, everything, AllenEquals},
		{"a bounded fact during the open-ended one", bounded, openEnd, AllenDuring},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Relate(tc.a, tc.b); got != tc.want {
				t.Errorf("Relate = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAllenRefusesDegenerateIntervals(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 3, 1, h, 0, 0, 0, time.UTC) }
	zeroLength := Interval{From: at(1), To: at(1)}
	inverted := Interval{From: at(2), To: at(1)}
	ok := Interval{From: at(3), To: at(4)}

	for _, bad := range []Interval{zeroLength, inverted} {
		if got := Relate(bad, ok); got != AllenUndefined {
			t.Errorf("a degenerate interval related as %q; it spans nothing and no relation holds", got)
		}
		if got := Relate(ok, bad); got != AllenUndefined {
			t.Errorf("a degenerate interval related as %q on the right-hand side", got)
		}
	}
}

// RelateEdges is the part the tool output uses: pairs sharing a subject, and
// nothing else, because two facts about different things have no interesting
// temporal relation.
func TestRelateEdgesPairsOnlyBySubject(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 3, 1, h, 0, 0, 0, time.UTC) }
	edges := []*GraphEdge{
		{ID: "runbook", FromNodeID: "sds-meta", ValidTo: at(2)},
		{ID: "incident", FromNodeID: "sds-meta", ValidFrom: at(2)},
		{ID: "elsewhere", FromNodeID: "other-host", ValidFrom: at(1)},
	}
	rels := RelateEdges(edges, 50)
	if len(rels) != 1 {
		t.Fatalf("got %d relations, want 1 (only the two about sds-meta pair)", len(rels))
	}
	if rels[0].Relation != AllenMeets {
		t.Errorf("relation = %q, want %q: the runbook's claim ended where the incident's began",
			rels[0].Relation, AllenMeets)
	}
	if rels[0].Subject != "sds-meta" {
		t.Errorf("subject = %q, want sds-meta", rels[0].Subject)
	}
}

func TestRelateEdgesIsBounded(t *testing.T) {
	edges := make([]*GraphEdge, 0, 40)
	for i := 0; i < 40; i++ {
		edges = append(edges, &GraphEdge{ID: string(rune('a'+i%26)) + string(rune('a'+i/26)), FromNodeID: "one-subject"})
	}
	// 40 edges about one subject is 780 pairs; a tool response cannot carry
	// them and a reader would not use them.
	if got := len(RelateEdges(edges, 10)); got != 10 {
		t.Errorf("RelateEdges returned %d pairs with a cap of 10", got)
	}
}
