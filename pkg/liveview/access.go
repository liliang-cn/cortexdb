package liveview

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"path"
	"strings"
	"time"
)

// WithAccessToken puts the view behind a token. Without it the view answers
// anyone who can reach it, which is right on 127.0.0.1 and wrong anywhere
// else: the pages show every memory in the brain.
//
// A browser gets a small form asking for the token. The token is posted in
// the request body, never in the URL, so it does not end up in a proxy's
// access log or the browser's history, and is traded for an HttpOnly cookie
// that lasts a year. A script sends "Authorization: Bearer <token>" instead.
//
// The cookie holds an HMAC of the token, not the token: changing the token
// signs every browser out.
func WithAccessToken(token string) Option {
	return func(o *options) { o.token = token }
}

const (
	accessCookie = "cortexdb_view"
	// accessField is the form field the sign-in form posts. Named so it
	// cannot be mistaken for a field of any page the view serves.
	accessField   = "cortexdb_view_token"
	accessLasts   = 365 * 24 * time.Hour
	sessionDomain = "cortexdb live view session"
)

func accessToken(opts []Option) string {
	var o options
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o.token
}

func sessionValue(token string) string {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(sessionDomain))
	return hex.EncodeToString(m.Sum(nil))
}

func sameSecret(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// gate wraps the view's routes in the token check; with no token it is the
// routes unchanged.
func gate(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	session := sessionValue(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(accessCookie); err == nil && sameSecret(c.Value, session) {
			next.ServeHTTP(w, r)
			return
		}
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") && sameSecret(strings.TrimPrefix(auth, "Bearer "), token) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost && !isAPI(r.URL.Path) {
			if err := r.ParseForm(); err == nil && r.PostForm.Has(accessField) {
				if sameSecret(r.PostForm.Get(accessField), token) {
					http.SetCookie(w, &http.Cookie{
						Name:     accessCookie,
						Value:    session,
						Path:     "/",
						MaxAge:   int(accessLasts / time.Second),
						HttpOnly: true,
						Secure:   servedOverTLS(r),
						SameSite: http.SameSiteLaxMode,
					})
					// Back to the same page as a GET. The Location is relative
					// so it stays right when the view is mounted under a prefix
					// the handler never sees — set by hand, because
					// http.Redirect makes it absolute against the stripped path.
					w.Header().Set("Location", backToPage(r.URL.Path, r.URL.RawQuery))
					w.WriteHeader(http.StatusSeeOther)
					return
				}
				denyPage(w, http.StatusUnauthorized, true)
				return
			}
		}
		if isAPI(r.URL.Path) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "this view needs its access token"})
			return
		}
		denyPage(w, http.StatusUnauthorized, false)
	})
}

func isAPI(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/")
}

// servedOverTLS reports whether the browser reached the view over HTTPS,
// directly or through a proxy that says so.
func servedOverTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// backToPage is a relative reference to the page at p, resolved by the
// browser against the URL it posted to.
func backToPage(p, rawQuery string) string {
	ref := "./"
	if p != "" && !strings.HasSuffix(p, "/") {
		ref = path.Base(p)
	}
	if rawQuery != "" {
		ref += "?" + rawQuery
	}
	return ref
}

var signInPage = template.Must(template.New("signin").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>CortexDB</title>
<style>
:root { color-scheme: light; --bg: #f6f7f9; --card: #fff; --fg: #111827; --muted: #6b7280; --line: #e5e7eb; --accent: #4f46e5; --bad: #b91c1c; }
@media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) { color-scheme: dark; --bg: #0f1115; --card: #171a21; --fg: #e5e7eb; --muted: #9ca3af; --line: #2a2f3a; --accent: #818cf8; --bad: #f87171; } }
* { box-sizing: border-box; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: var(--bg); color: var(--fg); font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; padding: 16px; }
form { width: 100%; max-width: 360px; background: var(--card); border: 1px solid var(--line); border-radius: 12px; padding: 28px 24px; }
h1 { margin: 0 0 4px; font-size: 18px; }
p { margin: 0 0 20px; color: var(--muted); font-size: 14px; }
label { display: block; font-size: 13px; margin-bottom: 6px; }
input { width: 100%; padding: 9px 11px; border: 1px solid var(--line); border-radius: 8px; background: var(--card); color: var(--fg); font: inherit; }
input:focus { outline: 2px solid var(--accent); outline-offset: 1px; }
button { margin-top: 16px; width: 100%; padding: 9px; border: 0; border-radius: 8px; background: var(--accent); color: #fff; font: inherit; font-weight: 600; cursor: pointer; }
.bad { color: var(--bad); margin: 12px 0 0; font-size: 13px; }
</style>
</head>
<body>
<form method="post" autocomplete="on">
<h1>CortexDB</h1>
<p>This live view is private. Enter its access token. · 这个视图需要访问令牌。</p>
<label for="t">Access token · 访问令牌</label>
<input id="t" name="` + accessField + `" type="password" autocomplete="current-password" required autofocus>
<button type="submit">Open · 打开</button>
{{if .Wrong}}<p class="bad" role="alert">That token is not right. · 令牌不对。</p>{{end}}
</form>
</body>
</html>
`))

func denyPage(w http.ResponseWriter, status int, wrong bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = signInPage.Execute(w, struct{ Wrong bool }{wrong})
}
