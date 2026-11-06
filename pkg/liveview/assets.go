package liveview

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

// withGzip compresses the pages and the JSON the view serves. The graph alone
// is three quarters of a megabyte of JSON on the shared brain, read again on
// every reload; it compresses to a tenth. The event stream is left alone: it
// is flushed event by event, and a compressor would hold events back.
func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) || strings.HasSuffix(r.URL.Path, "/api/stream") || strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/"), "assets/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

var gzipWriters = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression); return w }}

// gzipResponseWriter compresses a body once a handler starts writing one, and
// steps aside for responses that have no body to compress or arrive already
// encoded.
type gzipResponseWriter struct {
	http.ResponseWriter
	zw      *gzip.Writer
	decided bool
	plain   bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	g.decide(code)
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) decide(code int) {
	if g.decided {
		return
	}
	g.decided = true
	h := g.Header()
	if code == http.StatusNoContent || code == http.StatusNotModified || h.Get("Content-Encoding") != "" {
		g.plain = true
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Del("Content-Length")
	g.zw = gzipWriters.Get().(*gzip.Writer)
	g.zw.Reset(g.ResponseWriter)
}

func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	g.decide(http.StatusOK)
	if g.plain {
		return g.ResponseWriter.Write(p)
	}
	return g.zw.Write(p)
}

// Flush lets a handler that streams still reach the client.
func (g *gzipResponseWriter) Flush() {
	if g.zw != nil {
		_ = g.zw.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipResponseWriter) close() {
	if g.zw != nil {
		_ = g.zw.Close()
		gzipWriters.Put(g.zw)
		g.zw = nil
	}
}
