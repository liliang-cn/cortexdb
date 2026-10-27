package rpc

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// A client of a shared brain holds one connection for hours and uses it a
// few times an hour. Between two calls the far end can lose that connection
// without a word reaching the client: the brain fails over and its address
// moves to another node, or a connection is dropped somewhere on the path.
// The client then sends its next call down a dead socket and gets
// "connection reset by peer" back — a recall that fails for nothing, since a
// fresh connection would have worked at once.
//
// Two things keep that from reaching the caller. The client pings an idle
// connection, so one that died is found and replaced before a call needs it;
// the server has to allow those pings, or it answers them by hanging up. And
// a call that fails as UNAVAILABLE is made once more: the server never
// returns that code itself, so it always means the connection failed, and the
// second attempt goes out on a new one.

// clientPingInterval is how long a connection sits quiet before the client
// pings it. serverMinPingInterval must stay below it, or the server counts the
// pings as abuse and closes the connection.
const (
	clientPingInterval    = 30 * time.Second
	clientPingTimeout     = 10 * time.Second
	serverMinPingInterval = 20 * time.Second
)

// retryServiceConfig makes every method retry once on UNAVAILABLE.
const retryServiceConfig = `{
	"methodConfig": [{
		"name": [{}],
		"retryPolicy": {
			"maxAttempts": 2,
			"initialBackoff": "0.1s",
			"maxBackoff": "0.5s",
			"backoffMultiplier": 2,
			"retryableStatusCodes": ["UNAVAILABLE"]
		}
	}]
}`

// DialOptions are the connection settings for a client that keeps a
// connection to a brain open between calls.
func DialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                clientPingInterval,
			Timeout:             clientPingTimeout,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultServiceConfig(retryServiceConfig),
		grpc.WithChainUnaryInterceptor(CallInterceptor()),
	}
}

// CallInterceptor sends the request id of the call in ctx, and what the
// call's client said about itself, with every RPC, so the server's record of
// the call carries the id the client logs (observability.Call). A ctx with no
// call sends nothing and the server mints an id of its own, which it returns
// in the response header.
func CallInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if call := observability.CallFrom(ctx); call != nil {
			kv := []string{observability.RequestIDHeader, call.ID()}
			c := call.Client()
			for _, f := range [][2]string{
				{observability.ClientHostHeader, c.Host},
				{observability.ClientProjectHeader, c.Project},
				{observability.ClientAgentHeader, c.Agent},
			} {
				if f[1] != "" {
					kv = append(kv, f[0], observability.EncodeClientValue(f[1]))
				}
			}
			ctx = metadata.AppendToOutgoingContext(ctx, kv...)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// ServerOptions let a brain's server accept the pings DialOptions sends.
func ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             serverMinPingInterval,
			PermitWithoutStream: true,
		}),
	}
}
