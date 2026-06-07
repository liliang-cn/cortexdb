package rpcserver

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func runKnowledgeRoundTrip(t *testing.T, withEmbedder bool) {
	conn := newTestConn(t, withEmbedder, "")
	client := rpcv1.NewKnowledgeServiceClient(conn)
	ctx := context.Background()

	saved, err := client.SaveKnowledge(ctx, &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k1",
		Title:       "Go concurrency",
		Content:     "Goroutines are lightweight threads managed by the Go runtime. Channels connect goroutines.",
		Metadata:    map[string]string{"lang": "en"},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.GetKnowledge().GetId() != "k1" {
		t.Fatalf("saved id = %q", saved.GetKnowledge().GetId())
	}

	got, err := client.GetKnowledge(ctx, &rpcv1.GetKnowledgeRequest{KnowledgeId: "k1"})
	if err != nil || got.GetKnowledge().GetTitle() != "Go concurrency" {
		t.Fatalf("get: %v title=%q", err, got.GetKnowledge().GetTitle())
	}

	res, err := client.SearchKnowledge(ctx, &rpcv1.SearchKnowledgeRequest{
		Query: "goroutines channels", TopK: 3,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.GetResults()) == 0 {
		t.Fatal("search returned no results")
	}
	if res.GetResults()[0].GetKnowledgeId() != "k1" {
		t.Fatalf("top hit = %q, want k1", res.GetResults()[0].GetKnowledgeId())
	}

	del, err := client.DeleteKnowledge(ctx, &rpcv1.DeleteKnowledgeRequest{KnowledgeId: "k1"})
	if err != nil || !del.GetDeleted() {
		t.Fatalf("delete: %v deleted=%v", err, del.GetDeleted())
	}
	if _, err := client.GetKnowledge(ctx, &rpcv1.GetKnowledgeRequest{KnowledgeId: "k1"}); status.Code(err) != codes.NotFound {
		t.Fatalf("get after delete: want NotFound, got %v", err)
	}
}

func TestKnowledgeRoundTripLexical(t *testing.T)  { runKnowledgeRoundTrip(t, false) }
func TestKnowledgeRoundTripEmbedder(t *testing.T) { runKnowledgeRoundTrip(t, true) }

func TestSaveKnowledgeValidation(t *testing.T) {
	conn := newTestConn(t, false, "")
	client := rpcv1.NewKnowledgeServiceClient(conn)
	_, err := client.SaveKnowledge(context.Background(), &rpcv1.SaveKnowledgeRequest{Content: "x"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}
