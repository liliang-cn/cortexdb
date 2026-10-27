package observability

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Where one request's time went, by stage.
//
// The metrics say how long each method takes on the whole; they cannot say why
// one Recall took 24 seconds while the one before took 0.7 — and that one was
// the embedder on another machine waiting for room to load. A request gets a
// Stages in its context; embedding, vector search, keyword search and graph
// walks add what they took to it; the request log prints it with the call.

// Stages accumulates the time a request spent in each named stage.
type Stages struct {
	mu    sync.Mutex
	order []string
	dur   map[string]time.Duration
	count map[string]int
}

type stagesKey struct{}

// WithStages gives a request a stage collector.
func WithStages(ctx context.Context) (context.Context, *Stages) {
	s := &Stages{dur: map[string]time.Duration{}, count: map[string]int{}}
	return context.WithValue(ctx, stagesKey{}, s), s
}

// StagesFrom returns the request's collector, or nil.
func StagesFrom(ctx context.Context) *Stages {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(stagesKey{}).(*Stages)
	return s
}

// Time starts timing a stage of the request in ctx; call the result when it
// is done. Free when the request has no collector.
func Time(ctx context.Context, stage string) func() {
	s := StagesFrom(ctx)
	if s == nil {
		return func() {}
	}
	start := time.Now()
	return func() { s.Add(stage, time.Since(start)) }
}

// Add records d against stage.
func (s *Stages) Add(stage string, d time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dur[stage]; !ok {
		s.order = append(s.order, stage)
	}
	s.dur[stage] += d
	s.count[stage]++
}

// Names is the stages in the order they were first timed.
func (s *Stages) Names() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

// Millis is each stage's total in milliseconds.
func (s *Stages) Millis() map[string]int64 {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.dur))
	for k, v := range s.dur {
		out[k] = v.Milliseconds()
	}
	return out
}

// String is "embed=23.90s vector=0.04s×2", in the order the stages first ran.
func (s *Stages) String() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	names := append([]string(nil), s.order...)
	if len(names) == 0 {
		return ""
	}
	parts := make([]string, 0, len(names))
	for _, n := range names {
		p := fmt.Sprintf("%s=%.2fs", n, s.dur[n].Seconds())
		if c := s.count[n]; c > 1 {
			p += fmt.Sprintf("×%d", c)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}
