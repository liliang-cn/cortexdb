package rpcserver

import (
	"google.golang.org/grpc"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// AuthInterceptor is the authorization this package installs in the servers it
// builds — bearer lookup, method clearance, row confinement, per-tool
// classification, SPARQL narrowing and the caller key that ownership checks
// read — as a value a process can install in a grpc.Server of its own.
//
// It is exported for the process that mounts cortexdb.v1 beside other services
// on one listener. That process has one interceptor chain, and the choice it
// faces without this is to rewrite the policy or to drop it; both are worse
// than routing cortexdb.v1 calls through the interceptor that already knows
// what a read is. Register attaches the services; this attaches the policy.
// Together they are NewWithPolicy without the server it constructs.
func AuthInterceptor(keys *authz.KeySet, db *cortexdb.DB) grpc.UnaryServerInterceptor {
	return authInterceptor(keys, db)
}
