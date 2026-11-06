// Wire types for the liveview JSON API. They mirror the Go types in
// pkg/liveview (live.go, server.go, record.go, contract.go, explore.go,
// history.go) and the contract in issue #127 for the change feed and the
// entity view. Field names are the wire names, so nothing is renamed here.

// ---- graph --------------------------------------------------------------

export interface Node {
  id: string;
  label: string;
  type: string;
  /** The record's _grade; absent for a record nobody stamped. */
  grade?: string;
}

export interface Edge {
  source: string;
  target: string;
  label: string;
  /** The edge's own row id, so a record lookup can be asked about it. */
  id?: string;
  grade?: string;
}

export type ActivityKind = "query" | "write" | "relate";

/** One thing the brain just did (a tool call). */
export interface ActivityEvent {
  seq: number;
  /** Unix milliseconds. */
  at: number;
  kind: ActivityKind | (string & {});
  tool: string;
  text: string;
  /** Strings to light nodes up by, matched loosely against labels and ids. */
  terms?: string[];
  /** from/to pairs a relation write pulses along. */
  links?: [string, string][];
  failed?: boolean;
}

export interface Payload {
  version: number;
  nodes: Node[];
  edges: Edge[];
  events: ActivityEvent[];
  source: string;
  activity: boolean;
  interval_ms: number;
  grades?: boolean;
  records?: boolean;
  temporal?: boolean;
  explore?: boolean;
  /** Unix ms of the instant described; absent for the live graph. */
  as_of?: number;
  pinned?: boolean;
  pin_reason?: string;
}

/** What the SSE `snapshot` event carries. */
export interface SnapshotPayload extends Payload {
  /** Change-feed head when the subscription was taken; 0 with no feed. */
  cursor: number;
  /** Whether this source emits `change` events at all. */
  changes: boolean;
}

export interface Delta {
  version: number;
  added_nodes: Node[];
  removed_nodes: string[];
  added_edges: Edge[];
  removed_edges: Edge[];
  /** Totals after applying. */
  nodes: number;
  edges: number;
}

// ---- change feed (issue #127) -------------------------------------------

export type ChangeOp = "added" | "updated" | "superseded" | "retracted" | "merged" | "deleted";
export type ChangeKind = "node" | "edge" | "triple" | "memory" | "knowledge" | "ontology_schema";

export interface Ref {
  id: string;
  label: string;
  type?: string;
}

export interface ChangeItem {
  /** Strictly increasing; may have gaps. */
  seq: number;
  /** Unix milliseconds. */
  at: number;
  kind: ChangeKind | (string & {});
  op: ChangeOp;
  /** The changed row's id. */
  id: string;
  producer?: string;
  /** Raw history reason: superseded, retracted, merged, document_deleted, ... */
  reason?: string;
  /** One plain line, e.g. "Leo lives_in Chengdu". */
  summary: string;
  node?: Ref;
  edge?: { source: Ref; predicate: string; target: Ref };
  triple?: { subject: string; predicate: string; object: string };
  replaced_by?: { id: string; summary: string };
  replaces?: { id: string; summary: string };
  schema?: { id: string; version: number };
  grade?: string;
  /** Node ids this change touched; empty when none. */
  entities: string[];
}

export interface ChangesPage {
  /** Newest first. */
  items: ChangeItem[];
  head: number;
  /** Pass as `before` for the next older page; absent at the oldest. */
  next_before?: number;
  pruned_through?: number;
}

export interface ChangesQuery {
  before?: number;
  after?: number;
  limit?: number;
  kind?: string[];
  op?: string[];
  producer?: string[];
  entity?: string[];
}

export interface ResetEvent {
  reason: string;
  pruned_through: number;
  head: number;
}

export interface PingEvent {
  at: number;
}

// ---- entity (issue #127) ------------------------------------------------

export interface EntityRelation {
  edge_id: string;
  predicate: string;
  other: Ref;
  grade?: string;
  valid_from?: number;
  recorded_at?: number;
}

export interface RelationGroup {
  predicate: string;
  edges: EntityRelation[];
}

export interface EntityView {
  /** From the current snapshot; null when the id is not in the graph now. */
  node: Node | null;
  out: RelationGroup[];
  in: RelationGroup[];
}

// ---- record inspector ---------------------------------------------------

export interface RecordText {
  chunk_id: string;
  document_id?: string;
  content: string;
}

export interface RecordDecision {
  id: string;
  kind?: string;
  actor?: string;
  at?: string;
  verdict?: string;
  note?: string;
  grade?: string;
  supersedes?: string[];
}

export interface MemoryFact {
  key: string;
  value: string;
}

export interface MemoryDetail {
  id: string;
  content: string;
  scope?: string;
  namespace?: string;
  user_id?: string;
  created_at?: string;
  importance?: number;
  facts?: MemoryFact[];
}

