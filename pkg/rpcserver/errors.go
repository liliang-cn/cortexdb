package rpcserver

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// toStatus maps facade errors onto canonical gRPC codes.
func toStatus(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, err.Error())
	}
	// Checked before the message sniffing below, which only recognises a few
	// stock phrases and would otherwise report a caller's malformed schema as
	// an internal server fault.
	if errors.Is(err, cortexdb.ErrInvalidOntology) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not found"):
		return status.Error(codes.NotFound, msg)
	case strings.Contains(lower, "is required"),
		strings.Contains(lower, "empty text"),
		strings.Contains(lower, "invalid"):
		return status.Error(codes.InvalidArgument, msg)
	default:
		return status.Error(codes.Internal, msg)
	}
}
