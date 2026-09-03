package observability

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// contentType is the classic Prometheus text exposition format. Scrapers
// content-negotiate, but they all accept 0.0.4, and announcing the version is
// what stops a scraper guessing OpenMetrics and then rejecting the body for a
// missing "# EOF" trailer.
const contentType = "text/plain; version=0.0.4; charset=utf-8"

// Handler serves the registry in Prometheus text format. The caller mounts it
// wherever it likes — this package never touches http.DefaultServeMux and never
// opens a listener, because a library that binds a port decides an operator's
// firewall rules for them.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", contentType)
		if req.Method == http.MethodHead {
			return
		}
		// Errors here are a client that hung up mid-scrape. There is nothing to
		// report to and nothing to retry: the next scrape is in fifteen seconds.
		_ = r.WriteText(w)
	})
}

// WriteText renders the whole registry in Prometheus text format. It is not
// called WriteTo because that name belongs to io.WriterTo, whose signature
// returns a byte count nobody here would use.
//
// The rendering is a point-in-time read of atomics rather than a locked-out
// snapshot: two different families can be a few microseconds apart in the same
// scrape. That is the same guarantee every Prometheus client gives, and it is
// the right one — freezing the registry for the length of a scrape would make
// every RPC wait on a monitoring system.
func (r *Registry) WriteText(w io.Writer) error {
	bw := bufio.NewWriter(w)
	for _, f := range r.snapshotFamilies() {
		// HELP and TYPE are emitted even when the family has no series yet, so
		// an operator can see that a metric exists before the first request
		// arrives instead of wondering whether the build has it at all.
		if f.help != "" {
			fmt.Fprintf(bw, "# HELP %s %s\n", f.name, escapeHelp(f.help))
		}
		fmt.Fprintf(bw, "# TYPE %s %s\n", f.name, f.kind)

		for _, s := range f.snapshotSeries() {
			switch f.kind {
			case kindCounter, kindGauge:
				fmt.Fprintf(bw, "%s%s %s\n", f.name, renderLabels(f.labelNames, s.labelValues, "", ""), formatValue(s.value.load()))
			case kindHistogram:
				writeHistogram(bw, f, s)
			}
		}
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return nil
}

func writeHistogram(bw *bufio.Writer, f *family, s *series) {
	// Buckets are cumulative in the exposition format even though they are
	// stored non-cumulatively, so the running total is built here. The final
	// total is written twice — as the +Inf bucket and as _count — which is what
	// guarantees the invariant every scraper assumes, that le="+Inf" equals
	// _count, even while another goroutine is observing.
	var cumulative uint64
	for i, bound := range f.bounds {
		cumulative += s.buckets[i].Load()
		fmt.Fprintf(bw, "%s_bucket%s %d\n",
			f.name, renderLabels(f.labelNames, s.labelValues, "le", formatValue(bound)), cumulative)
	}
	cumulative += s.buckets[len(f.bounds)].Load()
	fmt.Fprintf(bw, "%s_bucket%s %d\n",
		f.name, renderLabels(f.labelNames, s.labelValues, "le", "+Inf"), cumulative)

	labels := renderLabels(f.labelNames, s.labelValues, "", "")
	fmt.Fprintf(bw, "%s_sum%s %s\n", f.name, labels, formatValue(s.sum.load()))
	fmt.Fprintf(bw, "%s_count%s %d\n", f.name, labels, cumulative)
}

// renderLabels builds the {k="v",...} suffix, optionally appending one extra
// pair (used for the histogram's le label, which must come last).
func renderLabels(names, values []string, extraName, extraValue string) string {
	if len(names) == 0 && extraName == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(n)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(values[i]))
		b.WriteByte('"')
	}
	if extraName != "" {
		if len(names) > 0 {
			b.WriteByte(',')
		}
		b.WriteString(extraName)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(extraValue))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// escapeLabelValue escapes the three characters the text format reserves inside
// a quoted label value. Getting this wrong is the classic silent corruption: an
// unescaped newline in a label ends the sample line early, and the scraper reads
// the remainder as a new metric with a nonsense name rather than reporting an
// error against the line that caused it.
func escapeLabelValue(v string) string {
	if !strings.ContainsAny(v, "\\\"\n") {
		return v
	}
	var b strings.Builder
	b.Grow(len(v) + 8)
	for _, c := range v {
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// escapeHelp escapes a HELP line. Unlike a label value it is not quoted, so a
// double quote needs no escape — only the backslash and the newline that would
// otherwise terminate the line.
func escapeHelp(h string) string {
	if !strings.ContainsAny(h, "\\\n") {
		return h
	}
	var b strings.Builder
	b.Grow(len(h) + 8)
	for _, c := range h {
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// formatValue renders a float the way the text format requires. Go prints
// infinities as "+Inf"/"-Inf" and NaN as "NaN" already, which happens to match,
// but relying on that coincidence is how a formatting change becomes a scrape
// failure, so it is spelled out.
func formatValue(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// validMetricName enforces Prometheus' [a-zA-Z_:][a-zA-Z0-9_:]* rule. A name
// that breaks it is not rejected by the scraper with a useful message — the
// whole scrape fails — so it is caught at declaration time instead.
func validMetricName(name string) error {
	if name == "" {
		return fmt.Errorf("metric name is empty")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := c == '_' || c == ':' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(i > 0 && c >= '0' && c <= '9')
		if !ok {
			return fmt.Errorf("invalid metric name %q: %q is not allowed at position %d", name, string(c), i)
		}
	}
	return nil
}

// validLabelName enforces [a-zA-Z_][a-zA-Z0-9_]*, and additionally rejects the
// __ prefix, which Prometheus reserves for its own relabelling machinery: a
// label named __name__ or __address__ is dropped or, worse, rewrites the series.
func validLabelName(name string) error {
	if name == "" {
		return fmt.Errorf("label name is empty")
	}
	if strings.HasPrefix(name, "__") {
		return fmt.Errorf("invalid label name %q: the __ prefix is reserved by Prometheus", name)
	}
	if name == "le" {
		return fmt.Errorf("invalid label name %q: le is reserved for histogram buckets", name)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(i > 0 && c >= '0' && c <= '9')
		if !ok {
			return fmt.Errorf("invalid label name %q: %q is not allowed at position %d", name, string(c), i)
		}
	}
	return nil
}
