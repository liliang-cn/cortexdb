// The pure rules behind the graph scene: what is connected to what, what is
// in focus, what is filtered out, and what changed between two stores. None of
// it knows about three.js, React or the DOM, so each rule is a unit test.

import type { ActivityEvent, Edge, Node } from "@/api";
import { edgeKey } from "@/live/reducer";
import { CO_OCCURS } from "./colors";

export { edgeKey };

export type NodeMap = ReadonlyMap<string, Node>;
export type EdgeMap = ReadonlyMap<string, Edge>;

// ---- adjacency ----------------------------------------------------------

export interface Adjacency {
  out: ReadonlyMap<string, Edge[]>;
  in: ReadonlyMap<string, Edge[]>;
  /** Distinct edges touching a node, either way. */
  degree: ReadonlyMap<string, number>;
}

export function buildAdjacency(edges: Iterable<Edge>): Adjacency {
  const out = new Map<string, Edge[]>();
  const inn = new Map<string, Edge[]>();
  const degree = new Map<string, number>();
  const push = (m: Map<string, Edge[]>, k: string, e: Edge) => {
    const list = m.get(k);
    if (list) list.push(e);
    else m.set(k, [e]);
  };
  for (const e of edges) {
    push(out, e.source, e);
    push(inn, e.target, e);
    degree.set(e.source, (degree.get(e.source) ?? 0) + 1);
    if (e.target !== e.source) degree.set(e.target, (degree.get(e.target) ?? 0) + 1);
  }
  return { out, in: inn, degree };
}

export interface Region {
  nodes: Set<string>;
  /** edgeKey()s. */
  edges: Set<string>;
}

/** A node, the edges touching it, and the other end of each. */
export function neighbourhood(adj: Adjacency, id: string, keep: (e: Edge) => boolean = () => true): Region {
  const nodes = new Set<string>([id]);
  const edges = new Set<string>();
  for (const list of [adj.out.get(id), adj.in.get(id)]) {
    for (const e of list ?? []) {
      if (!keep(e)) continue;
      edges.add(edgeKey(e));
      nodes.add(e.source);
      nodes.add(e.target);
    }
  }
  return { nodes, edges };
}

/** Shortest path on the undirected graph: "how are these connected" ignores direction. */
export function tracePath(adj: Adjacency, a: string, b: string): { nodes: string[]; edges: string[] } | null {
  if (a === b) return { nodes: [a], edges: [] };
  const prev = new Map<string, { from: string; edge: string }>();
  const seen = new Set<string>([a]);
  const queue = [a];
  for (let i = 0; i < queue.length && !prev.has(b); i++) {
    const cur = queue[i] as string;
    for (const list of [adj.out.get(cur), adj.in.get(cur)]) {
      for (const e of list ?? []) {
        const next = e.source === cur ? e.target : e.source;
        if (seen.has(next)) continue;
        seen.add(next);
        prev.set(next, { from: cur, edge: edgeKey(e) });
        queue.push(next);
      }
    }
  }
  if (!prev.has(b)) return null;
  const nodes = [b];
  const edges: string[] = [];
  for (let at = b; at !== a; ) {
    const step = prev.get(at);
    if (!step) break;
    edges.push(step.edge);
    nodes.push(step.from);
    at = step.from;
  }
  return { nodes: nodes.reverse(), edges: edges.reverse() };
}

// ---- focus --------------------------------------------------------------

export type FocusKind = "none" | "path" | "preview" | "selection" | "hover" | "highlight" | "query";

export interface Focus {
  kind: FocusKind;
  /** Nodes drawn at full strength; everything else recedes. */
  nodes: ReadonlySet<string>;
  /** Edges drawn at full strength. */
  edges: ReadonlySet<string>;
  /** Edges that carry their predicate as a label. */
  labelled: ReadonlySet<string>;
  /** The node the focus is centred on, when it has one. */
  centre: string | null;
}

export const NO_FOCUS: Focus = {
  kind: "none",
  nodes: new Set(),
  edges: new Set(),
  labelled: new Set(),
  centre: null,
};

export interface FocusInput {
  nodes: NodeMap;
  adj: Adjacency;
  path: { nodes: ReadonlySet<string>; edges: ReadonlySet<string> } | null;
  /** A rail row under the pointer. */
  preview: { nodes: ReadonlySet<string>; edges: ReadonlySet<string> } | null;
  selected: string | null;
  hovered: string | null;
  /** Ids an answer lit (find, ask, cypher). */
  highlight: ReadonlySet<string>;
  /** The name filter, lower-cased. */
  query: string;
  /** Edges a filter removed never take part in a neighbourhood. */
  edgeShown: (e: Edge) => boolean;
}

function edgesWithin(adj: Adjacency, ids: ReadonlySet<string>, shown: (e: Edge) => boolean): Set<string> {
  const out = new Set<string>();
  for (const id of ids) {
    for (const e of adj.out.get(id) ?? []) if (ids.has(e.target) && shown(e)) out.add(edgeKey(e));
  }
  return out;
}

