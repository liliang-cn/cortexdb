// Package rpcserver exposes the pkg/cortexdb facade over gRPC.
// It is a pure conversion layer: every handler converts proto messages to the
// facade's Request/Response structs and delegates to *cortexdb.DB.
package rpcserver

import (
	"strings"

	"google.golang.org/grpc"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// Options configures the gRPC server wrapper.
type Options struct {
	// Token enables bearer-token auth when non-empty.
	Token string
	// DBPath is reported by AdminService.Info.
	DBPath string
	// BackupDir confines AdminService.Backup: destinations are relative to it
	// and may not leave it. Empty means the directory holding DBPath, which is
	// where the server is already known to be able to write.
	BackupDir string
}

// New returns a grpc.Server with all cortexdb.v1 services registered.
func New(db *cortexdb.DB, opts Options) *grpc.Server {
	s := grpc.NewServer(grpc.UnaryInterceptor(authInterceptor(opts.Token)))
	Register(s, db, opts)
	return s
}

// Register attaches all cortexdb.v1 services to an existing grpc.Server.
func Register(s *grpc.Server, db *cortexdb.DB, opts Options) {
	backupDir := opts.BackupDir
	if strings.TrimSpace(backupDir) == "" {
		backupDir = defaultBackupDir(opts.DBPath)
	}
	rpcv1.RegisterAdminServiceServer(s, &adminService{db: db, dbPath: opts.DBPath, backupDir: backupDir})
	rpcv1.RegisterKnowledgeServiceServer(s, &knowledgeService{db: db})
	rpcv1.RegisterMemoryServiceServer(s, &memoryService{db: db})
	rpcv1.RegisterKnowledgeGraphServiceServer(s, &graphService{db: db})
	rpcv1.RegisterGraphRagServiceServer(s, &graphragService{db: db})
	rpcv1.RegisterToolsServiceServer(s, &toolsService{db: db})
}
