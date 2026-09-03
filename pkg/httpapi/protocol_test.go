package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestAnUnknownFieldIsRefusedRatherThanIgnored(t *testing.T) {
	srv := newTestServer(t, "")

	// top_k spelled the way a caller who has read someone else's client would
	// spell it. Dropped silently, this returns 200 with a default-sized result
	// set and nothing anywhere says the limit was ignored.
	status, payload := do(t, srv, http.MethodPost, "/v1/knowledge/search", "",
		`{"query":"anything","topK":3}`)
	if status != http.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", status, payload)
	}
	if msg := errorMessage(t, payload); !strings.Contains(msg, "topK") {
		t.Fatalf("error %q does not name the field that was wrong", msg)
	}
}

func TestAMalformedBodyIsA400(t *testing.T) {
	srv := newTestServer(t, "")
	status, payload := do(t, srv, http.MethodPost, "/v1/knowledge", "", `{"content": `)
	if status != http.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", status, payload)
	}
	errorMessage(t, payload)
}

// Two JSON documents in one body is a caller sending something it did not mean;
// the decoder reads the first and would otherwise leave the second unmentioned.
func TestTrailingContentAfterTheBodyIsRefused(t *testing.T) {
	srv := newTestServer(t, "")
	status, payload := do(t, srv, http.MethodPost, "/v1/knowledge/search", "",
		`{"query":"one"} {"query":"two"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", status, payload)
	}
}

func TestAnUnknownToolIs404NamingTheTool(t *testing.T) {
	srv := newTestServer(t, "")
	status, payload := do(t, srv, http.MethodPost, "/v1/tools/nope", "", "{}")
	if status != http.StatusNotFound {
		t.Fatalf("= %d, want 404: %s", status, payload)
	}
	if msg := errorMessage(t, payload); !strings.Contains(msg, "nope") {
		t.Fatalf("error %q does not name the tool that was asked for", msg)
	}
}

func TestAnUnknownRouteIs404(t *testing.T) {
	srv := newTestServer(t, "")
	status, payload := do(t, srv, http.MethodGet, "/v1/nothing-here", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("= %d, want 404: %s", status, payload)
	}
	errorMessage(t, payload)
}

func TestTheWrongMethodIsRefusedWithTheOnesThatWork(t *testing.T) {
	srv := newTestServer(t, "")

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/v1/knowledge", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /v1/knowledge = %d, want 405", resp.StatusCode)
	}
	// Without Allow the caller has to guess which verb the route wanted.
	allow := resp.Header.Get("Allow")
	for _, method := range []string{http.MethodDelete, http.MethodGet, http.MethodPost} {
		if !strings.Contains(allow, method) {
			t.Fatalf("Allow = %q, missing %s", allow, method)
		}
	}
}

func TestAToolCalledWithTheWrongMethodIs405(t *testing.T) {
	srv := newTestServer(t, "")
	status, payload := do(t, srv, http.MethodGet, "/v1/tools/ingest_document", "", "")
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("= %d, want 405: %s", status, payload)
	}
}

func TestABodyOverTheSizeLimitIsRefused(t *testing.T) {
	srv := newTestServer(t, "")

	oversized := `{"knowledge_id":"big","content":"` + strings.Repeat("a", maxRequestBytes+1) + `"}`
	status, payload := do(t, srv, http.MethodPost, "/v1/knowledge", "", oversized)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("= %d, want 413: %s", status, payload)
	}
}

// A facade that refuses a request is not a server that broke. The knowledge
// API's own "knowledge_id is required" has to reach the caller as a 400, or
// every validation mistake reads as an outage.
func TestAFacadeRejectionIsA400AndNotA500(t *testing.T) {
	srv := newTestServer(t, "")
	status, payload := do(t, srv, http.MethodPost, "/v1/knowledge", "", `{"content":"x"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", status, payload)
	}
	errorMessage(t, payload)
}

func TestAReadWithNoIdSaysWhichParameterIsMissing(t *testing.T) {
	srv := newTestServer(t, "")
	status, payload := do(t, srv, http.MethodGet, "/v1/knowledge", "", "")
	if status != http.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", status, payload)
	}
	if msg := errorMessage(t, payload); !strings.Contains(msg, "knowledge_id") {
		t.Fatalf("error %q does not name the missing parameter", msg)
	}
}

// Tool arguments that are valid JSON but not an object would reach the tool's
// own json.Unmarshal and come back as a decode failure, which the message
// sniffing below would report as an internal fault. It is a caller's mistake.
func TestToolArgumentsThatAreNotAnObjectAreA400(t *testing.T) {
	srv := newTestServer(t, "")
	status, payload := do(t, srv, http.MethodPost, "/v1/tools/ingest_document", "", `[1,2,3]`)
	if status != http.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", status, payload)
	}
}

func TestStatusForMapsFacadeErrorsOntoHonestCodes(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{errors.New("memory abc not found"), http.StatusNotFound},
		{errors.New("knowledge_id is required"), http.StatusBadRequest},
		{errors.New("json: cannot unmarshal string into Go struct field .top_k of type int"), http.StatusBadRequest},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		if got := statusFor(c.err); got != c.want {
			t.Fatalf("statusFor(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

// ontologyRejection wraps the sentinel without borrowing its wording, so this
// can only pass through the errors.Is arm rather than by accidentally
// containing the word "invalid" — the same trap pkg/rpcserver documents.
type ontologyRejection struct{}

func (ontologyRejection) Error() string { return `object type "Airport" must declare primary_key` }
func (ontologyRejection) Unwrap() error { return cortexdb.ErrInvalidOntology }

func TestAnOntologyRejectionIsA400(t *testing.T) {
	if got := statusFor(ontologyRejection{}); got != http.StatusBadRequest {
		t.Fatalf("statusFor(ontology rejection) = %d, want 400", got)
	}
}
