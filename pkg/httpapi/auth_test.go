package httpapi

import (
	"net/http"
	"testing"
)

func TestAServerWithATokenRefusesEverythingWithoutIt(t *testing.T) {
	srv := newTestServer(t, "s3cret")

	t.Run("no header at all", func(t *testing.T) {
		status, payload := do(t, srv, http.MethodGet, "/v1/health", "", "")
		if status != http.StatusUnauthorized {
			t.Fatalf("= %d, want 401: %s", status, payload)
		}
		errorMessage(t, payload)
	})

	t.Run("the wrong token", func(t *testing.T) {
		status, payload := do(t, srv, http.MethodGet, "/v1/health", "nope", "")
		if status != http.StatusUnauthorized {
			t.Fatalf("= %d, want 401: %s", status, payload)
		}
	})

	t.Run("the right token", func(t *testing.T) {
		status, payload := do(t, srv, http.MethodGet, "/v1/health", "s3cret", "")
		if status != http.StatusOK {
			t.Fatalf("= %d, want 200: %s", status, payload)
		}
	})
}

// Health is behind the token as well. gRPC's interceptor covers AdminService
// like everything else, and a REST surface that left /v1/health open would mean
// one deployment answered differently depending on which port was asked.
func TestHealthIsNotAnExceptionToTheToken(t *testing.T) {
	srv := newTestServer(t, "s3cret")
	if status, payload := do(t, srv, http.MethodGet, "/v1/health", "", ""); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated health = %d, want 401: %s", status, payload)
	}
}

func TestAServerWithNoTokenAsksForNone(t *testing.T) {
	srv := newTestServer(t, "")
	if status, payload := do(t, srv, http.MethodGet, "/v1/health", "", ""); status != http.StatusOK {
		t.Fatalf("= %d, want 200 when auth is disabled: %s", status, payload)
	}
}

// A 401 has to say which scheme it wanted, or a client is guessing.
func TestARefusalNamesTheSchemeItExpected(t *testing.T) {
	srv := newTestServer(t, "s3cret")
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q, want Bearer", got)
	}
}

// The prefix is exact. "bearer x" and a bare "s3cret" are not the credential
// the gRPC side accepts either, and quietly accepting them here would make the
// two surfaces disagree about what a valid request looks like.
func TestOnlyTheBearerPrefixIsAccepted(t *testing.T) {
	srv := newTestServer(t, "s3cret")
	for _, header := range []string{"s3cret", "bearer s3cret", "Basic s3cret", "Bearer  s3cret"} {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/health", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Authorization", header)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Authorization %q = %d, want 401", header, resp.StatusCode)
		}
	}
}
