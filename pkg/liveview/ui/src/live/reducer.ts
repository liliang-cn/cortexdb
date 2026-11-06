// The live state and the pure functions that move it.
//
// Nothing here knows about React, zustand or EventSource: each function takes
// the current LiveData and one thing the stream said, and returns the next
// LiveData, or the very same object when the event changed nothing. That keeps
// the rules (identity, ordering, dedupe, limits) unit-testable with fixtures,
// and lets the store be a thin wrapper.

import type {
  ActivityEvent,
  ChangeItem,
  ChangeOp,
  Delta,
  Edge,
  Node,
  ResetEvent,
  SnapshotPayload,
} from "@/api";

export const ACTIVITY_LIMIT = 500;
export const CHANGES_LIMIT = 1000;
/** How long a node or edge stays "recently touched" after a change. */
export const TOUCH_TTL_MS = 6000;
/** A snapshot that differs from the last one by more than this lights nothing. */
const SNAPSHOT_TOUCH_LIMIT = 200;

/** The transport's state, as the page sees it. */
export type Link = "connecting" | "open" | "reconnecting" | "offline";

/** What the source can do, from the last snapshot. */
export interface Capabilities {
  grades: boolean;
  records: boolean;
  temporal: boolean;
  explore: boolean;
  /** Tool-call activity is observed (false for a process with no MCP server). */
  activity: boolean;
  /** The source emits `change` events. */
  changes: boolean;
}

export interface Touch {
  op: ChangeOp;
  /** Unix ms when it was touched. */
  at: number;
}

export interface LiveData {
  link: Link;
  /** A snapshot has arrived on this page load. */
  ready: boolean;
  source: string;
  caps: Capabilities;
  intervalMs: number;

  /** The graph version the snapshot and deltas are at. */
  version: number;
  nodes: ReadonlyMap<string, Node>;
  /** Keyed by edgeKey(): the two ends and the relation. */
  edges: ReadonlyMap<string, Edge>;

  /** The instant the person asked to pin to (unix ms), or null for live. */
  asOf: number | null;
  /** The server answered the pin (the snapshot describes `pinnedAt`). */
  pinned: boolean;
  pinnedAt: number | null;
  /** Why the server refused a pin, when it did. */
  pinReason: string | null;

  /** Last change seq seen; the point a reconnect resumes from. 0 when none. */
  cursor: number;
  /** Tool calls, newest first, at most ACTIVITY_LIMIT. */
  activity: ActivityEvent[];
  /** Change feed rows, newest first by seq, deduped, at most CHANGES_LIMIT. */
  changes: ReadonlyArray<ChangeItem>;
  /** Node or edge id to what just happened to it; see sweepTouched. */
  touched: Readonly<Record<string, Touch>>;
  /** How many times the server said the cursor fell behind retention. */
  resets: number;
}

const noCaps: Capabilities = {
  grades: false,
  records: false,
  temporal: false,
  explore: false,
  activity: false,
  changes: false,
};

export function initialData(asOf: number | null = null): LiveData {
  return {
    link: "connecting",
    ready: false,
    source: "",
    caps: noCaps,
    intervalMs: 0,
    version: 0,
    nodes: new Map(),
    edges: new Map(),
    asOf,
    pinned: false,
    pinnedAt: null,
    pinReason: null,
    cursor: 0,
    activity: [],
    changes: [],
    touched: {},
    resets: 0,
  };
}

// ---- identity -----------------------------------------------------------

/** An edge's identity is its two ends and its relation, as on the Go side. */
export function edgeKey(e: Pick<Edge, "source" | "label" | "target">): string {
  return `${e.source}\u0000${e.label}\u0000${e.target}`;
}

/** The id changes and touches refer to: the row id when there is one. */
export function edgeRef(e: Edge): string {
  return e.id || edgeKey(e);
}

function sameNode(a: Node, b: Node): boolean {
  return a.id === b.id && a.label === b.label && a.type === b.type && a.grade === b.grade;
}

function sameEdge(a: Edge, b: Edge): boolean {
  return a.source === b.source && a.target === b.target && a.label === b.label && a.id === b.id && a.grade === b.grade;
}

// ---- touched ------------------------------------------------------------

/** A change that says why outranks the plain add/update/remove a delta implies. */
function rank(op: ChangeOp): number {
  return op === "superseded" || op === "retracted" || op === "merged" ? 2 : 1;
}

function touch(map: Record<string, Touch>, id: string, op: ChangeOp, now: number): void {
  if (!id) return;
  const prev = map[id];
  if (prev && now - prev.at < TOUCH_TTL_MS && rank(prev.op) > rank(op)) return;
  map[id] = { op, at: now };
}

