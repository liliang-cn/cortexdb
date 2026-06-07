package rpcserver

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatus(t *testing.T) {
	cases := []struct {
		err  error
		want codes.Code
	}{
		{nil, codes.OK},
		{errors.New("memory abc not found"), codes.NotFound},
		{errors.New("knowledge_id is required"), codes.InvalidArgument},
		{errors.New("boom"), codes.Internal},
	}
	for _, c := range cases {
		got := status.Code(toStatus(c.err))
		if got != c.want {
			t.Fatalf("toStatus(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
