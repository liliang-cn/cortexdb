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

// AuthInterceptorFromSource is AuthInterceptor over the keys src holds at each
// call: with authz.OpenKeySource, a key added to or removed from the key file
// applies without a restart, as in the servers NewWithPolicy builds. A nil
// or zero src refuses every call.
func AuthInterceptorFromSource(src *authz.KeySource, db *cortexdb.DB) grpc.UnaryServerInterceptor {
	return authInterceptorFrom(src, db)
}

// AuthInterceptorWithTools is AuthInterceptorFromSource for a server that
// Register gave Options.ExtraTools: the same tools, so that a call to one is
// authorized by its own line. It refuses a set NewWithPolicy would refuse.
func AuthInterceptorWithTools(src *authz.KeySource, db *cortexdb.DB, tools []Tool) (grpc.UnaryServerInterceptor, error) {
	extras, err := newExtraTools(tools)
	if err != nil {
		return nil, err
	}
	return authInterceptorWith(src, db, extras), nil
}
