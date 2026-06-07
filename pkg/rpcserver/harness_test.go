package rpcserver

import (
	"context"
	"crypto/sha256"
	"net"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// fakeEmbedder is a deterministic embedder for dual-mode tests.
type fakeEmbedder struct{}

func (fakeEmbedder) Dim() int { return 8 }

func (f fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	sum := sha256.Sum256([]byte(text))
	vec := make([]float32, 8)
	for i := range vec {
		vec[i] = float32(sum[i]) / 255.0
	}
	return vec, nil
}

func (f fakeEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := f.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// newTestConn spins up an in-process server over bufconn and returns a client conn.
func newTestConn(t *testing.T, withEmbedder bool, token string) *grpc.ClientConn {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	var opts []cortexdb.Option
	if withEmbedder {
		opts = append(opts, cortexdb.WithEmbedder(fakeEmbedder{}))
	}
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath), opts...)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	lis := bufconn.Listen(1 << 20)
	srv := New(db, Options{Token: token, DBPath: dbPath})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
