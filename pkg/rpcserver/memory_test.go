package rpcserver

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func runMemoryRoundTrip(t *testing.T, withEmbedder bool) {
	conn := newTestConn(t, withEmbedder, "")
	client := rpcv1.NewMemoryServiceClient(conn)
	ctx := context.Background()

	meta, _ := structpb.NewStruct(map[string]any{"topic": "coffee", "count": 2.0})
	saved, err := client.SaveMemory(ctx, &rpcv1.SaveMemoryRequest{
		MemoryId: "m1", UserId: "u1", Scope: "user",
		Content: "User prefers dark roast coffee in the morning.", Metadata: meta, Importance: 0.8,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.GetMemory().GetUserId() != "u1" {
		t.Fatalf("user_id = %q", saved.GetMemory().GetUserId())
	}
	if saved.GetMemory().GetMetadata().GetFields()["topic"].GetStringValue() != "coffee" {
		t.Fatal("metadata round-trip failed")
	}

	res, err := client.SearchMemory(ctx, &rpcv1.SearchMemoryRequest{
		Query: "coffee preference", UserId: "u1", Scope: "user", TopK: 3,
	})
	if err != nil || len(res.GetResults()) == 0 {
		t.Fatalf("search: %v n=%d", err, len(res.GetResults()))
	}

	newImp := 0.9
	upd, err := client.UpdateMemory(ctx, &rpcv1.UpdateMemoryRequest{MemoryId: "m1", Importance: &newImp})
	if err != nil || upd.GetMemory().GetImportance() != 0.9 {
		t.Fatalf("update: %v importance=%v", err, upd.GetMemory().GetImportance())
	}

	del, err := client.DeleteMemory(ctx, &rpcv1.DeleteMemoryRequest{MemoryId: "m1"})
	if err != nil || !del.GetDeleted() {
		t.Fatalf("delete: %v", err)
	}
}

func TestMemoryRoundTripLexical(t *testing.T)  { runMemoryRoundTrip(t, false) }
func TestMemoryRoundTripEmbedder(t *testing.T) { runMemoryRoundTrip(t, true) }
