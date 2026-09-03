package observability

import (
	"fmt"
	"io"
	"sync"
	"testing"
)

// TestConcurrentRecordersProduceExactCounts is the -race test. The registry is
// written from every RPC, so the interesting failure is not a lost update under
// contention but a torn read of a float64 or a concurrent map write during the
// first sighting of a label combination.
func TestConcurrentRecordersProduceExactCounts(t *testing.T) {
	const (
		goroutines = 64
		iterations = 500
		methods    = 8
	)

	r := NewRegistry()
	requests := r.NewCounter("cortexdb_requests_total", "Requests received.", "method")
	inFlight := r.NewGauge("cortexdb_requests_in_flight", "Requests in flight.", "method")
	latency := r.NewHistogram("cortexdb_latency_seconds", "Handler latency.", []float64{0.01, 0.1, 1}, "method")

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				method := fmt.Sprintf("Method%d", (g+i)%methods)
				requests.Inc(method)
				gauge := inFlight.With(method)
				gauge.Inc()
				latency.Observe(0.05, method)
				gauge.Dec()
			}
		}(g)
	}

	// Scraping while the writers run is the real deployment: Prometheus does not
	// wait for a quiet moment. This also exercises snapshotFamilies against
	// concurrent first-sightings creating new series.
	stop := make(chan struct{})
	scraped := make(chan struct{})
	go func() {
		defer close(scraped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = r.WriteText(io.Discard)
			_ = r.Snapshot()
		}
	}()

	wg.Wait()
	close(stop)
	<-scraped

	var total float64
	for i := 0; i < methods; i++ {
		method := fmt.Sprintf("Method%d", i)
		total += requests.With(method).Value()

		if got := inFlight.With(method).Value(); got != 0 {
			t.Errorf("in-flight gauge for %s = %v, want 0 — an Inc lost its Dec", method, got)
		}
		h := latency.With(method)
		if h.Count() != uint64(requests.With(method).Value()) {
			t.Errorf("histogram count for %s = %d, want %v", method, h.Count(), requests.With(method).Value())
		}
	}
	if want := float64(goroutines * iterations); total != want {
		t.Fatalf("total requests = %v, want %v", total, want)
	}
}

// TestConcurrentFirstSightingsCreateOneSeries checks the double-checked create
// path: many goroutines racing to record the same brand-new label combination
// must end up sharing one series, not each incrementing a copy that the last
// writer overwrites.
func TestConcurrentFirstSightingsCreateOneSeries(t *testing.T) {
	const goroutines = 128

	r := NewRegistry()
	c := r.NewCounter("cortexdb_requests_total", "Requests received.", "method")

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			c.Inc("Search")
		}()
	}
	close(start)
	wg.Wait()

	if got := c.With("Search").Value(); got != goroutines {
		t.Fatalf("counter = %v, want %d", got, goroutines)
	}
	c.f.mu.RLock()
	n := len(c.f.series)
	c.f.mu.RUnlock()
	if n != 1 {
		t.Fatalf("family holds %d series, want 1", n)
	}
}
