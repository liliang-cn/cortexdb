package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// healthProbeTimeout bounds a whole probe: connect, Health, Info.
const healthProbeTimeout = 5 * time.Second

// probeHealth asks a running cortexdb-grpc whether it is serving, and returns
// a one-line description of what answered.
//
// This is what `cortexdb-grpc -health` runs, which is why it lives in the
// server binary rather than in a separate tool: a container HEALTHCHECK and a
// systemd health command then need nothing installed beside the server itself.
// The image stays a static binary with no shell and no probe helper in it.
func probeHealth(ctx context.Context, addr, token string) (string, error) {
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if token != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(bearerInterceptor(token)))
	}
	conn, err := grpc.NewClient(probeAddr(addr), opts...)
	if err != nil {
		return "", fmt.Errorf("connect to cortexdb-grpc at %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()

	client := rpcv1.NewAdminServiceClient(conn)
	health, err := client.Health(ctx, &rpcv1.HealthRequest{})
	if err != nil {
		return "", fmt.Errorf("health %s: %w (is the server up, and does CORTEXDB_GRPC_TOKEN match?)", addr, err)
	}
	if !health.GetOk() {
		return "", fmt.Errorf("health %s: server reported not ok", addr)
	}
	info, err := client.Info(ctx, &rpcv1.InfoRequest{})
	if err != nil {
		return "", fmt.Errorf("info %s: %w", addr, err)
	}
	embedder := "none"
	if info.GetHasEmbedder() {
		embedder = "on"
	}
	return fmt.Sprintf("ok %s v%s db=%s embedder=%s", addr, info.GetVersion(), info.GetDbPath(), embedder), nil
}

// probeAddr turns a listen address into a dial address.
//
// A server configured with CORTEXDB_GRPC_ADDR=0.0.0.0:47821 — which is what a
// container needs — hands the probe a wildcard host it should not dial. The
// probe always runs beside the server, so the loopback interface is the right
// target and the one that works on every platform.
func probeAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return net.JoinHostPort("127.0.0.1", port)
	}
	return addr
}

// bearerInterceptor attaches `authorization: Bearer <token>` to every call,
// matching the server's authInterceptor.
func bearerInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
