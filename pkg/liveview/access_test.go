package liveview

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func gatedView(t *testing.T, token string) (*Server, *httptest.Server) {
	t.Helper()
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A", Type: "entity"}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sv, err := New(ctx, f.source(), 40*time.Millisecond, false, WithAccessToken(token))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	mux := http.NewServeMux()
	mux.Handle("/view/", http.StripPrefix("/view", sv.Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return sv, ts
}

func noRedirects(jar http.CookieJar) *http.Client {
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// A view with a token shows nothing to a browser without it: the page is the
// sign-in form and the APIs answer 401, so the graph never leaves the server.
func TestAGatedViewShowsOnlyTheSignInFormWithoutTheToken(t *testing.T) {
	_, ts := gatedView(t, "s3cret-token")

	page, err := http.Get(ts.URL + "/view/ontology")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusUnauthorized {
		t.Fatalf("page status = %d", page.StatusCode)
	}
	if !strings.Contains(string(body), `name="`+accessField+`"`) || strings.Contains(string(body), "entity:a") {
		t.Fatalf("the page is not the sign-in form: %s", body)
	}

	for _, p := range []string{"/view/api/graph", "/view/api/stream", "/view/api/recalls"} {
		r, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusUnauthorized || strings.Contains(string(b), "entity:a") {
			t.Errorf("%s = %d %s", p, r.StatusCode, b)
		}
	}
}

// Signing in posts the token in the body, gets back a cookie that is not the
// token, and lands on the same page — under the mount prefix — as a GET.
func TestSigningInTradesTheTokenForACookieAndReturnsToThePage(t *testing.T) {
	_, ts := gatedView(t, "s3cret-token")
	jar, _ := cookiejar.New(nil)
	c := noRedirects(jar)

	wrong, err := c.PostForm(ts.URL+"/view/ontology", url.Values{accessField: {"nope"}})
	if err != nil {
		t.Fatal(err)
	}
	wb, _ := io.ReadAll(wrong.Body)
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized || !strings.Contains(string(wb), "not right") {
		t.Fatalf("a wrong token: %d %s", wrong.StatusCode, wb)
	}

	ok, err := c.PostForm(ts.URL+"/view/ontology?lang=zh", url.Values{accessField: {"s3cret-token"}})
	if err != nil {
		t.Fatal(err)
	}
	ok.Body.Close()
	if ok.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign-in status = %d", ok.StatusCode)
	}
	loc, _ := ok.Location()
	if loc.Path != "/view/ontology" || loc.RawQuery != "lang=zh" {
		t.Fatalf("sign-in sends the browser to %s", loc)
	}
	var cookie *http.Cookie
	for _, ck := range ok.Cookies() {
		if ck.Name == accessCookie {
			cookie = ck
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.Value == "s3cret-token" || strings.Contains(cookie.Value, "s3cret") {
		t.Fatalf("session cookie = %+v", cookie)
	}

	g, err := c.Get(ts.URL + "/view/api/graph")
	if err != nil {
		t.Fatal(err)
	}
	gb, _ := io.ReadAll(g.Body)
	g.Body.Close()
	if g.StatusCode != http.StatusOK || !strings.Contains(string(gb), "entity:a") {
		t.Fatalf("signed in, the graph = %d %s", g.StatusCode, gb)
	}
}

// A script sends the token as a bearer header; a wrong one is refused.
func TestAScriptReachesAGatedViewWithABearerToken(t *testing.T) {
	_, ts := gatedView(t, "s3cret-token")
	for token, want := range map[string]int{"s3cret-token": http.StatusOK, "s3cret-tokeN": http.StatusUnauthorized} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/view/api/graph", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != want {
			t.Errorf("bearer %q = %d, want %d", token, r.StatusCode, want)
		}
	}
}

// Changing the token signs every browser out: the cookie is bound to it.
func TestACookieFromAnOldTokenDoesNotOpenTheView(t *testing.T) {
	_, ts := gatedView(t, "new-token")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/view/api/graph", nil)
	req.AddCookie(&http.Cookie{Name: accessCookie, Value: sessionValue("old-token")})
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old cookie = %d", r.StatusCode)
	}
}

// Behind an HTTPS proxy the cookie is Secure.
func TestTheSessionCookieIsSecureBehindAnHTTPSProxy(t *testing.T) {
	_, ts := gatedView(t, "s3cret-token")
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/view/", strings.NewReader(url.Values{accessField: {"s3cret-token"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	r, err := noRedirects(nil).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if len(r.Cookies()) != 1 || !r.Cookies()[0].Secure {
		t.Fatalf("cookies = %+v", r.Cookies())
	}
	if loc, _ := r.Location(); loc.Path != "/view/" {
		t.Fatalf("sign-in at the root sends the browser to %s", loc)
	}
}

// A view served past this machine without a token is refused before it binds.
func TestStartAtRefusesAPublicAddressWithoutAToken(t *testing.T) {
	f := &fakeSource{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if _, err := StartAt(ctx, f.source(), "0.0.0.0:0", time.Second, false); err == nil || !strings.Contains(err.Error(), "access token") {
		t.Fatalf("StartAt without a token on 0.0.0.0 = %v", err)
	}
	sv, err := StartAt(ctx, f.source(), "127.0.0.1:0", time.Second, false)
	if err != nil {
		t.Fatalf("StartAt on loopback: %v", err)
	}
	_ = sv.Close()
	sv, err = StartAt(ctx, f.source(), "0.0.0.0:0", time.Second, false, WithAccessToken("t"))
	if err != nil {
		t.Fatalf("StartAt with a token: %v", err)
	}
	r, err := http.Get(strings.Replace(sv.URL(), "0.0.0.0", "127.0.0.1", 1) + "/api/graph")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	_ = sv.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a token-gated listener answered %d without the token", r.StatusCode)
	}
}
