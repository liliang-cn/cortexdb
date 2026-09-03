package rpcserver

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
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

// newBackupServer starts a server whose backup directory is a directory of its
// own, so a test can tell "landed in the backup directory" apart from "landed
// next to the database".
func newBackupServer(t *testing.T) (rpcv1.AdminServiceClient, string, *cortexdb.DB) {
	t.Helper()
	root := t.TempDir()
	dbPath := filepath.Join(root, "brain.db")
	backupDir := filepath.Join(root, "backups")

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	lis := bufconn.Listen(1 << 20)
	srv := New(db, Options{DBPath: dbPath, BackupDir: backupDir})
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

	return rpcv1.NewAdminServiceClient(conn), backupDir, db
}

// samePath compares two paths through whatever symlinks stand between them and
// the filesystem — /var is one on macOS, and the handler resolves the backup
// directory only when it already exists.
func samePath(t *testing.T, got, want string) bool {
	t.Helper()
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return resolve(got) == resolve(want)
}

// TestBackupOverTheWireLandsInTheBackupDirectory is the whole point of the RPC:
// an operator with a token and no shell on the box can take a backup.
func TestBackupOverTheWireLandsInTheBackupDirectory(t *testing.T) {
	client, backupDir, _ := newBackupServer(t)

	resp, err := client.Backup(context.Background(), &rpcv1.BackupRequest{Path: "daily/monday.db"})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	want := filepath.Join(backupDir, "daily", "monday.db")
	if !samePath(t, resp.GetPath(), want) {
		t.Fatalf("backup path = %q, want %q", resp.GetPath(), want)
	}
	fi, err := os.Stat(resp.GetPath())
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if fi.Size() == 0 || resp.GetSizeBytes() != fi.Size() {
		t.Fatalf("size_bytes = %d, file is %d bytes", resp.GetSizeBytes(), fi.Size())
	}

	if _, err := client.Backup(context.Background(), &rpcv1.BackupRequest{Path: "daily/monday.db"}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("second backup to the same name: %v, want AlreadyExists", err)
	}
}

// TestBackupRefusesToWriteOutsideTheBackupDirectory is the security case. The
// caller picks this path over the network, and a token that grants rows must not
// thereby grant a file anywhere on the host.
func TestBackupRefusesToWriteOutsideTheBackupDirectory(t *testing.T) {
	client, backupDir, _ := newBackupServer(t)

	escapes := []struct {
		name string
		path string
	}{
		{"absolute", filepath.Join(t.TempDir(), "stolen.db")},
		{"parent", "../stolen.db"},
		{"buried traversal", "daily/../../stolen.db"},
		{"traversal back in", "daily/../../backups/../stolen.db"},
		{"the directory itself", "."},
		{"empty", ""},
	}
	for _, tc := range escapes {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.Backup(context.Background(), &rpcv1.BackupRequest{Path: tc.path})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("path %q: err = %v, want InvalidArgument", tc.path, err)
			}
		})
	}

	// Nothing was created anywhere: the rejections happen before any mkdir.
	if entries, err := os.ReadDir(filepath.Dir(backupDir)); err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), "stolen") {
				t.Fatalf("a rejected backup still created %s", e.Name())
			}
		}
	}
}

// TestBackupPathWithAQuoteStaysAFilename guards the destination reaching SQLite
// as a bound parameter. The driver executes several statements from one string,
// so a filename interpolated into "VACUUM INTO '...'" would let a network caller
// append SQL of their own — and a filename is allowed to contain a quote, so
// nothing upstream rejects it. The proof is that the table named in the payload
// is still there afterwards.
func TestBackupPathWithAQuoteStaysAFilename(t *testing.T) {
	client, backupDir, db := newBackupServer(t)

	const payload = "x'; DROP TABLE embeddings; --"
	resp, err := client.Backup(context.Background(), &rpcv1.BackupRequest{Path: payload})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !samePath(t, resp.GetPath(), filepath.Join(backupDir, payload)) {
		t.Fatalf("backup path = %q, want the payload treated as one filename", resp.GetPath())
	}

	var n int
	if err := db.SQL().QueryRow("SELECT count(*) FROM embeddings").Scan(&n); err != nil {
		t.Fatalf("embeddings table is gone: %v", err)
	}
}
