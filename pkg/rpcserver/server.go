// Package rpcserver exposes the pkg/cortexdb facade over gRPC.
// It is a pure conversion layer: every handler converts proto messages to the
// facade's Request/Response structs and delegates to *cortexdb.DB.
package rpcserver

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// Options configures the gRPC server wrapper.
type Options struct {
	// Token enables bearer-token auth when non-empty. It grants unconfined
	// read/write, which is what it has always meant; KeyFile is how a
	// deployment gets anything narrower.
	Token string
	// KeyFile is the path to a JSON file of scoped API keys. When set it is
	// the entire policy and Token is ignored — see authz.Resolve for why
	// honouring both would defeat the point.
	KeyFile string
	// Keys is a pre-loaded policy, for callers that build one in process.
	// It takes precedence over Token and KeyFile.
	Keys *authz.KeySet
	// DBPath is reported by AdminService.Info.
	DBPath string
	// BackupDir confines AdminService.Backup: destinations are relative to it
	// and may not leave it. Empty means the directory holding DBPath, which is
	// where the server is already known to be able to write.
	BackupDir string
}

// New returns a grpc.Server with all cortexdb.v1 services registered.
//
// A key file that cannot be loaded yields a server that refuses every RPC.
// Coming up wide open because the policy failed to parse is the one outcome
// this must never produce, and the alternative — panicking inside a
// constructor that has never returned an error — would be worse for callers.
// Use NewWithPolicy to see that failure at startup instead of at first call.
func New(db *cortexdb.DB, opts Options) *grpc.Server {
	s, err := NewWithPolicy(db, opts)
	if err != nil {
		return grpc.NewServer(grpc.UnaryInterceptor(refuseAllInterceptor(err)))
	}
	return s
}

// NewWithPolicy is New, reporting a key-file load failure to the caller.
func NewWithPolicy(db *cortexdb.DB, opts Options) (*grpc.Server, error) {
	keys := opts.Keys
	if keys == nil {
		var err error
		keys, err = authz.Resolve(opts.KeyFile, opts.Token)
		if err != nil {
			return nil, err
		}
	}
	s := grpc.NewServer(grpc.UnaryInterceptor(authInterceptor(keys)))
	Register(s, db, opts)
	return s, nil
}

// refuseAllInterceptor is what a server does when it has no policy it can
// trust: say why, on every call, rather than serve anything.
func refuseAllInterceptor(cause error) grpc.UnaryServerInterceptor {
	return func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) {
		return nil, status.Errorf(codes.FailedPrecondition, "api key policy unavailable: %v", cause)
	}
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
