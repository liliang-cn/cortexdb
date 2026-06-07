// Package rpcserver exposes the pkg/cortexdb facade over gRPC.
// It is a pure conversion layer: every handler converts proto messages to the
// facade's Request/Response structs and delegates to *cortexdb.DB.
package rpcserver

import (
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
}

// New returns a grpc.Server with all cortexdb.v1 services registered.
func New(db *cortexdb.DB, opts Options) *grpc.Server {
	s := grpc.NewServer(grpc.UnaryInterceptor(authInterceptor(opts.Token)))
	Register(s, db, opts)
	return s
}

// Register attaches all cortexdb.v1 services to an existing grpc.Server.
func Register(s *grpc.Server, db *cortexdb.DB, opts Options) {
	rpcv1.RegisterAdminServiceServer(s, &adminService{db: db, dbPath: opts.DBPath})
	rpcv1.RegisterKnowledgeServiceServer(s, &knowledgeService{db: db})
	rpcv1.RegisterMemoryServiceServer(s, &memoryService{db: db})
	rpcv1.RegisterKnowledgeGraphServiceServer(s, &graphService{db: db})
	rpcv1.RegisterGraphRagServiceServer(s, &graphragService{db: db})
	rpcv1.RegisterToolsServiceServer(s, &toolsService{db: db})
}
