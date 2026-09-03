package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// newTestServer serves the REST API over a real temp-file database.
//
// No embedder is wired: lexical retrieval is the path a first-time evaluator
// hits, because nobody has an embedding endpoint configured before their first
// curl. A round trip that only worked with vectors would prove the wrong thing.
func newTestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("httpapi_%d.db", testname.Nano()))
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv := httptest.NewServer(New(db, Options{Token: token, DBPath: dbPath}))
	t.Cleanup(srv.Close)
	return srv
}

// do sends one request and returns both the status and the body. Both, because
// a handler that reports the wrong status with the right body is exactly the
// dishonesty this package is meant not to have.
func do(t *testing.T, srv *httptest.Server, method, path, token, body string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp.StatusCode, payload
}

// mustDo fails unless the call returned want, then decodes the body as JSON.
func mustDo(t *testing.T, srv *httptest.Server, method, path, body string, want int) map[string]any {
	t.Helper()
	status, payload := do(t, srv, method, path, "", body)
	if status != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, status, want, payload)
	}
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("%s %s returned a non-JSON body: %v: %s", method, path, err, payload)
	}
	return out
}

// errorMessage reads the {"error":"..."} body every failure is required to have.
func errorMessage(t *testing.T, payload []byte) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("error body is not JSON: %v: %s", err, payload)
	}
	if out.Error == "" {
		t.Fatalf("error body carries no message: %s", payload)
	}
	return out.Error
}
