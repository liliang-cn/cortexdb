// Package httpapi exposes the pkg/cortexdb facade over HTTP/JSON.
//
// It is the same shape as pkg/rpcserver and for the same reason: a request is
// decoded into the facade's own Request struct, handed to *cortexdb.DB, and the
// facade's Response is written back. No retrieval, fusion, ranking or graph
// logic lives here. Two surfaces over one database that each carried a little
// of their own logic would answer the same question differently, and the
// difference would only show up to whoever used both.
//
// The facade types already carry json tags — they are the same structs the MCP
// tools are defined in terms of — so the conversion this package performs is
// mostly the routing, the auth and the error mapping, and very little else.
package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Options configures the HTTP server wrapper.
type Options struct {
	// Token enables bearer-token auth when non-empty, with exactly the
	// semantics of rpcserver.Options.Token. One deployment must not be
	// securable to two different degrees depending on the port.
	Token string
	// DBPath is reported by GET /v1/info. It is a label, not a handle: nothing
	// here opens it, and leaving it empty only makes /v1/info quieter.
	DBPath string
}

// New returns an http.Handler serving the REST API over db.
func New(db *cortexdb.DB, opts Options) http.Handler {
	rt := newRouter()
	registerRoutes(rt, db, opts)
	return withAuth(opts.Token, rt)
}

// withAuth enforces a static bearer token on every route.
// An empty token disables authentication.
func withAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		// Constant-time, so that a token cannot be recovered by timing the
		// refusals — the comparison is the only place the real token is read.
		if !found || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid or missing bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
