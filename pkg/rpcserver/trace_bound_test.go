package rpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// boundRecord logs one memory_get with args through the request log and
// returns the text line and the decoded record.
func boundRecord(t *testing.T, args any) (string, map[string][]string, int) {
	t.Helper()
	var buf, file bytes.Buffer
	raw, _ := json.Marshal(args)
	logger := RequestLogInterceptor(TraceOptions{All: true, Log: slog.New(slog.NewTextHandler(&buf, nil)), File: &file})
	req := &rpcv1.CallToolRequest{Name: "memory_get", ArgsJson: string(raw)}
	_, _ = logger(context.Background(), req, &grpc.UnaryServerInfo{FullMethod: callToolMethod}, func(context.Context, any) (any, error) { return nil, nil })
	var rec struct {
		IDs        map[string][]string `json:"ids"`
		IDsOmitted int                 `json:"ids_omitted"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(file.Bytes()), &rec); err != nil {
		t.Fatalf("record %q: %v", file.String(), err)
	}
	if strings.Contains(file.String(), `"ids_omitted":0`) {
		t.Errorf("ids_omitted written as zero: %s", file.String())
	}
	return buf.String(), rec.IDs, rec.IDsOmitted
}

// The record keeps at most maxTraceIDs ids per path, the first ones in the
// sorted order it logs them in, and says how many it left out, summed over
// the paths. The line still counts them all. The log runs before
// authentication, so a caller with any token could otherwise write a list
// of any length to the file.
func TestTheRecordBoundsTheIDsItKeeps(t *testing.T) {
	ids := make([]string, maxTraceIDs+50)
	for i := range ids {
		ids[i] = fmt.Sprintf("m%04d", i)
	}
	line, got, omitted := boundRecord(t, map[string]any{"memory_ids": ids, "memory_id": "solo"})
	if list := got["memory_ids"]; len(list) != maxTraceIDs || list[0] != "m0000" || list[maxTraceIDs-1] != ids[maxTraceIDs-1] {
		t.Errorf("kept %d ids (%v ...), want the first %d", len(list), list[:min(len(list), 2)], maxTraceIDs)
	}
	if !reflect.DeepEqual(got["memory_id"], []string{"solo"}) || omitted != 50 {
		t.Errorf("memory_id %v, ids_omitted %d; want [solo], 50", got["memory_id"], omitted)
	}
	if want := fmt.Sprintf(" ids=%d", maxTraceIDs+51); !strings.Contains(line, want) {
		t.Errorf("the line %q does not count every id (%s)", line, want)
	}
	// Two paths over the bound: the omitted ids of both are counted.
	_, _, omitted = boundRecord(t, map[string]any{"memory_ids": ids, "memory_id": ids})
	if omitted != 100 {
		t.Errorf("ids_omitted %d over two paths, want 100", omitted)
	}
	// Under the bound, nothing is omitted and the field is absent.
	if _, _, omitted = boundRecord(t, map[string]any{"memory_ids": []string{"m1", "m2"}}); omitted != 0 {
		t.Errorf("a short list says it omitted %d ids", omitted)
	}
}

// The record also bounds each id's length: an id longer than maxTraceIDBytes
// is cut there and marked with "…", so a hundred long ids cannot fill the
// file either. The cut falls on a character boundary: "€" is three bytes, so
// 256 bytes would split the 86th, and the id keeps 85 of them (255 bytes).
// An id of exactly maxTraceIDBytes is kept whole, one byte more is cut. The
// ids are distinct before the cut: two that share their first 256 bytes are
// both listed, and look the same after it.
func TestTheRecordCutsALongID(t *testing.T) {
	euros := strings.Repeat("€", 100)
	exact := strings.Repeat("a", maxTraceIDBytes)
	_, got, _ := boundRecord(t, map[string]any{"memory_id": euros, "memory_ids": []string{"short", exact, exact + "b"}})
	if want := []string{strings.Repeat("€", 85) + "…"}; !reflect.DeepEqual(got["memory_id"], want) {
		t.Errorf("a 300-byte id of \"€\" logged as %q, want 85 of them and the mark", got["memory_id"])
	}
	if want := []string{exact, exact + "…", "short"}; !reflect.DeepEqual(got["memory_ids"], want) {
		t.Errorf("memory_ids logged as %q, want the 256-byte id whole, the 257-byte one cut, the short one whole", got["memory_ids"])
	}
	_, got, _ = boundRecord(t, map[string]any{"memory_ids": []string{exact + "x", exact + "y"}})
	if want := []string{exact + "…", exact + "…"}; !reflect.DeepEqual(got["memory_ids"], want) {
		t.Errorf("two ids that share 256 bytes logged as %d entries, want both", len(got["memory_ids"]))
	}
}