export interface RecordDetail {
  available: boolean;
  reason?: string;
  found: boolean;
  id: string;
  edge?: boolean;
  type?: string;
  content?: string;
  from?: string;
  to?: string;
  grade?: string;
  state?: string;
  why?: string;
  source?: string;
  chunk?: string;
  producer?: string;
  at?: string;
  by?: string;
  confidence?: string;
  valid_from?: string;
  memory?: MemoryDetail;
  contradicts?: string[];
  document_id?: string;
  chunk_ids?: string[];
  text?: RecordText[];
  missing_chunks?: string[];
  inferred?: boolean;
  rule?: string;
  decisions?: RecordDecision[];
  chain?: RecordDecision[];
  chain_truncated?: boolean;
  notes?: string[];
}

// ---- knowledge contract -------------------------------------------------

export interface ContractRow {
  /** Contract value verbatim; empty for the untagged row. */
  grade: string;
  kind: string;
  nodes: number;
  edges: number;
}

export interface GradedRecord {
  id: string;
  edge: boolean;
  type: string;
  content?: string;
  from?: string;
  to?: string;
  grade: string;
  state?: string;
  why?: string;
  source?: string;
  producer?: string;
  at?: string;
  by?: string;
}

export interface ContractReport {
  available: boolean;
  reason?: string;
  rows: ContractRow[];
  graded: number;
  untagged: number;
  nodes: number;
  edges: number;
  attention: GradedRecord[];
  truncated?: boolean;
  total: number;
  /** When this was read, unix ms. */
  at: number;
}

// ---- explore: find / expand / ask / cypher ------------------------------

export interface FindResult extends Node {
  match?: string;
}

export interface FindResponse {
  results: FindResult[];
}

export interface Neighbourhood {
  nodes: Node[];
  edges: Edge[];
}

export interface AskText {
  id: string;
  title?: string;
  text: string;
  score?: number;
}

export interface GraphFact {
  subject: string;
  predicate: string;
  object: string;
  subject_id?: string;
  object_id?: string;
}

export interface AskAnswer {
  memories: AskText[];
  passages: AskText[];
  facts: GraphFact[];
  /** Every node id the answer names. */
  touched: string[];
  mode?: string;
}

export interface CypherAnswer {
  columns: string[];
  rows: string[][];
  row_count: number;
  truncated?: boolean;
  nodes: Node[];
  edges: Edge[];
}

// ---- history (graph_history) --------------------------------------------

export interface ReplacedVersion {
  id: string;
  object: string;
  object_name?: string;
  ended_at?: string;
  reason?: string;
}

export interface InEffect {
  kind: string;
  summary: string;
  rule?: string;
  replaced?: ReplacedVersion;
  schema_id?: string;
  schema_version?: number;
  producer?: string;
  revisions?: number;
  merged_from?: string;
  alongside?: string[];
  schema_opinion?: string;
  also_asserted_by?: string[];
}

export interface VersionProvenance {
  document_id?: string;
  chunk_ids?: string[];
  inferred?: boolean;
  rule?: string;
  source?: string;
}

export interface HistoryVersion {
  id: string;
  subject: string;
  object: string;
  object_name?: string;
  /** RFC 3339; Go omits zero times, so absent means unknown. */
  valid_from?: string;
  valid_to?: string;
  recorded_at?: string;
  retracted_at?: string;
  status: string;
  reason?: string;
  superseded_by?: string;
  producer?: string;
  schema_id?: string;
  schema_version?: number;
  provenance?: VersionProvenance;
  in_effect?: InEffect;
}

export interface HistoryLine {
  subject: string;
  subject_name?: string;
  predicate: string;
  /** "out" when the entity is the subject, "in" when it is the object. */
  direction: "out" | "in" | (string & {});
  /** Oldest first. */
  versions: HistoryVersion[];
  versions_truncated?: boolean;
}

export interface HistoryNodeVersion {
  content?: string;
  node_type?: string;
  valid_from?: string;
  valid_to?: string;
  recorded_at?: string;
  retracted_at?: string;
  status: string;
  reason?: string;
  superseded_by?: string;
  producer?: string;
  in_effect?: InEffect;
}

export interface HistoryMerge {
  id: string;
  name?: string;
  at?: string;
}

export interface GraphHistory {
  entity: string;
  name?: string;
  as_of?: string;
  node_versions?: HistoryNodeVersion[];
  node_versions_truncated?: boolean;
  merged_from?: HistoryMerge[];
  lines: HistoryLine[];
  next_cursor?: string;
  truncated?: boolean;
}

export interface HistoryQuery {
  entity: string;
  relation?: string;
  as_of?: string;
  from?: string;
  to?: string;
  limit?: number;
  max_versions?: number;
  cursor?: string;
}
