// Wire types and fetchers for the Analysis page. The types mirror the Go ones
// in pkg/liveview/analysis.go field for field. Every path is relative, so the
// page keeps working when the view is mounted under a prefix.

import { getJSON } from "./client";

export type BucketUnit = "day" | "week" | "month" | "year";

/** One bar or point: a bucket, its series when the chart has several, and the value. */
export interface AnalysisPoint {
  bucket: string;
  series?: string;
  value: number;
}

/** One time chart of counts. A bucket that is absent is zero. */
export interface AnalysisSeries {
  available: boolean;
  reason?: string;
  points: AnalysisPoint[];
  /** The count over the range. */
  total: number;
  /** The bucket this chart uses when it is not the overview's. */
  unit?: BucketUnit;
  /** The count over everything the brain holds, when the read learned it. */
  all?: number;
  /** The scan stopped at its cap inside the range; it is complete from covered_since on. */
  truncated?: boolean;
  covered_since?: string;
}

export interface AnalysisRecallPoint {
  bucket: string;
  recalls: number;
  empty: number;
  p50_ms: number;
  p95_ms: number;
}

/** The recall log over the range. A bucket with no recalls is absent. */
export interface AnalysisRecalls {
  available: boolean;
  reason?: string;
  /** The brain keeps no recall log, which is the default and not a fault. */
  off?: boolean;
  points: AnalysisRecallPoint[];
  total: number;
  empty: number;
  p50_ms?: number;
  p95_ms?: number;
  truncated?: boolean;
  covered_since?: string;
}

export interface AnalysisTypeCount {
  name: string;
  count: number;
}

export interface AnalysisGraph {
  available: boolean;
  reason?: string;
  /** "store" or "graph": what the counts were taken over. */
  counted?: string;
  nodes: number;
  edges: number;
  node_types: AnalysisTypeCount[];
  edge_types: AnalysisTypeCount[];
  other_nodes: number;
  other_node_types: number;
  other_edges: number;
  other_edge_types: number;
}

export interface AnalysisOverview {
  available: boolean;
  reason?: string;
  /** The builder can be used: the source runs tools. */
  explore: boolean;
  /** 0 is everything. */
  days: number;
  unit: BucketUnit;
  /** The first bucket of the range; absent when days is 0. */
  since?: string;
  until: string;
  timezone: string;
  memories: AnalysisSeries;
  documents: AnalysisSeries;
  recalls: AnalysisRecalls;
  writes: AnalysisSeries;
  graph: AnalysisGraph;
  at: number;
}

export type Measure = "count" | "sum" | "avg" | "median" | "p95" | "min" | "max";
export type RowKind = "documents" | "chunks";

/** One builder question, as /api/analysis/query reads it. */
export interface AnalysisQuery {
  of: RowKind;
  collection?: string;
  measure: Measure;
  field?: string;
  group?: string;
  bucket?: BucketUnit | "";
  bucket_field?: string;
  since?: string;
  until?: string;
  timezone?: string;
}

export interface AnalysisRow {
  /** Absent when the question has no bucket; null when the row's time could not be read. */
  bucket?: string | null;
  /** Null for the rows that do not carry the group field. */
  group?: string | null;
  /** Null when no row of the group held a number. */
  value: number | null;
  rows: number;
  skipped?: number;
}

export interface AnalysisResult {
  query: AnalysisQuery;
  rows: AnalysisRow[];
  truncated?: boolean;
}

export interface AnalysisField {
  name: string;
  kind: "number" | "date" | "text";
  documents: number;
  distinct: number;
  examples?: string[];
}

export interface AnalysisFields {
  sampled: number;
  fields: AnalysisField[];
  collections: string[];
}

type Opts = { signal?: AbortSignal };

/** The zone the reader is in, which is the zone a "day" is cut in. */
export function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

export const analysisApi = {
  overview: (days: number, o: Opts = {}) =>
    getJSON<AnalysisOverview>("api/analysis/overview", { days: String(days), tz: browserZone() }, o.signal),

  fields: (collection: string, o: Opts = {}) => getJSON<AnalysisFields>("api/analysis/fields", { collection }, o.signal),

  query: (q: AnalysisQuery, o: Opts = {}) =>
    getJSON<AnalysisResult>(
      "api/analysis/query",
      {
        of: q.of,
        collection: q.collection,
        measure: q.measure,
        field: q.measure === "count" ? undefined : q.field,
        group: q.group,
        bucket: q.bucket,
        bucket_field: q.bucket ? q.bucket_field : undefined,
        since: q.since,
        until: q.until,
        tz: browserZone(),
      },
      o.signal,
    ),
};
