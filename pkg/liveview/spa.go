package liveview

// Serving the single-page app.
//
// The frontend (pkg/liveview/ui: Vite, React, TypeScript, Tailwind, shadcn) is
// built into pkg/liveview/dist, which is committed and embedded here, so a
// `go install` user needs no Node. Go serves three things from it and nothing
// else: the index, the hashed build files, and — as 404s — everything under
// /api/ that no route claimed.
//
//   - The index is never cached, and carries the one tag the server rewrites:
//     the <base> marked data-cortexdb-base. The view can be mounted under a
//     prefix of another mux (http.StripPrefix and a trailing slash); the
//     browser resolves every URL the app uses against that base, so telling it
//     the prefix is the whole of supporting one.
//   - Build files are named by a hash of their content, so they are cached for
//     good (immutable) and compressed once.
//   - The app's own routes (/, /changes, /entity, /activity, /recalls,
//     /library, /ontology, /ontology-history) all answer with the index and
//     the router takes it from there. The last four were pages of their own,
//     written into Go strings, until the app took them over; their paths and
//     query options are unchanged, so a bookmark or a link from a tool's
//     answer (serve_graph_3d's library_url, /recalls?trace=) still lands.

import (
	"bytes"
	"compress/gzip"
	"embed"
	"html"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
)

//go:embed all:dist
var distFiles embed.FS

// embeddedUI is the built app. A seam rather than a package variable read
// directly, so a test serves a tiny app of its own and does not depend on
// whatever the real build last produced.
func embeddedUI() fs.FS {
	sub, err := fs.Sub(distFiles, "dist")
	if err != nil {
		panic("cortexdb/liveview: dist is not embedded: " + err.Error())
	}
	return sub
}

// baseTag finds the one tag of the index the server rewrites: the <base> that
// carries the data-cortexdb-base marker. It is found by the marker rather than
// by a literal spelling because the spelling is the build tool's to choose —
// Vite writes the tag back self-closed (`<base href="/" data-cortexdb-base />`),
// and a literal match that missed it would leave a view mounted under a prefix
// asking the root for its bundle and its API.
var baseTag = regexp.MustCompile(`<base\b[^>]*\bdata-cortexdb-base\b[^>]*>`)

// rebase points the index's base tag at the mount prefix.
func rebase(index []byte, prefix string) []byte {
	loc := baseTag.FindIndex(index)
	if loc == nil {
		return index
	}
	tag := `<base href="` + html.EscapeString(prefix) + `/" data-cortexdb-base>`
	out := make([]byte, 0, len(index)+len(prefix))
	out = append(out, index[:loc[0]]...)
	out = append(out, tag...)
	return append(out, index[loc[1]:]...)
}

// spaRoutes are the app's own paths. Each answers with the index.
var spaRoutes = map[string]bool{
	"/": true, "/changes": true, "/entity": true, "/activity": true,
	"/recalls": true, "/library": true, "/ontology": true, "/ontology-history": true,
}

// mountPrefix is what a parent mux stripped from the request's path before the
// view saw it: the part of the original path that comes before r.URL.Path. At
// the root it is "".
func mountPrefix(r *http.Request) string {
	orig := r.RequestURI
	if u, err := url.ParseRequestURI(orig); err == nil {
		orig = u.EscapedPath()
	}
	cur := r.URL.EscapedPath()
	if cur != "" && strings.HasSuffix(orig, cur) {
		return strings.TrimSuffix(orig, cur)
	}
	return ""
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if !spaRoutes[r.URL.Path] {
		http.NotFound(w, r)
		return
	}
	if !readOnly(w, r) {
		return
	}
	index, err := fs.ReadFile(s.ui, "index.html")
	if err != nil {
		http.Error(w, "the live view's frontend is not built into this binary", http.StatusNotFound)
		return
	}
	index = rebase(index, mountPrefix(r))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A rebuilt binary must never be shadowed by a stale index in the browser:
	// the index is the one file that names the hashed bundle to load.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(index)
}

// assetCache holds each build file compressed once.
type assetCache struct{ gz sync.Map }

func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	clean := path.Clean("/" + name)[1:]
	// No dotfiles (the .keep that holds the directory in git) and nothing that
	// escapes the directory; fs.ReadFile would refuse the latter anyway.
	if clean == "" || clean != name || strings.HasPrefix(path.Base(clean), ".") {
		http.NotFound(w, r)
		return
	}
	body, err := fs.ReadFile(s.ui, "assets/"+clean)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctype := mime.TypeByExtension(path.Ext(clean))
	if ctype == "" {
		ctype = http.DetectContentType(body)
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Vary", "Accept-Encoding")
	if acceptsGzip(r) && compressible(ctype) {
		if packed, ok := s.assets.gz.Load(clean); ok {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(packed.([]byte))
			return
		}
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(body)
		_ = zw.Close()
		// Keyed by name alone: a name carries a hash of its content, so one
		// name is one body for the life of the process.
		s.assets.gz.Store(clean, buf.Bytes())
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(buf.Bytes())
		return
	}
	_, _ = w.Write(body)
}

// compressible says whether a content type is text, which is the only kind
// worth compressing (images and fonts arrive compressed already).
func compressible(ctype string) bool {
	ctype, _, _ = strings.Cut(ctype, ";")
	switch {
	case strings.HasPrefix(ctype, "text/"):
		return true
	}
	switch ctype {
	case "application/javascript", "application/json", "image/svg+xml", "application/xml", "application/wasm":
		return true
	}
	return false
}
