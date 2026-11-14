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
//	n.created_at     properties.created_at, else the created_at column
//	n.updated_at     properties.updated_at, else the updated_at column
//	r.created_at     properties.created_at, else the created_at column (edges have no updated_at)
//
// A stored property always wins over a fallback. There is no n.id fallback:
// openCypher reads a missing property as null, and id(n) is the way to the id.
// The timestamp columns read as RFC 3339 UTC strings to the second
// ("2026-03-05T10:00:00Z"), so they compare as text in time order and
// date(n.created_at) buckets them; like content they are not in keys(n),
// properties(n) or a returned node.
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
// avg, stDev, stDevP, percentileCont(x, p), percentileDisc(x, p), each with
// DISTINCT. Functions: id, elementId, labels, type, keys,
// properties, size, length, nodes, relationships, startNode, endNode,
// coalesce, head, last, tail, reverse, range, toLower, toUpper, trim, ltrim,
// rtrim, replace, substring, left, right, split, toString, toInteger,
// toFloat, toBoolean, abs, ceil, floor, round(x[, precision]), sign, sqrt,
// exp, log, log10, exists(prop), isEmpty, and the temporal functions below.
//
// Refused with an error naming the construct: every write clause (CREATE,
// MERGE, SET, DELETE, DETACH DELETE, REMOVE, FOREACH, LOAD CSV) — the
// language is read-only by design; CALL; shortestPath/allShortestPaths;
// pattern expressions and pattern predicates; EXISTS/COUNT/COLLECT
// subqueries; map projections; reduce; namespaced functions other than the
// temporal ones; localdatetime, time and localtime; named time zones;
// quantified path patterns and GQL match modes; reusing a variable-length
// relationship variable across clauses; WITH ... SKIP/LIMIT ... WHERE that
// reads a variable the WITH does not project.
//
// # Statistics
//
// Every aggregate skips nulls. stDev is the sample standard deviation (n-1)
// and stDevP the population one (n); both are 0.0 for a group too small to
// have one, an empty group included. percentileCont interpolates linearly
// between the two nearest values and is always a float; percentileDisc is
// the nearest rank — the smallest value with at least that share of the
// group at or below it — returned as it is stored. Both are null for an
// empty group, and a percentile outside 0.0 to 1.0 is an error. sum, avg,
// stDev and the percentiles take numbers only: a string that holds a number
// goes through toFloat.
//
// # Dates
//
// date(x) and datetime(x) read text — YYYY-MM-DD, YYYYMMDD, YYYY-MM, YYYY,
// the ISO week forms YYYY-Www and YYYY-Www-D, the ordinal YYYY-DDD, and any
// of them followed by T (or a space, as SQL writes it) and a time with an
// optional offset: Z, +08:00, +0800, +08. A time with no offset is UTC.
// date() of a timestamp is the date as written, in the timestamp's own
// offset. They also take a map ({year, month, day, hour, minute, second,
// millisecond, microsecond, nanosecond, timezone}, or {epochSeconds} /
// {epochMillis} for a datetime), convert each other, and with no argument
// are the query's one instant in UTC. datetime.fromEpoch(seconds, nanos) and
// datetime.fromEpochMillis(ms) are there too. null gives null. Text that is
// not a date is an error naming the text, as in openCypher, not null: a null
// would drop the row from its bucket without a word. Guard mixed data with
// CASE WHEN x =~ '\\d{4}-\\d{2}-\\d{2}.*' THEN date(x) END.
//
// Components: year, quarter, month, week (ISO), weekYear, day, ordinalDay,
// dayOfWeek (Monday is 1), dayOfQuarter; a datetime adds hour, minute,
// second, millisecond, microsecond, nanosecond, timezone, offset,
// offsetMinutes, offsetSeconds, epochSeconds, epochMillis. An unknown
// component is an error.
//
// date.truncate(unit, d) and datetime.truncate(unit, t) give the start of
// the millennium, century, decade, year, weekYear, quarter, month, week
// (Monday) or day, and for a datetime also the hour, minute, second,
// millisecond or microsecond, in the value's own offset. Each takes a date
// or a datetime, never a string.
//
// duration(x) reads ISO 8601 (P1Y2M3DT4H5M6.5S, P2W) or a map of years,
// quarters, months, weeks, days, hours, minutes, seconds, milliseconds,
// microseconds, nanoseconds. duration.between(a, b) is months, days and the
// rest; duration.inMonths, inDays and inSeconds are the whole difference in
// that one unit. A duration adds to and subtracts from a date (which ignores
// its sub-day part) or a datetime; adding months keeps the day of the month
// or, where the month is shorter, its last day. Components: years, quarters,
// months, weeks, days, hours, minutes, seconds, milliseconds, microseconds,
// nanoseconds, and monthsOfYear, daysOfWeek, minutesOfHour, secondsOfMinute,
// millisecondsOfSecond, microsecondsOfSecond, nanosecondsOfSecond.
//
// Dates order by day and datetimes by instant, whatever offset each is
// written in. A date and a datetime are never equal and `<` between them is
// null; the same holds against a string. Two datetimes are equal, and one
// group, only when they are the same instant at the same offset. Durations
// are equal part by part (P1D is not PT24H) and `<` between them is null.
// ORDER BY puts datetimes, dates, then durations between paths and strings.
// In a result every temporal value is its ISO 8601 string: "2026-03-01",
// "2026-03-05T10:00:00+08:00", "P1M2D".
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
