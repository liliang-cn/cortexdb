// Package cypher is a read-only openCypher / GQL MATCH engine over CortexDB's
// property graph (graph_nodes, graph_edges), on SQLite and PostgreSQL.
//
// It exists because models write Cypher fluently and SPARQL badly. The graph
// already answered structural questions through expand_graph, search_paths and
// SPARQL over its RDF projection; this is the same rows asked in the language
// a model reaches for first.
//
// # How it runs
//
// Each MATCH clause compiles to one SQL statement (recursive CTEs for
// variable-length relationships) that finds candidate bindings; everything
// that depends on openCypher's value semantics — comparisons, three-valued
// logic, aggregation, ordering — is evaluated in Go, once, identically on
// both databases. Property comparisons are pushed into SQL only as a superset
// pre-filter and re-checked exactly. See sqlgen.go for why. Every caller value
// is a bound parameter; SQL text is built only from fixed fragments and
// generated aliases.
//
// # Mapping onto the property graph
//
//	(n:T)            node_type = T (one type per node; labels(n) = [T], [] when unset)
//	-[r:R]->         edge_type = R; direction is from_node_id → to_node_id
//	n.key            the key in the node's properties JSON; numbers keep int/float
//	n.name           properties.name, else properties.title (as rdfs:label in SPARQL)
//	n.content        properties.content, else the content column (else null)
//	id(n), id(r)     the node or edge id (elementId is the same)
//	r.key            the key in the edge's properties JSON; r.weight falls back to the weight column
//	type(r)          edge_type
//
// A stored property always wins over a fallback. There is no n.id fallback:
// openCypher reads a missing property as null, and id(n) is the way to the id.
//
// # Supported subset
//
// Clauses: MATCH, OPTIONAL MATCH (comma-separated patterns, WHERE), WITH
// [DISTINCT] (aliases, aggregation, WHERE, ORDER BY, SKIP, LIMIT), UNWIND,
// RETURN [DISTINCT] (AS aliases, *, ORDER BY [ASC|DESC], SKIP/OFFSET, LIMIT),
// UNION, UNION ALL.
//
// Patterns: node patterns with a variable, labels (:A, :A:B conjunction,
// :A|B alternation, GQL IS A) and inline property maps; relationship patterns
// with direction (->, <-, -), types with alternation [:A|B], inline property
// maps, and variable length *, *n, *m..n, *..n, *m.. with an upper bound of at
// most MaxVarLength (6). An unbounded * is answered only after proving no
// trail from the seeds is longer than the cap; otherwise it is refused.
// Named paths p = (...). Within one MATCH no relationship is matched twice
// (openCypher relationship isomorphism), including along variable-length
// trails; nodes may repeat.
//
// Expressions: literals (integer, float, string, boolean, null, list, map),
// $parameters, property access, list indexing and slicing, + - * / % ^,
// = <> != < > <= >=, AND OR XOR NOT, IS [NOT] NULL, IN, STARTS WITH,
// ENDS WITH, CONTAINS, =~ (RE2 syntax, whole-string match), n:Label and GQL
// n IS [NOT] LABELED Label, CASE (simple and searched), list comprehensions,
// any/all/none/single. Aggregates: count(*), count, collect, min, max, sum,
// avg, each with DISTINCT. Functions: id, elementId, labels, type, keys,
// properties, size, length, nodes, relationships, startNode, endNode,
// coalesce, head, last, tail, reverse, range, toLower, toUpper, trim, ltrim,
// rtrim, replace, substring, left, right, split, toString, toInteger,
// toFloat, toBoolean, abs, ceil, floor, round, sign, sqrt, exists(prop),
// isEmpty.
//
// Refused with an error naming the construct: every write clause (CREATE,
// MERGE, SET, DELETE, DETACH DELETE, REMOVE, FOREACH, LOAD CSV) — the
// language is read-only by design; CALL; shortestPath/allShortestPaths;
// pattern expressions and pattern predicates; EXISTS/COUNT/COLLECT
// subqueries; map projections; reduce; namespaced and temporal functions;
// quantified path patterns and GQL match modes; reusing a variable-length
// relationship variable across clauses; WITH ... SKIP/LIMIT ... WHERE that
// reads a variable the WITH does not project.
//
// # Time
//
// The read sees the live tables, or with an as-of instant the bitemporal
// union with history (GraphStore.NodeSource / EdgeSource), exactly as every
// other graph read does. In addition, temporal facts — edges carrying
// valid_from / valid_to in their properties, as graphflow writes them — are
// matched only when valid at the instant (now by default); the caller can
// turn that off.
//
// # Order and budgets
//
// Without ORDER BY, a MATCH's rows come out sorted by the ids of the matched
// named elements in the order the pattern names them, and every later stage
// is stable, so the same query on the same graph returns the same order on
// both databases. Results are capped (MaxRows, flagged Truncated), every
// clause is capped in intermediate rows (an error, never a silent cut), and
// the whole query runs under a timeout.
package cypher
