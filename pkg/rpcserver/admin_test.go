package rpcserver

import (
	"context"
	"testing"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func TestAdminInfoAndHealth(t *testing.T) {
	conn := newTestConn(t, false, "")
	client := rpcv1.NewAdminServiceClient(conn)

	h, err := client.Health(context.Background(), &rpcv1.HealthRequest{})
	if err != nil || !h.GetOk() {
		t.Fatalf("health: %v ok=%v", err, h.GetOk())
	}
	info, err := client.Info(context.Background(), &rpcv1.InfoRequest{})
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.GetVersion() != cortexdbroot.Version {
		t.Fatalf("version = %q, want %q", info.GetVersion(), cortexdbroot.Version)
	}
	if info.GetHasEmbedder() {
		t.Fatal("expected has_embedder=false")
	}
}

func TestAdminAuthOverWire(t *testing.T) {
	conn := newTestConn(t, false, "tok123")
	client := rpcv1.NewAdminServiceClient(conn)
	if _, err := client.Health(context.Background(), &rpcv1.HealthRequest{}); err == nil {
		t.Fatal("expected UNAUTHENTICATED without token")
	}
}
