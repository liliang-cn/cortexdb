# Evaluation And Release Gates

This project now has a small deterministic evaluation layer aimed at release confidence for the LLM-first RAG, knowledge graph, and GraphRAG surface.

## What The Evaluation Covers

- Retrieval regression checks across three modes:
  - lexical no-embedder GraphRAG
  - embedder-backed lexical/vector GraphRAG
  - graph-expanded GraphRAG
- Knowledge-graph correctness checks for:
  - ontology-lite validation
  - deterministic inferred-edge materialization with provenance
- Package-level benchmarks for:
  - `SearchGraphRAG`
  - `BuildContext`

The evaluation corpus is intentionally small and deterministic. It is designed to catch behavioral regressions in retrieval mode selection and graph expansion, not to serve as a large benchmark suite.

## Recommended Commands

Run these before a release:

```bash
go test ./pkg/cortexdb
go test -race ./pkg/cortexdb
go test -run 'TestEvaluationRetrievalModes|TestEvaluationSchemaValidationAndInference' ./pkg/cortexdb
go test -run '^$' -bench 'BenchmarkEvaluationGraphRAGSearchModes|BenchmarkEvaluationBuildContext' ./pkg/cortexdb
go build ./...
```

If `go test ./...` fails, separate tracked-repo failures from local untracked workspace files before treating it as a release blocker.

## Quality Gates

Ship only when all of these are true:

1. `pkg/cortexdb` unit tests pass.
2. `pkg/cortexdb` race tests pass.
3. Retrieval evaluation proves:
   - lexical mode keeps the employment seed chunk
   - embedder-backed lexical mode keeps the same seed chunk
   - graph mode recovers the related employer-profile chunk
4. Schema validation rejects illegal relation type pairs.
5. Inference materializes the expected inferred edge and stores provenance.
6. `go build ./...` succeeds.

## Benchmark Review Guidance

Benchmarks are for regression review, not hardcoded pass/fail thresholds in CI.

Review these trends between releases:

- `BenchmarkEvaluationGraphRAGSearchModes/lexical`
  - should remain the lowest-cost retrieval path
- `BenchmarkEvaluationGraphRAGSearchModes/graph_light`
  - should stay materially cheaper than full graph mode on the same fixture
- `BenchmarkEvaluationGraphRAGSearchModes/graph`
  - can cost more, but should not show unexpected allocation spikes after unrelated changes
- `BenchmarkEvaluationBuildContext`
  - should stay stable because packing is part of every high-level retrieval path

If a benchmark regresses noticeably, compare it to the previous release output and inspect the hot path before shipping.
