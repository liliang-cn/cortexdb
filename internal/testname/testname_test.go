package testname

import (
	"sync"
	"testing"
	"time"
)

// The measurement that started this, run against both generators.
//
// The bare clock is expected to collide; the assertion is only on Nano, so
// this stays green on a machine whose clock really does tick every
// nanosecond — where the old code was accidentally fine and the new code is
// still correct.
func TestNanoIsNeverHandedOutTwice(t *testing.T) {
	const goroutines, each = 64, 2000

	seen := make(chan int64, goroutines*each)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				seen <- Nano()
			}
		}()
	}
	wg.Wait()
	close(seen)

	unique := make(map[int64]struct{}, goroutines*each)
	for n := range seen {
		if _, dup := unique[n]; dup {
			t.Fatalf("Nano handed out %d twice", n)
		}
		unique[n] = struct{}{}
	}
	if len(unique) != goroutines*each {
		t.Fatalf("got %d distinct values, want %d", len(unique), goroutines*each)
	}
}

// What the tests actually did, kept as evidence rather than as an assertion:
// a clock coarser than its unit makes two parallel readers agree.
func TestTheClockThisReplacesCanRepeat(t *testing.T) {
	dups := 0
	const rounds = 20000
	for i := 0; i < rounds; i++ {
		var wg sync.WaitGroup
		var a, b int64
		wg.Add(2)
		go func() { defer wg.Done(); a = time.Now().UnixNano() }()
		go func() { defer wg.Done(); b = time.Now().UnixNano() }()
		wg.Wait()
		if a == b {
			dups++
		}
	}
	t.Logf("time.Now().UnixNano() agreed in %d/%d parallel pairs (%.1f%%)",
		dups, rounds, 100*float64(dups)/float64(rounds))
}

func TestNanoStaysOrderedAndNearTheClock(t *testing.T) {
	before := time.Now().UnixNano()
	var prev int64
	for i := 0; i < 1000; i++ {
		n := Nano()
		if n <= prev {
			t.Fatalf("Nano went backwards: %d after %d", n, prev)
		}
		prev = n
	}
	// The pid seed and the step-by-one path both move it forward; neither may
	// move it anywhere a reader would not recognise as now.
	if drift := time.Duration(prev - before); drift < 0 || drift > time.Second {
		t.Fatalf("Nano drifted %v from the clock", drift)
	}
}
