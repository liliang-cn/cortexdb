// Package httpapi exposes the pkg/cortexdb facade over HTTP/JSON.
//
// It is the same shape as pkg/rpcserver and for the same reason: a request is
// decoded into the facade's own Request struct, handed to *cortexdb.DB, and the
// facade's Response is written back. No retrieval, fusion, ranking or graph
// logic lives here. Two surfaces over one database that each carried a little
// of their own logic would answer the same question differently, and the
// difference would only show up to whoever used both.
//
// The same goes for who may ask. This package enforces pkg/authz's key policy —
// scoped keys, clearances and row confinement — against an explicit table of
// route classifications, so a key that is confined over gRPC is confined here
// in the same words. A REST port that ignored the key file would be a door that
// ignores the lock beside it.
//
// The facade types already carry json tags — they are the same structs the MCP
// tools are defined in terms of — so the conversion this package performs is
// mostly the routing, the auth and the error mapping, and very little else.
package httpapi

import (
	"fmt"
	"net/http"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Options configures the HTTP server wrapper. The three policy fields are
// rpcserver.Options' three, with the same precedence, because one deployment
// must not be securable to two different degrees depending on the port.
type Options struct {
	// Token enables bearer-token auth when non-empty. It grants unconfined
	// read/write, which is what it has always meant; KeyFile is how a
	// deployment gets anything narrower.
	Token string
	// KeyFile is the path to a JSON file of scoped API keys. When set it is
	// the entire policy and Token is ignored — see authz.Resolve for why
	// honouring both would leave the environment variable as a master key
	// outranking every scope in the file.
	KeyFile string
	// Keys is a pre-loaded policy, for callers that build one in process.
	// It takes precedence over Token and KeyFile.
	Keys *authz.KeySet
	// DBPath is reported by GET /v1/info. It is a label, not a handle: nothing
	// here opens it, and leaving it empty only makes /v1/info quieter.
	DBPath string
}

// New returns an http.Handler serving the REST API over db.
//
// A key file that cannot be loaded yields a handler that refuses every request
// and says why, rather than one that serves them unprotected. New is kept
// error-free because it is the signature every existing caller uses; callers
// that want the load failure in their hands call NewWithPolicy.
func New(db *cortexdb.DB, opts Options) http.Handler {
	h, err := NewWithPolicy(db, opts)
	if err != nil {
		return refuseAll(err)
	}
	return h
}

// NewWithPolicy is New, reporting a key-file load failure to the caller.
func NewWithPolicy(db *cortexdb.DB, opts Options) (http.Handler, error) {
	keys := opts.Keys
	if keys == nil {
		var err error
		keys, err = authz.Resolve(opts.KeyFile, opts.Token)
		if err != nil {
			return nil, err
		}
	}
	rt := newRouter()
	registerRoutes(rt, db, opts)
	// Every registered handler is wrapped with the checks for its own route,
	// so a route cannot be served without one. The 404 and 405 answers stay
	// inside the router, unwrapped: a path nobody registered is not an
	// authorisation failure and should not be reported as one.
	rt.wrapEach(authorizeWith(db))
	return authenticate(keys, rt), nil
}

// refuseAll is what a server does when it has no policy it can trust: say why,
// on every request, rather than serve anything. 503 rather than 500 because the
// server is not broken — it is unwilling to run without the policy it was told
// to enforce, and that is a condition an operator fixes and retries.
func refuseAll(cause error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusServiceUnavailable,
			fmt.Sprintf("api key policy unavailable: %v", cause))
	})
}
