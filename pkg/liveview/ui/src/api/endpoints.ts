import { getJSON, postJSON } from "./client";
import type {
  AskAnswer,
  ChangesPage,
  ChangesQuery,
  ContractReport,
  CypherAnswer,
  EntityView,
  FindResponse,
  GraphHistory,
  HistoryQuery,
  Neighbourhood,
  Payload,
  RecordDetail,
} from "./types";

/** An instant the API takes as `as_of`: RFC 3339 from unix milliseconds. */
export function asOfParam(ms: number): string {
  return new Date(ms).toISOString();
}

type Opts = { signal?: AbortSignal };

export const api = {
  /** The one-shot payload. A pinned read answers 200 and says so in `pinned`. */
  graph: (asOf?: number, o: Opts = {}) =>
    getJSON<Payload>("api/graph", { as_of: asOf ? asOfParam(asOf) : undefined }, o.signal),

  record: (id: string, o: Opts = {}) => getJSON<RecordDetail>("api/record", { id }, o.signal),

  contract: (o: Opts = {}) => getJSON<ContractReport>("api/contract", undefined, o.signal),

  find: (q: string, asOf?: number, o: Opts = {}) =>
    getJSON<FindResponse>("api/find", { q, as_of: asOf ? asOfParam(asOf) : undefined }, o.signal),

  expand: (id: string, limit?: number, asOf?: number, o: Opts = {}) =>
    getJSON<Neighbourhood>("api/expand", { id, limit, as_of: asOf ? asOfParam(asOf) : undefined }, o.signal),

  ask: (q: string, o: Opts = {}) => postJSON<AskAnswer>("api/ask", { q }, o.signal),

  cypher: (query: string, o: Opts = {}) => postJSON<CypherAnswer>("api/cypher", { query }, o.signal),

  history: (q: HistoryQuery, o: Opts = {}) =>
    getJSON<GraphHistory>("api/history", { ...q }, o.signal),

  /**
   * One page of the change feed, newest first. An older server without
   * /api/changes answers 404; callers treat that as "no change feed".
   */
  changes: (q: ChangesQuery = {}, o: Opts = {}) =>
    getJSON<ChangesPage>("api/changes", { ...q }, o.signal),

  entity: (id: string, o: Opts = {}) => getJSON<EntityView>("api/entity", { id }, o.signal),
};
