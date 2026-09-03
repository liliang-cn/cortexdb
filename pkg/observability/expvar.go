package observability

import (
	"expvar"
	"fmt"
)

// Snapshot renders the registry as plain Go values, which is what the expvar
// view is built from and what a test can assert against without parsing text.
//
// A family with no labels maps straight to its value; a family with labels maps
// to one entry per series, keyed by the same {name="value"} rendering the
// Prometheus output uses, so the two views are recognisably the same numbers.
func (r *Registry) Snapshot() map[string]any {
	out := make(map[string]any)
	for _, f := range r.snapshotFamilies() {
		all := f.snapshotSeries()
		if len(f.labelNames) == 0 {
			if len(all) == 0 {
				continue
			}
			out[f.name] = seriesSnapshot(f, all[0])
			continue
		}
		byLabels := make(map[string]any, len(all))
		for _, s := range all {
			byLabels[renderLabels(f.labelNames, s.labelValues, "", "")] = seriesSnapshot(f, s)
		}
		out[f.name] = byLabels
	}
	return out
}

func seriesSnapshot(f *family, s *series) any {
	if f.kind != kindHistogram {
		return s.value.load()
	}
	buckets := make(map[string]uint64, len(f.bounds)+1)
	var cumulative uint64
	for i, bound := range f.bounds {
		cumulative += s.buckets[i].Load()
		buckets[formatValue(bound)] = cumulative
	}
	cumulative += s.buckets[len(f.bounds)].Load()
	buckets["+Inf"] = cumulative
	return map[string]any{
		"count":   cumulative,
		"sum":     s.sum.load(),
		"buckets": buckets,
	}
}

// ExpvarVar returns the registry as an expvar.Var. It is an expvar.Func, so the
// numbers are read at the moment /debug/vars is fetched rather than copied at
// publication time — nothing polls, and an unread registry costs nothing.
func (r *Registry) ExpvarVar() expvar.Var {
	return expvar.Func(func() any { return r.Snapshot() })
}

// PublishExpvar publishes the registry under name so it appears on /debug/vars.
//
// expvar.Publish panics on a duplicate name, which would turn a second Open of
// the same process — the normal shape of a test — into a crash, so the name is
// checked first and a duplicate is returned as an error the caller can ignore
// or log.
//
// Note that importing expvar at all registers /debug/vars on
// http.DefaultServeMux, a side effect of the standard library that this package
// inherits and cannot opt out of. A server that does not want the default mux
// exposed should serve expvar.Handler() on a mux of its own and not use
// DefaultServeMux, which is what cmd/ does.
func (r *Registry) PublishExpvar(name string) error {
	if expvar.Get(name) != nil {
		return fmt.Errorf("observability: expvar name %q is already published", name)
	}
	expvar.Publish(name, r.ExpvarVar())
	return nil
}
