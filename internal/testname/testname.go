// Package testname hands out the unique numbers that tests use to name the
// databases, schemas and files they create.
//
// It exists because time.Now().UnixNano() is not unique and the tests assumed
// it was. The name says nanoseconds; the clock underneath does not have to
// tick that fast, and on darwin/arm64 it ticks once per microsecond. Two
// goroutines that read it at the same moment get the same number about three
// times in four:
//
//	identical UnixNano in 151446/200000 parallel pairs (75.723%)
//	smallest observed tick: 1000ns
//
// So `fmt.Sprintf("test_evaluation_lexical_%d.db", time.Now().UnixNano())` in
// two parallel tests is not two databases. It is one file opened by two
// connection pools, and the loser reports
//
//	failed to enable foreign keys: database is locked (5) (SQLITE_BUSY)
//
// at Open, before the test has done anything — which reads like a product
// fault in a store that is working perfectly.
//
// Nano is a drop-in for the call it replaces: still an int64, still ordered,
// still roughly the wall clock, but never handed out twice in one process.
package testname

import (
	"os"
	"sync/atomic"
	"time"
)

// last is the highest number handed out so far.
//
// Seeded with the pid as well as the clock so that two test binaries — and
// `go test ./...` runs packages in parallel — cannot both start their sequence
// at the same value. The offset is under a tenth of a millisecond, which no
// caller of this can notice.
var last atomic.Int64

func init() {
	last.Store(time.Now().UnixNano() + int64(os.Getpid()))
}

// Nano returns a number that no other caller in this process will receive.
//
// It follows the clock when the clock is ahead and steps by one when it is
// not, so a sequence of calls stays ordered and stays close to the real time —
// which is what makes it readable in a leftover filename.
func Nano() int64 {
	for {
		prev := last.Load()
		next := time.Now().UnixNano()
		if next <= prev {
			next = prev + 1
		}
		if last.CompareAndSwap(prev, next) {
			return next
		}
	}
}
