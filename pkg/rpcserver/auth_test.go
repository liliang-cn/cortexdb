package rpcserver

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func passthrough(ctx context.Context, req any) (any, error) { return "ok", nil }

func TestAuthInterceptor(t *testing.T) {
	info := &grpc.UnaryServerInfo{FullMethod: "/cortexdb.v1.AdminService/Health"}

	t.Run("no token configured: open", func(t *testing.T) {
		ic := authInterceptor("")
		if _, err := ic(context.Background(), nil, info, passthrough); err != nil {
			t.Fatalf("expected open access, got %v", err)
		}
	})
	t.Run("valid token", func(t *testing.T) {
		ic := authInterceptor("s3cret")
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("authorization", "Bearer s3cret"))
		if _, err := ic(ctx, nil, info, passthrough); err != nil {
			t.Fatalf("expected pass, got %v", err)
		}
	})
	t.Run("missing token", func(t *testing.T) {
		ic := authInterceptor("s3cret")
		_, err := ic(context.Background(), nil, info, passthrough)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want UNAUTHENTICATED, got %v", err)
		}
	})
	t.Run("wrong token", func(t *testing.T) {
		ic := authInterceptor("s3cret")
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("authorization", "Bearer nope"))
		_, err := ic(ctx, nil, info, passthrough)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want UNAUTHENTICATED, got %v", err)
		}
	})
}