/**
 * What the scene should emphasise right now. The order is the order of
 * intent: a traced path beats a row being previewed, which beats the selected
 * node, which beats a passing hover, which beats an answer's lit set, which
 * beats the name filter.
 */
export function computeFocus(i: FocusInput): Focus {
  if (i.path) {
    return { kind: "path", nodes: i.path.nodes, edges: i.path.edges, labelled: i.path.edges, centre: null };
  }
  if (i.preview && (i.preview.nodes.size > 0 || i.preview.edges.size > 0)) {
    return { kind: "preview", nodes: i.preview.nodes, edges: i.preview.edges, labelled: i.preview.edges, centre: null };
  }
  const centred = (kind: "selection" | "hover", id: string): Focus | null => {
    if (!i.nodes.has(id)) return null;
    const r = neighbourhood(i.adj, id, i.edgeShown);
    return { kind, nodes: r.nodes, edges: r.edges, labelled: r.edges, centre: id };
  };
  if (i.selected) {
    const f = centred("selection", i.selected);
    if (f) return f;
  }
  if (i.hovered) {
    const f = centred("hover", i.hovered);
    if (f) return f;
  }
  if (i.highlight.size > 0) {
    const nodes = new Set<string>();
    for (const id of i.highlight) if (i.nodes.has(id)) nodes.add(id);
    if (nodes.size > 0) {
      return { kind: "highlight", nodes, edges: edgesWithin(i.adj, nodes, i.edgeShown), labelled: new Set(), centre: null };
    }
  }
  const q = i.query.trim().toLowerCase();
  if (q) {
    const nodes = new Set<string>();
    for (const n of i.nodes.values()) {
      if (n.label.toLowerCase().includes(q) || n.id.toLowerCase().includes(q)) nodes.add(n.id);
    }
    return { kind: "query", nodes, edges: edgesWithin(i.adj, nodes, i.edgeShown), labelled: new Set(), centre: null };
  }
  return NO_FOCUS;
}

// ---- filters ------------------------------------------------------------

export interface Filters {
  hiddenTypes: ReadonlySet<string>;
  hiddenPredicates: ReadonlySet<string>;
  /** `co_occurs` only means two names appeared together, so it is off until asked for. */
  showCoOccurs: boolean;
}

export const NO_FILTERS: Filters = { hiddenTypes: new Set(), hiddenPredicates: new Set(), showCoOccurs: false };

/** A type is keyed by its name; a node with none is "(untyped)". */
export const UNTYPED = "(untyped)";
export const typeKey = (n: Pick<Node, "type">) => n.type || UNTYPED;

export function nodeShown(f: Filters, n: Node): boolean {
  return !f.hiddenTypes.has(typeKey(n));
}

export function predicateShown(f: Filters, predicate: string): boolean {
  if (predicate === CO_OCCURS) return f.showCoOccurs && !f.hiddenPredicates.has(predicate);
  return !f.hiddenPredicates.has(predicate);
}

export function edgeShown(f: Filters, e: Edge, nodes: NodeMap): boolean {
  if (!predicateShown(f, e.label)) return false;
  const s = nodes.get(e.source);
  const t = nodes.get(e.target);
  return !!s && !!t && nodeShown(f, s) && nodeShown(f, t);
}

export interface CountRow {
  key: string;
  count: number;
}

const byCountThenName = (a: CountRow, b: CountRow) => b.count - a.count || (a.key < b.key ? -1 : a.key > b.key ? 1 : 0);

export function typeCounts(nodes: Iterable<Node>): CountRow[] {
  const m = new Map<string, number>();
  for (const n of nodes) m.set(typeKey(n), (m.get(typeKey(n)) ?? 0) + 1);
  return [...m].map(([key, count]) => ({ key, count })).sort(byCountThenName);
}

export function predicateCounts(edges: Iterable<Edge>): CountRow[] {
  const m = new Map<string, number>();
  for (const e of edges) {
    const k = e.label || "related";
    m.set(k, (m.get(k) ?? 0) + 1);
  }
  return [...m].map(([key, count]) => ({ key, count })).sort(byCountThenName);
}

// ---- store to scene -----------------------------------------------------

export interface GraphDiff {
  addedNodes: Node[];
  changedNodes: Node[];
  removedNodes: string[];
  addedEdges: Edge[];
  changedEdges: Edge[];
  /** edgeKey()s. */
  removedEdges: string[];
  empty: boolean;
}

/**
 * What differs between what the scene holds and what the store has now.
 *
 * The store keeps the very same object for a row that did not change, so
 * "changed" is an identity comparison and a store update that touched one node
 * costs one entry here, not a rebuild. An edge whose end is not a node is not
 * drawn, so it counts as absent.
 */
