package rpcserver

import (
	"context"
	"testing"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func TestGraphRagInsertAndSearchEmbedder(t *testing.T) {
	conn := newTestConn(t, true, "")
	client := rpcv1.NewGraphRagServiceClient(conn)
	ctx := context.Background()

	_, err := client.InsertGraphDocument(ctx, &rpcv1.InsertGraphDocumentRequest{
		Id: "d1", Title: "SQLite", Content: "SQLite is an embedded SQL database engine used everywhere.",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	res, err := client.SearchGraphRag(ctx, &rpcv1.SearchGraphRagRequest{Query: "embedded database", TopK: 3})
	if err != nil || len(res.GetChunks()) == 0 {
		t.Fatalf("search: %v chunks=%d", err, len(res.GetChunks()))
	}
}

func TestGraphRagInsertRequiresEmbedder(t *testing.T) {
	conn := newTestConn(t, false, "")
	client := rpcv1.NewGraphRagServiceClient(conn)
	_, err := client.InsertGraphDocument(context.Background(), &rpcv1.InsertGraphDocumentRequest{Id: "d1", Content: "x"})
	if err == nil {
		t.Fatal("expected error without embedder")
	}
}

func TestTextSearchEmbedder(t *testing.T) {
	conn := newTestConn(t, true, "")
	client := rpcv1.NewGraphRagServiceClient(conn)
	ctx := context.Background()
	if _, err := client.InsertText(ctx, &rpcv1.InsertTextRequest{Id: "t1", Text: "the quick brown fox"}); err != nil {
		t.Fatalf("insert text: %v", err)
	}
	res, err := client.SearchText(ctx, &rpcv1.SearchTextRequest{Query: "quick fox", TopK: 3})
	if err != nil || len(res.GetResults()) == 0 {
		t.Fatalf("search text: %v", err)
	}
	hy, err := client.HybridSearchText(ctx, &rpcv1.HybridSearchTextRequest{Query: "quick fox", TopK: 3})
	if err != nil || len(hy.GetResults()) == 0 {
		t.Fatalf("hybrid search: %v", err)
	}
}
