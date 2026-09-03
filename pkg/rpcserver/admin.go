package rpcserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type adminService struct {
	rpcv1.UnimplementedAdminServiceServer
	db     *cortexdb.DB
	dbPath string
	// backupDir is the only directory Backup will write into. Resolved once at
	// registration so every request is checked against the same canonical
	// string rather than against whatever the process's cwd happens to be.
	backupDir string
}

func (s *adminService) Health(context.Context, *rpcv1.HealthRequest) (*rpcv1.HealthResponse, error) {
	return &rpcv1.HealthResponse{Ok: true}, nil
}

func (s *adminService) Info(context.Context, *rpcv1.InfoRequest) (*rpcv1.InfoResponse, error) {
	return &rpcv1.InfoResponse{
		Version:     cortexdbroot.Version,
		DbPath:      s.dbPath,
		HasEmbedder: s.db.HasEmbedder(),
	}, nil
}

// Backup snapshots the brain to a file on the server, without stopping it.
//
// The caller names the destination, which is why most of this handler is about
// where the file may go. A bearer token already grants full read and write, so
// nothing here is a privilege the caller lacked — but "write a file at a path I
// choose" is a different class of thing from "write a row", and a backup is a
// complete readable copy of the brain, so where it lands decides who else can
// read it. The path is therefore relative to one configured directory and
// cannot leave it. The systemd unit's ProtectSystem=strict already stops the
// process writing outside its state directory; this is the half that does not
// depend on the deployment having been hardened.
func (s *adminService) Backup(ctx context.Context, req *rpcv1.BackupRequest) (*rpcv1.BackupResponse, error) {
	dest, err := resolveBackupPath(s.backupDir, req.GetPath())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Subdirectories under the backup directory are allowed (dated folders are
	// the obvious use), so the parent may not exist yet. 0o700 matches the
	// unit's StateDirectoryMode: a backup is as readable as the database.
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return nil, status.Errorf(codes.Internal, "create backup directory: %v", err)
	}
	// SQLite would refuse an existing destination anyway; saying so plainly is
	// worth more to whoever is reading a failed cron job's output.
	if _, err := os.Stat(dest); err == nil {
		return nil, status.Errorf(codes.AlreadyExists, "backup destination %q already exists", req.GetPath())
	}

	if err := s.db.Backup(ctx, dest); err != nil {
		if errors.Is(err, cortexdb.ErrBackupUnsupported) {
			return nil, status.Error(codes.Unimplemented, err.Error())
		}
		return nil, toStatus(err)
	}

	var size int64
	if fi, err := os.Stat(dest); err == nil {
		size = fi.Size()
	}
	return &rpcv1.BackupResponse{Path: dest, SizeBytes: size}, nil
}

// resolveBackupPath turns a caller-supplied name into an absolute path that is
// provably inside dir, or an error saying why it is not.
//
// The rule is: the name is relative, and the cleaned join of dir and the name
// must still be under dir. That single containment check is what actually holds
// — the explicit rejections of absolute paths and of ".." exist so the error
// message names the mistake, not because the check needs them.
func resolveBackupPath(dir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("backup path is required")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("backup path %q must be relative to the server's backup directory, not absolute", name)
	}
	if dir == "" {
		return "", errors.New("this server has no backup directory configured")
	}

	root, err := canonicalDir(dir)
	if err != nil {
		return "", fmt.Errorf("resolve backup directory: %v", err)
	}

	dest := filepath.Clean(filepath.Join(root, name))
	rel, err := filepath.Rel(root, dest)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("backup path %q escapes the server's backup directory", name)
	}
	return dest, nil
}

// canonicalDir resolves symlinks in dir so containment is checked against the
// real path. Without this a backup directory reached through a symlink (/var on
// a system where it is one, or a macOS temp dir) compares unequal to the path
// the join produces, and every request looks like an escape.
func canonicalDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	// Best effort: the directory may not exist yet on a first backup, and that
	// is not a reason to refuse — MkdirAll creates it below.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// defaultBackupDir is where backups go when the operator has not said. The
// directory holding the database is the one place the server is already known
// to be able to write — under ProtectSystem=strict it is generally the only one.
func defaultBackupDir(dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return ""
	}
	// A postgres:// DSN has no directory, and its backend cannot back itself up
	// anyway; leaving this empty makes the RPC fail on the path rather than
	// inventing a directory named after a URL.
	if strings.Contains(dbPath, "://") {
		return ""
	}
	return filepath.Dir(dbPath)
}