/** Drop entries older than the TTL. Returns the same state when none expired. */
export function sweepTouched(s: LiveData, now: number): LiveData {
  let expired = false;
  for (const t of Object.values(s.touched)) {
    if (now - t.at >= TOUCH_TTL_MS) {
      expired = true;
      break;
    }
  }
  if (!expired) return s;
  const next: Record<string, Touch> = {};
  for (const [id, t] of Object.entries(s.touched)) {
    if (now - t.at < TOUCH_TTL_MS) next[id] = t;
  }
  return { ...s, touched: next };
}

// ---- rings --------------------------------------------------------------

/**
 * Merge change items into a list kept newest first by seq, with no seq twice.
 * Used for the live tail here and by pages that splice a fetched page in.
 */
export function mergeChangeItems(
  existing: ReadonlyArray<ChangeItem>,
  incoming: ReadonlyArray<ChangeItem>,
  limit: number = CHANGES_LIMIT,
): ReadonlyArray<ChangeItem> {
  if (incoming.length === 0) return existing;
  const seen = new Set(existing.map((c) => c.seq));
  const fresh: ChangeItem[] = [];
  for (const c of incoming) {
    if (seen.has(c.seq)) continue;
    seen.add(c.seq);
    fresh.push(c);
  }
  if (fresh.length === 0) return existing;
  const all = existing.concat(fresh).sort((a, b) => b.seq - a.seq);
  return all.length > limit ? all.slice(0, limit) : all;
}

function activityKey(e: ActivityEvent): string {
  // seq restarts with the process, so the time keeps a restarted server's
  // first events from being mistaken for the old ones.
  return `${e.seq}:${e.at}`;
}

function mergeActivity(existing: ActivityEvent[], incoming: ActivityEvent[]): ActivityEvent[] {
  if (incoming.length === 0) return existing;
  const seen = new Set(existing.map(activityKey));
  const fresh = incoming.filter((e) => {
    const k = activityKey(e);
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });
  if (fresh.length === 0) return existing;
  const all = existing.concat(fresh).sort((a, b) => b.at - a.at || b.seq - a.seq);
  return all.length > ACTIVITY_LIMIT ? all.slice(0, ACTIVITY_LIMIT) : all;
}

// ---- stream events ------------------------------------------------------

/** Adopt a node list, keeping the object a row already had when it is unchanged. */
function reconcileNodes(prev: ReadonlyMap<string, Node>, list: Node[]): Map<string, Node> {
  const next = new Map<string, Node>();
  for (const n of list) {
    const old = prev.get(n.id);
    next.set(n.id, old && sameNode(old, n) ? old : n);
  }
  return next;
}

function reconcileEdges(prev: ReadonlyMap<string, Edge>, list: Edge[]): Map<string, Edge> {
  const next = new Map<string, Edge>();
  for (const e of list) {
    const k = edgeKey(e);
    const old = prev.get(k);
    next.set(k, old && sameEdge(old, e) ? old : e);
  }
  return next;
}

/**
 * A snapshot replaces the graph (keeping identity of what did not change) and
 * merges its activity backlog. On a live reconnect it also lights what moved
 * while the page was away, unless the jump is too big to be worth lighting.
 */
export function applySnapshot(s: LiveData, snap: SnapshotPayload, now: number): LiveData {
  const nodes = reconcileNodes(s.nodes, snap.nodes ?? []);
  const edges = reconcileEdges(s.edges, snap.edges ?? []);
  const pinned = Boolean(snap.pinned);

  const touched: Record<string, Touch> = { ...s.touched };
  if (s.ready && !s.pinned && !pinned) {
    const moves: [string, ChangeOp][] = [];
    for (const [id, n] of nodes) {
      const old = s.nodes.get(id);
      if (!old) moves.push([id, "added"]);
      else if (old !== n) moves.push([id, "updated"]);
    }
    for (const id of s.nodes.keys()) if (!nodes.has(id)) moves.push([id, "deleted"]);
    for (const [k, e] of edges) {
      const old = s.edges.get(k);
      if (!old) moves.push([edgeRef(e), "added"]);
      else if (old !== e) moves.push([edgeRef(e), "updated"]);
    }
    for (const [k, e] of s.edges) if (!edges.has(k)) moves.push([edgeRef(e), "deleted"]);
    if (moves.length <= SNAPSHOT_TOUCH_LIMIT) for (const [id, op] of moves) touch(touched, id, op, now);
  }

  // The change cursor is the last seq seen, never the snapshot's head: a
  // resume replays what lies between the two, and moving the cursor early
  // would lose it if the connection dropped mid-replay.
  let cursor = s.cursor;
  let changes = s.changes;
  if (!pinned) {
    if (cursor === 0) cursor = snap.cursor ?? 0;
    else if ((snap.cursor ?? 0) < cursor) {
      // The feed went backwards (a restarted or replaced source): what is held
      // belongs to another history.
      cursor = snap.cursor ?? 0;
      changes = [];
    }
  }

  return {
    ...s,
    ready: true,
    source: snap.source ?? "",
    caps: {
      grades: Boolean(snap.grades),
      records: Boolean(snap.records),
      temporal: Boolean(snap.temporal),
      explore: Boolean(snap.explore),
      activity: Boolean(snap.activity),
      changes: Boolean(snap.changes),
    },
    intervalMs: snap.interval_ms ?? 0,
    version: snap.version,
    nodes,
    edges,
    pinned,
    pinnedAt: pinned ? (snap.as_of ?? null) : null,
    pinReason: snap.pin_reason ? snap.pin_reason : null,
    cursor,
    changes,
    activity: mergeActivity(s.activity, snap.events ?? []),
    touched,
  };
}

