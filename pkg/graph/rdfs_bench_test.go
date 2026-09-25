package graph

import "testing"

// BenchmarkRDFSRefreshSynthetic measures the inference engine alone, without
// the store, on graphs shaped like what the property-graph projection feeds
// it: a class tree eight deep, thousands of typed instances, and hundreds of
// properties with domain and range. The 20k case is the production brain's
// order of magnitude; the 2k case exists so a slower engine can still be
// measured more than once.
func BenchmarkRDFSRefreshSynthetic(b *testing.B) {
	cases := []struct {
		name                                  string
		depth, instances, properties, edgeCnt int
	}{
		{"2k", 5, 400, 30, 1500},
		{"20k", 8, 4000, 300, 14700},
	}
	for _, tc := range cases {
		explicit := syntheticInferenceGraph(tc.depth, tc.instances, tc.properties, tc.edgeCnt)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportMetric(float64(len(explicit)), "explicit")
			var inferred int
			for i := 0; i < b.N; i++ {
				records := computeRDFSInferenceRecords(explicit)
				inferred = len(records) - len(explicit)
			}
			b.ReportMetric(float64(inferred), "inferred")
		})
	}
}