export function diffGraph(
  prevNodes: ReadonlyMap<string, Node>,
  prevEdges: ReadonlyMap<string, Edge>,
  nextNodes: NodeMap,
  nextEdges: EdgeMap,
): GraphDiff {
  const d: GraphDiff = {
    addedNodes: [],
    changedNodes: [],
    removedNodes: [],
    addedEdges: [],
    changedEdges: [],
    removedEdges: [],
    empty: true,
  };
  for (const [id, n] of nextNodes) {
    const old = prevNodes.get(id);
    if (!old) d.addedNodes.push(n);
    else if (old !== n) d.changedNodes.push(n);
  }
  for (const id of prevNodes.keys()) if (!nextNodes.has(id)) d.removedNodes.push(id);

  const drawable = (e: Edge) => nextNodes.has(e.source) && nextNodes.has(e.target);
  for (const [k, e] of nextEdges) {
    if (!drawable(e)) continue;
    const old = prevEdges.get(k);
    if (!old) d.addedEdges.push(e);
    else if (old !== e) d.changedEdges.push(e);
  }
  for (const k of prevEdges.keys()) {
    const next = nextEdges.get(k);
    if (!next || !drawable(next)) d.removedEdges.push(k);
  }
  d.empty =
    !d.addedNodes.length &&
    !d.changedNodes.length &&
    !d.removedNodes.length &&
    !d.addedEdges.length &&
    !d.changedEdges.length &&
    !d.removedEdges.length;
  return d;
}

/** The core graph with whatever exploring brought in beside it; the core wins on a clash. */
export function mergeExplored<T>(core: ReadonlyMap<string, T>, extra: ReadonlyMap<string, T>): ReadonlyMap<string, T> {
  if (extra.size === 0) return core;
  const out = new Map(core);
  for (const [k, v] of extra) if (!out.has(k)) out.set(k, v);
  return out;
}

// ---- tool-call terms ----------------------------------------------------

const TERM_FUZZY_CAP = 6;
const TERM_TOTAL_CAP = 24;

/**
 * Terms come from a tool's arguments, which name things the way a person
 * does; the graph keys them the way a database does. Exact label matches are
 * taken first and always; a substring only fills in when a term named nothing
 * exactly, and is capped hard, so a pulse never covers half the graph.
 */
export function matchTerms(nodes: NodeMap, terms: ReadonlyArray<string> | undefined): string[] {
  if (!terms?.length) return [];
  const found: string[] = [];
  const seen = new Set<string>();
  for (const term of terms) {
    if (found.length >= TERM_TOTAL_CAP) break;
    const t = String(term).toLowerCase();
    if (t.length < 2) continue;
    const exact: string[] = [];
    const fuzzy: string[] = [];
    for (const n of nodes.values()) {
      const label = n.label.toLowerCase();
      if (label === t) exact.push(n.id);
      else if (fuzzy.length < TERM_FUZZY_CAP && t.length >= 4 && (label.includes(t) || n.id.toLowerCase().includes(t))) fuzzy.push(n.id);
    }
    for (const id of (exact.length ? exact : fuzzy).slice(0, TERM_TOTAL_CAP - found.length)) {
      if (!seen.has(id)) {
        seen.add(id);
        found.push(id);
      }
    }
  }
  return found;
}

/** One name to one node: its label exactly, else an id that contains it. */
export function findNodeByName(nodes: NodeMap, name: string): string | null {
  const t = name.toLowerCase();
  for (const n of nodes.values()) if (n.label.toLowerCase() === t) return n.id;
  for (const n of nodes.values()) if (n.id.toLowerCase().includes(t)) return n.id;
  return null;
}

/** The edges between two nodes, either way round. */
export function edgesBetween(adj: Adjacency, a: string, b: string): Edge[] {
  return [...(adj.out.get(a) ?? []).filter((e) => e.target === b), ...(adj.out.get(b) ?? []).filter((e) => e.target === a)];
}

/** The most nodes one event lights by id. */
const EXACT_NODE_CAP = 200;

/**
 * What an activity event names in the current graph: nodes by id and by term,
 * edges by the pairs it linked. Ids come first and are taken as they are; the
 * terms add what they match beyond them.
 */
export function activityTargets(nodes: NodeMap, adj: Adjacency, ev: Pick<ActivityEvent, "terms" | "nodes" | "links">): { nodes: string[]; edges: string[] } {
  const seen = new Set<string>();
  const ids: string[] = [];
  for (const id of ev.nodes ?? []) {
    if (ids.length >= EXACT_NODE_CAP) break;
    if (nodes.has(id) && !seen.has(id)) {
      seen.add(id);
      ids.push(id);
    }
  }
  for (const id of matchTerms(nodes, ev.terms)) {
    if (!seen.has(id)) {
      seen.add(id);
      ids.push(id);
    }
  }
  const edges: string[] = [];
  for (const [from, to] of ev.links ?? []) {
    const a = findNodeByName(nodes, from);
    const b = findNodeByName(nodes, to);
    if (!a || !b) continue;
    for (const e of edgesBetween(adj, a, b)) edges.push(edgeKey(e));
  }
  return { nodes: ids, edges };
}
