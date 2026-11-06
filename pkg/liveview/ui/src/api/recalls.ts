// Wire types and fetchers for the recall log behind the Recalls page. The
// types mirror the Go ones field for field: RecallLogEntry, RecallLogHit and
// the list, stats and feedback shapes in pkg/cortexdb/recall_log.go, and the
// explain record in pkg/cortexdb/recall_explain.go. The routes are in
// pkg/liveview/recalls.go. Every path is relative, so the page keeps working
// when the view is mounted under a prefix.

import { ApiError, apiUrl, getJSON, postJSON, type Params } from "./client";

export type RecallVerdict = "useful" | "noise";

/** One hit as the log keeps it (RecallLogHit). */
export interface RecallHit {
  /** "memory" or "knowledge". */
  kind: string;
  id: string;
  /** The record's node in the graph, for lighting it up. */
  node_id?: string;
  /** Rank within its own side: memories and knowledge are ranked apart. */
  rank: number;
  title?: string;
  score: number;
  similarity?: number;
  coverage?: number;
  via?: string[];
  writer?: string;
  source?: string;
  project?: string;
  date?: string;
  /** How the record says it is known; absent when untagged. */
  grade?: string;
  grade_why?: string;
  /** The mark given on this hit in this recall, if any. */
  verdict?: RecallVerdict | (string & {});
}

/** What the recall searched with (RecallExplainPlan). */
export interface RecallExplainPlan {
  query: string;
  keywords?: string[];
  alternate_queries?: string[];
  entity_names?: string[];
  retrieval_mode?: string;
  planner?: string;
}

/** One row a retriever put forward. */
export interface RecallCandidate {
  id: string;
  score: number;
  rank: number;
}

/** One row a floor or filter removed. */
export interface RecallDrop {
  by: string;
  id: string;
  score?: number;
  threshold?: number;
}

/** One half of a recall: memories or knowledge (RecallExplainSide). */
export interface RecallExplainSide {
  requested_mode?: string;
  effective_mode?: string;
  reason?: string;
  /** Each retriever's top candidates with its own raw score, in its order. */
  candidates?: Record<string, RecallCandidate[]>;
  fused?: RecallCandidate[];
  dropped?: RecallDrop[];
  /** How many each floor removed, counting those past the ones kept in `dropped`. */
  dropped_total?: Record<string, number>;
}

export interface RecallExplainHit {
  kind: string;
  id: string;
  score: number;
  similarity?: number;
  coverage?: number;
  via?: string[];
  grade?: string;
}

/** The account of one recall (RecallExplain). */
export interface RecallExplain {
  plan: RecallExplainPlan;
  memory?: RecallExplainSide;
  knowledge?: RecallExplainSide;
  graph_facts?: number;
  degraded?: string[];
  final: RecallExplainHit[];
}

/** One logged recall (RecallLogEntry). */
export interface RecallEntry {
  id: string;
  request_id?: string;
  /** The W3C trace the recall was part of, and the span it ran in. */
  trace_id?: string;
  span_id?: string;
  /** RFC 3339. */
  at: string;
  key?: string;
  host?: string;
  project?: string;
  agent?: string;
  tool?: string;
  query: string;
  planner?: string;
  latency_ms: number;
  hit_count: number;
  degraded?: string[];
  hits: RecallHit[];
  /** Only recalls/get carries it. */
  explain?: RecallExplain;
}

export interface RecallListResponse {
  recalls: RecallEntry[];
  /** Pass as `before` for the next older page; absent at the end. */
  next_before?: number;
}

export interface RecordRecallStat {
  kind: string;
  id: string;
  node_id?: string;
  title?: string;
  count: number;
  last_at: string;
  queries?: string[];
}

export interface NeverRecalledRecord {
  id: string;
  node_id: string;
  title: string;
  created_at: string;
}

export interface RecallStats {
  /** The start of the retention window. */
  since: string;
  recalls: number;
  empty_recalls: number;
  degraded_recalls: number;
  most_recalled: RecordRecallStat[];
  record?: RecordRecallStat;
  /** Left out for a key with a scope, and when every memory has been recalled. */
  never_recalled?: NeverRecalledRecord[];
}

export interface RecallFeedbackRequest {
  recall_id: string;
  record_id: string;
  verdict: RecallVerdict | "clear";
  note?: string;
}

export interface RecallFeedbackResponse {
  recall_id: string;
  record_id: string;
  verdict: string;
}

/** The query string of a timeline page: what /api/recalls reads. */
export interface RecallListQuery {
  contains?: string;
  empty?: boolean;
  record_id?: string;
  trace?: string;
  key?: string;
  project?: string;
  before?: number;
  limit?: number;
}

/** Where the marks download as golden-set JSONL. */
export const GOLDEN_PATH = "api/recalls/golden";

type Opts = { signal?: AbortSignal };

function listParams(q: RecallListQuery): Params {
  return {
    contains: q.contains,
    empty: q.empty ? 1 : undefined,
    record_id: q.record_id,
    trace: q.trace,
    key: q.key,
    project: q.project,
    before: q.before || undefined,
    limit: q.limit,
  };
}

export const recallsApi = {
  list: (q: RecallListQuery, o: Opts = {}) => getJSON<RecallListResponse>("api/recalls", listParams(q), o.signal),

  get: (id: string, o: Opts = {}) => getJSON<RecallEntry>("api/recalls/get", { id }, o.signal),

  stats: (limit: number, o: Opts = {}) => getJSON<RecallStats>("api/recalls/stats", { limit }, o.signal),

  /** A JSON POST from this page: the server refuses a form post and another origin. */
  feedback: (req: RecallFeedbackRequest, o: Opts = {}) => postJSON<RecallFeedbackResponse>("api/recalls/feedback", req, o.signal),

  /**
   * The golden JSONL as text. Read here rather than followed as a link, so a
   * log that is off, or holds no marks, is said on the page instead of
   * arriving as a downloaded error.
   */
  golden: async (o: Opts = {}): Promise<string> => {
    let res: Response;
    try {
      res = await fetch(apiUrl(GOLDEN_PATH), { signal: o.signal, headers: { Accept: "application/x-ndjson, application/json" } });
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") throw err;
      throw new ApiError(err instanceof Error ? err.message : "network error", 0);
    }
    const text = await res.text();
    if (!res.ok) {
      let message = text.trim().slice(0, 200) || `${res.status} ${res.statusText}`;
      try {
        const body = JSON.parse(text) as { error?: string };
        if (body.error) message = body.error;
      } catch {
        // not JSON: the status line stands
      }
      throw new ApiError(message, res.status);
    }
    return text;
  },
};