/**
 * A delta carries upserts and removals. Rows that arrive unchanged keep their
 * object, and a delta that changes nothing returns the same state, so a
 * subscriber to nodes or edges re-renders only when its data really moved.
 */
export function applyDelta(s: LiveData, d: Delta, now: number): LiveData {
  if (!s.ready || s.pinned || d.version <= s.version) return s;

  let nodes: Map<string, Node> | null = null;
  let edges: Map<string, Edge> | null = null;
  const touched: Record<string, Touch> = { ...s.touched };
  let touchedAny = false;
  const mark = (id: string, op: ChangeOp) => {
    touch(touched, id, op, now);
    touchedAny = true;
  };

  for (const n of d.added_nodes ?? []) {
    const old = s.nodes.get(n.id);
    if (old && sameNode(old, n)) continue;
    nodes ??= new Map(s.nodes);
    nodes.set(n.id, n);
    mark(n.id, old ? "updated" : "added");
  }
  for (const id of d.removed_nodes ?? []) {
    if (!(nodes ?? s.nodes).has(id)) continue;
    nodes ??= new Map(s.nodes);
    nodes.delete(id);
    mark(id, "deleted");
  }
  for (const e of d.removed_edges ?? []) {
    const k = edgeKey(e);
    const old = (edges ?? s.edges).get(k);
    if (!old) continue;
    edges ??= new Map(s.edges);
    edges.delete(k);
    mark(edgeRef(old), "deleted");
  }
  for (const e of d.added_edges ?? []) {
    const k = edgeKey(e);
    const old = (edges ?? s.edges).get(k);
    if (old && sameEdge(old, e)) continue;
    edges ??= new Map(s.edges);
    edges.set(k, e);
    mark(edgeRef(e), old ? "updated" : "added");
  }

  if (!nodes && !edges) return { ...s, version: d.version };
  return {
    ...s,
    version: d.version,
    nodes: nodes ?? s.nodes,
    edges: edges ?? s.edges,
    touched: touchedAny ? touched : s.touched,
  };
}

export function applyActivity(s: LiveData, e: ActivityEvent): LiveData {
  const activity = mergeActivity(s.activity, [e]);
  return activity === s.activity ? s : { ...s, activity };
}

/** A change row joins the ring, advances the cursor, and lights what it touched. */
export function applyChange(s: LiveData, item: ChangeItem, now: number): LiveData {
  const changes = mergeChangeItems(s.changes, [item]);
  const cursor = Math.max(s.cursor, item.seq);
  if (changes === s.changes) return cursor === s.cursor ? s : { ...s, cursor };

  const touched: Record<string, Touch> = { ...s.touched };
  touch(touched, item.id, item.op, now);
  for (const id of item.entities ?? []) touch(touched, id, item.op, now);
  return { ...s, changes, cursor, touched };
}

/**
 * The cursor fell behind retention. Everything held about changes is stale, so
 * it is dropped; the server follows with a fresh snapshot and continues from
 * `head` on the same connection.
 */
export function applyReset(s: LiveData, r: ResetEvent): LiveData {
  return { ...s, changes: [], cursor: r.head, resets: s.resets + 1 };
}

// ---- pin ----------------------------------------------------------------

export function pinTo(s: LiveData, asOf: number | null): LiveData {
  if (s.asOf === asOf) return s;
  return { ...s, asOf, pinReason: null };
}
