// Package importflow imports external structured data (CSV, MySQL/PG SQL dumps)
// into CortexDB, building RAG (vector/FTS5) and knowledge-graph (RDF triple)
// foundations in a single pass, and optionally keeping each table as a typed
// table that cortexdb.DB.QueryImportedTables can aggregate and join in SQL
// (TablePlan.Table). AI assistance (mapping inference, triple
// extraction, text refinement) is injected via interfaces and always optional;
// this package imports no LLM SDK.
package importflow
