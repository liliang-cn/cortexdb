package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
)

// registeredRoutes is every route the REST API actually serves, read off the
// router that New builds rather than off a list written beside it.
//
// registerRoutes wants a live facade for the toolbox it registers, so this
// opens a real temp database — the same one the round-trip tests use. What it
// must not do is enumerate the routes any other way: the point of these tests
// is that they see what the server serves, not what somebody remembered to say
// it serves.
func registeredRoutes(t *testing.T) []route {
	t.Helper()
	rt := newRouter()
	db, _ := newTestDB(t)
	registerRoutes(rt, db, Options{})
	got := rt.registered()
	if len(got) == 0 {
		t.Fatal("the router registered no routes; these tests would pass vacuously")
	}
	return got
}

// TestEveryRegisteredRouteIsClassifiedAsAReadOrAWrite is the test that keeps
// the route table honest, and it is the reason the REST port can be served
// beside a confined gRPC port at all. Classifying by verb — POST is a write,
// say — is wrong on this API's first two POSTs, and the next endpoint somebody
// adds is exactly the one nobody will think to classify. So the table is
// explicit and this walks what the router really has to prove nothing escaped
// it.
func TestEveryRegisteredRouteIsClassifiedAsAReadOrAWrite(t *testing.T) {
	for _, rr := range registeredRoutes(t) {
		m, ok := lookupRoute(rr)
		if !ok {
			t.Errorf("%s is served but not classified; add it to routeAccess in access.go "+
				"as a read or a write — an unclassified route is denied to every key", rr)
			continue
		}
		if m.Access != authz.Read && m.Access != authz.Write {
			t.Errorf("%s is in the table with access %s", rr, m.Access)
		}
	}
}

// TestEveryClassifiedRouteIsStillRegistered catches the other drift: an entry
// left behind after a route was renamed or dropped, which would otherwise sit
// in the table looking like coverage it no longer provides.
func TestEveryClassifiedRouteIsStillRegistered(t *testing.T) {
	served := make(map[route]struct{})
	for _, rr := range registeredRoutes(t) {
		served[rr] = struct{}{}
	}
	for _, rr := range classifiedRoutes() {
		if _, ok := served[rr]; !ok {
			t.Errorf("%s is classified but no longer registered; drop it from routeAccess", rr)
		}
	}
}

// TestTheRegisteredRouteListLooksLikeTheWholeAPI guards against the enumeration
// silently shrinking. If registerRoutes lost half its endpoints, or if
// registered() stopped reporting the tool route, the two tests above would
// still pass with nothing left to check.
func TestTheRegisteredRouteListLooksLikeTheWholeAPI(t *testing.T) {
	got := registeredRoutes(t)
	seen := make(map[route]struct{}, len(got))
	for _, rr := range got {
		seen[rr] = struct{}{}
	}
	for _, want := range []route{
		{http.MethodGet, "/v1/health"},
		{http.MethodPost, "/v1/knowledge"},
		{http.MethodDelete, "/v1/memory"},
		{http.MethodPost, "/v1/graph/sparql"},
		{http.MethodGet, "/v1/tools"},
		// The dynamic endpoint. It has no entry in rt.routes, so if
		// registered() ever stopped synthesising it the whole toolbox would go
		// unclassified without a single test noticing.
		{http.MethodPost, toolRoutePath},
	} {
		if _, ok := seen[want]; !ok {
			t.Errorf("%s is not registered on the router", want)
		}
	}
	if len(got) < 15 {
		t.Errorf("the router reports only %d routes; the API has more than that", len(got))
	}
	for _, rr := range got {
		if !strings.HasPrefix(rr.path, "/v1/") {
			t.Errorf("%s is outside /v1; the classification table assumes the whole API lives there", rr)
		}
	}
}
