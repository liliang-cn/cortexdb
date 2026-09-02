package main

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpcserver"
)

// startTestServer runs a real cortexdb-grpc server on a loopback port and
// returns its address. The probe dials an address string, so bufconn would not
// exercise the path a HEALTHCHECK actually takes.
func startTestServer(t *testing.T, token string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "probe.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := rpcserver.New(db, rpcserver.Options{Token: token, DBPath: dbPath})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestProbeHealthDescribesAServingServer(t *testing.T) {
	addr := startTestServer(t, "")

	line, err := probeHealth(context.Background(), addr, "")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	for _, want := range []string{"ok", addr, "v" + cortexdbroot.Version, "embedder=none"} {
		if !strings.Contains(line, want) {
			t.Fatalf("probe line %q missing %q", line, want)
		}
	}
}

func TestProbeHealthCarriesTheToken(t *testing.T) {
	addr := startTestServer(t, "s3cret")

	if _, err := probeHealth(context.Background(), addr, "s3cret"); err != nil {
		t.Fatalf("probe with the right token: %v", err)
	}
	if _, err := probeHealth(context.Background(), addr, "wrong"); err == nil {
		t.Fatal("probe with the wrong token should fail, not report healthy")
	}
	if _, err := probeHealth(context.Background(), addr, ""); err == nil {
		t.Fatal("probe with no token against a guarded server should fail")
	}
}

func TestProbeHealthFailsWhenNothingIsListening(t *testing.T) {
	// Take a port and give it straight back, so it is almost certainly free.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()

	if _, err := probeHealth(context.Background(), addr, ""); err == nil {
		t.Fatal("probe against a dead address should fail")
	}
}

func TestProbeAddrDialsLoopbackForWildcardListeners(t *testing.T) {
	cases := map[string]string{
		"0.0.0.0:47821":   "127.0.0.1:47821",
		"[::]:47821":      "127.0.0.1:47821",
		":47821":          "127.0.0.1:47821",
		"127.0.0.1:47821": "127.0.0.1:47821",
		"10.0.0.5:47821":  "10.0.0.5:47821",
		"not-an-address":  "not-an-address",
	}
	for in, want := range cases {
		if got := probeAddr(in); got != want {
			t.Errorf("probeAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
