package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// statusFor maps a facade error onto an HTTP status.
//
// It is rpcserver.toStatus with a different vocabulary, deliberately: the same
// error must not be a caller's fault on one port and the server's on the other.
// The message sniffing is inherited along with it — the facade returns plain
// errors, and until it returns sentinels this is the honest amount of
// information available.
func statusFor(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, context.Canceled) {
		// 499: the request died with the caller. Reporting it as a 500 would
		// record a fault the server did not have, in a response nobody reads.
		return statusClientClosedRequest
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	// Checked before the sniffing below, which only recognises a few stock
	// phrases and would otherwise report a caller's malformed schema as an
	// internal server fault.
	if errors.Is(err, cortexdb.ErrInvalidOntology) {
		return http.StatusBadRequest
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "not found"):
		return http.StatusNotFound
	case strings.Contains(lower, "is required"),
		strings.Contains(lower, "empty text"),
		strings.Contains(lower, "invalid"),
		// A tool decodes its own arguments, so a wrongly typed field arrives
		// here as an unmarshal error. That is a caller holding the request
		// wrong, not a server that broke.
		strings.Contains(lower, "cannot unmarshal"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// statusClientClosedRequest is nginx's 499. Go has no constant for it because
// it is not in the RFC, but it is the code every log reader already knows.
const statusClientClosedRequest = 499
