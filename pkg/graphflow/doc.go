// Package graphflow provides a library-first graph extraction/build/report/export
// pipeline over CortexDB's graph and RDF storage.
//
// The package is intentionally layered:
//   - Detector finds input documents.
//   - Extractor emits a unified extraction schema.
//   - Build persists that schema into CortexDB's graph store.
//   - Analyze derives deterministic graph summaries.
//   - RenderReport produces markdown output.
//   - Export writes graph.json plus GRAPH_REPORT.md.
//
// The default closed loop is deterministic and does not require an LLM.
// Model-dependent extractors can be plugged in later via the Extractor interface.
package graphflow
