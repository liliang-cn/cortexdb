// Package importflow imports external structured data (CSV, MySQL/PG SQL dumps)
// into CortexDB, building RAG (vector/FTS5) and knowledge-graph (RDF triple)
// foundations in a single pass. AI assistance (mapping inference, triple
// extraction, text refinement) is injected via interfaces and always optional;
// this package imports no LLM SDK.
package importflow
