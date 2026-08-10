package rpcserver

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
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

// ontologyRejection wraps the sentinel without borrowing its message. Real
// validation errors happen to contain the word "invalid" through the sentinel's
// own text, which the message sniffing in toStatus would match by accident;
// stripping that coincidence means this test can only pass if the errors.Is arm
// is what does the mapping.
type ontologyRejection struct{}

func (ontologyRejection) Error() string { return `object type "Airport" must declare primary_key` }
func (ontologyRejection) Unwrap() error { return cortexdb.ErrInvalidOntology }

func TestToStatusMapsOntologyRejectionToInvalidArgument(t *testing.T) {
	err := toStatus(ontologyRejection{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %s (%v)", status.Code(err), err)
	}
	if got := status.Convert(err).Message(); got != `object type "Airport" must declare primary_key` {
		t.Fatalf("message should reach the caller unchanged, got %q", got)
	}
}
