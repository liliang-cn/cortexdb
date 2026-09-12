package main

import (
	"errors"
	"fmt"
	"strconv"
	"testing"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// fakePages lets a test hand walkMemoryPages a scripted sequence of
// responses keyed by the cursor the walk sends in, without any gRPC.
type fakePages struct {
	byCursor map[string]cortexdb.MemoryListAllResponse
	errAt    map[string]error
	calls    []string
}

func (f *fakePages) fetch(cursor string) (cortexdb.MemoryListAllResponse, error) {
	f.calls = append(f.calls, cursor)
	if err, ok := f.errAt[cursor]; ok {
		return cortexdb.MemoryListAllResponse{}, err
	}
	resp, ok := f.byCursor[cursor]
	if !ok {
		return cortexdb.MemoryListAllResponse{}, fmt.Errorf("fake: no page scripted for cursor %q", cursor)
	}
	return resp, nil
}

// makeRecords returns n distinct records numbered starting at start, so a
// test can assert both count and order.
func makeRecords(start, n int) []cortexdb.MemoryRecord {
	recs := make([]cortexdb.MemoryRecord, n)
	for i := 0; i < n; i++ {
		recs[i] = cortexdb.MemoryRecord{ID: strconv.Itoa(start + i)}
	}
	return recs
}

func TestWalkMemoryPagesTwoCleanPages(t *testing.T) {
	page1 := makeRecords(0, 500)
	page2 := makeRecords(500, 200)
	f := &fakePages{byCursor: map[string]cortexdb.MemoryListAllResponse{
		"": {Memories: page1, Truncated: true, NextCursor: "cursor-1"},
		"cursor-1": {Memories: page2, Truncated: false},
	}}

	got, err := walkMemoryPages(f.fetch, 0, "addr")
	if err != nil {
		t.Fatalf("walkMemoryPages: %v", err)
	}
	if len(got) != 700 {
		t.Fatalf("expected 700 records, got %d", len(got))
	}
	for i, rec := range got {
		if rec.ID != strconv.Itoa(i) {
			t.Fatalf("record %d: expected ID %d, got %s (order not preserved)", i, i, rec.ID)
		}
	}
	if len(f.calls) != 2 {
		t.Fatalf("expected exactly 2 fetch calls, got %d: %v", len(f.calls), f.calls)
	}
}

func TestWalkMemoryPagesLimitCapsMidPage(t *testing.T) {
	page1 := makeRecords(0, 500)
	page2 := makeRecords(500, 500)
	f := &fakePages{byCursor: map[string]cortexdb.MemoryListAllResponse{
		"": {Memories: page1, Truncated: true, NextCursor: "cursor-1"},
		"cursor-1": {Memories: page2, Truncated: true, NextCursor: "cursor-2"},
	}}

	got, err := walkMemoryPages(f.fetch, 700, "addr")
	if err != nil {
		t.Fatalf("walkMemoryPages: %v", err)
	}
	if len(got) != 700 {
		t.Fatalf("expected exactly 700 records for limit 700, got %d", len(got))
	}
	if len(f.calls) != 2 {
		t.Fatalf("expected the walk to stop after the page that reached the limit, got %d calls: %v", len(f.calls), f.calls)
	}
}

func TestWalkMemoryPagesLimitZeroWalksToCompletion(t *testing.T) {
	page1 := makeRecords(0, 500)
	page2 := makeRecords(500, 500)
	page3 := makeRecords(1000, 42)
	f := &fakePages{byCursor: map[string]cortexdb.MemoryListAllResponse{
		"":         {Memories: page1, Truncated: true, NextCursor: "cursor-1"},
		"cursor-1": {Memories: page2, Truncated: true, NextCursor: "cursor-2"},
		"cursor-2": {Memories: page3, Truncated: false},
	}}

	got, err := walkMemoryPages(f.fetch, 0, "addr")
	if err != nil {
		t.Fatalf("walkMemoryPages: %v", err)
	}
	if len(got) != 1042 {
		t.Fatalf("expected 1042 records, got %d", len(got))
	}
	if len(f.calls) != 3 {
		t.Fatalf("expected exactly 3 fetch calls, got %d: %v", len(f.calls), f.calls)
	}
}

func TestWalkMemoryPagesOldServerNoCursor(t *testing.T) {
	f := &fakePages{byCursor: map[string]cortexdb.MemoryListAllResponse{
		"": {Memories: makeRecords(0, 500), Truncated: true, NextCursor: ""},
	}}

	got, err := walkMemoryPages(f.fetch, 0, "addr")
	if err == nil {
		t.Fatalf("expected an error for a truncated response with no cursor, got %d records", len(got))
	}
	if got != nil {
		t.Fatalf("expected no partial slice on the old-server path, got %d records", len(got))
	}
	if len(f.calls) != 1 {
		t.Fatalf("expected the walk to stop after one call, got %d: %v", len(f.calls), f.calls)
	}
}

func TestWalkMemoryPagesStalledCursor(t *testing.T) {
	f := &fakePages{byCursor: map[string]cortexdb.MemoryListAllResponse{
		"":         {Memories: makeRecords(0, 500), Truncated: true, NextCursor: "cursor-1"},
		"cursor-1": {Memories: makeRecords(500, 500), Truncated: true, NextCursor: "cursor-1"},
	}}

	got, err := walkMemoryPages(f.fetch, 0, "addr")
	if err == nil {
		t.Fatalf("expected an error when the cursor stops advancing, got %d records", len(got))
	}
	if got != nil {
		t.Fatalf("expected no partial slice on the stalled-cursor path, got %d records", len(got))
	}
	// The stall must be caught on the page that repeats the cursor, not after
	// grinding through the page-count backstop.
	if len(f.calls) != 2 {
		t.Fatalf("expected exactly 2 fetch calls before the stall was caught, got %d: %v", len(f.calls), f.calls)
	}
}

func TestWalkMemoryPagesTruncatedWithNoRecords(t *testing.T) {
	f := &fakePages{byCursor: map[string]cortexdb.MemoryListAllResponse{
		"": {Memories: nil, Truncated: true, NextCursor: "cursor-1"},
	}}

	got, err := walkMemoryPages(f.fetch, 0, "addr")
	if err == nil {
		t.Fatalf("expected an error for a truncated page with zero records, got %d records", len(got))
	}
	if got != nil {
		t.Fatalf("expected no partial slice, got %d records", len(got))
	}
	if len(f.calls) != 1 {
		t.Fatalf("expected exactly 1 fetch call, got %d: %v", len(f.calls), f.calls)
	}
}

func TestWalkMemoryPagesFetchErrorPropagates(t *testing.T) {
	boom := errors.New("boom: connection reset")
	f := &fakePages{
		byCursor: map[string]cortexdb.MemoryListAllResponse{
			"": {Memories: makeRecords(0, 500), Truncated: true, NextCursor: "cursor-1"},
		},
		errAt: map[string]error{"cursor-1": boom},
	}

	got, err := walkMemoryPages(f.fetch, 0, "addr")
	if err == nil {
		t.Fatalf("expected the fetch error to propagate, got %d records", len(got))
	}
	if !errors.Is(err, boom) {
		t.Fatalf("expected the underlying fetch error to propagate via errors.Is, got: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no partial slice when a fetch mid-walk fails, got %d records", len(got))
	}
}
