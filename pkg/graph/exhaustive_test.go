package graph

import "os"

// exhaustiveRuns picks how many random cases a property test runs.
//
// The full counts are what proved the inference-maintenance and change-feed
// targets — a thousand-plus random sequences per backend — and they are worth
// re-running whenever that code changes: set CORTEXDB_EXHAUSTIVE=1. They are
// not worth running on every push. Under -race they took this package past
// go test's ten-minute default, and CI runs exactly that, so the suite failed
// on a timeout rather than on anything it found. The routine counts still
// cover hundreds of sequences each; they catch a broken rule the same way,
// only with less chance of hitting a rare interleaving.
func exhaustiveRuns(full, routine int) int {
	if os.Getenv("CORTEXDB_EXHAUSTIVE") != "" {
		return full
	}
	return routine
}
