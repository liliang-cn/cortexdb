// The ontology's version history, as the Versions page reads it.
//
// The shapes mirror the Go types the three routes encode: cortexdb's
// OntologyHistoryResponse, OntologyDiffResponse and the liveview's
// OntologyReport, of which this page reads only the list of saved schemas.
// Instants arrive as RFC 3339 text, as Go's time.Time encodes them.

import { getJSON } from "./client";

/** One interval during which a version was the active validator (cortexdb.OntologyActivation). */
export interface OntologyActivation {
  schema_id: string;
  version: number;
  active_from: string;
  /** Absent while the version still is the active validator. */
  active_to?: string;
  activated_by?: string;
  ended_by?: string;
  /** replaced, switched, deactivated or deleted. */
  end_reason?: string;
}

/**
 * What a version changed against the one before it, counted when it was saved
 * (cortexdb.OntologyChangeSummary). The diff route returns the changes themselves.
 */
export interface OntologyChangeSummary {
  /** The first version of a schema: there is nothing to compare it with. */
  initial?: boolean;
  /** Also true for a save that touched only actions, interfaces or object sets, which the diff does not count. */
  definition_changed: boolean;
  changes: number;
  breaking: number;
  has_breaking_changes: boolean;
  by_kind?: Record<string, number>;
}

/** One stored version as ontology_history lists it (cortexdb.OntologyVersionInfo). */
export interface OntologyVersionInfo {
  version: number;
  name: string;
  description?: string;
  strict_actions?: boolean;
  saved_at: string;
  saved_by?: string;
  /** Absent for a version saved before the history kept summaries. */
  summary?: OntologyChangeSummary;
  /** True while this version is the active validator. */
  active: boolean;
  /** The intervals it was active, oldest first. */
  activations: OntologyActivation[];
}

/** One page of a schema's history (cortexdb.OntologyHistoryResponse). */
export interface OntologyHistoryResponse {
  schema_id: string;
  /** False for a schema that was deleted while its history remains. */
  exists: boolean;
  current_version?: number;
  total: number;
  has_more: boolean;
  versions: OntologyVersionInfo[];
}

export interface OntologyHistoryQuery {
  schema: string;
  limit?: number;
  offset?: number;
  oldest_first?: boolean;
}

/** One difference between two versions (cortexdb.OntologyChange). */
export interface OntologyChange {
  /** object_type_added, property_removed, cardinality_tightened, ... */
  kind: string;
  /** "Person", "Person.email" or "lives_in.a". */
  target: string;
  detail: string;
  /** Data already written under the older version may no longer validate. */
  breaking: boolean;
}

export interface OntologyDiff {
  changes: OntologyChange[];
  has_breaking_changes: boolean;
}

export interface OntologyDiffResponse {
  diff: OntologyDiff;
}

/** A saved schema as the ontology report names it (liveview.OntologySchemaRef). */
export interface OntologySchemaRef {
  schema_id: string;
  name?: string;
  version: number;
  active: boolean;
}

/**
 * The part of the ontology report this page reads: whether the source could
 * be asked at all, and which schemas are saved. The report carries much more,
 * which the Ontology page draws.
 */
export interface OntologySchemaIndex {
  available: boolean;
  reason?: string;
  saved: boolean;
  schemas: OntologySchemaRef[];
}

type Opts = { signal?: AbortSignal };

export const ontologyHistoryApi = {
  /** The saved schemas. gap=0 asks for the declarations alone, so the store's vocabulary is not read for nothing. */
  schemas: (o: Opts = {}) => getJSON<OntologySchemaIndex>("api/ontology", { gap: 0 }, o.signal),

  /** One page of a schema's versions, newest first unless oldest_first is set. */
  history: (q: OntologyHistoryQuery, o: Opts = {}) =>
    getJSON<OntologyHistoryResponse>(
      "api/ontology/history",
      { schema: q.schema, limit: q.limit, offset: q.offset, oldest_first: q.oldest_first ? 1 : undefined },
      o.signal,
    ),

  /** What changed from one stored version to another. */
  diff: (schema: string, from: number, to: number, o: Opts = {}) =>
    getJSON<OntologyDiffResponse>("api/ontology/diff", { schema, from, to }, o.signal),
};
