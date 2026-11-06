// The live rail's rows: changes and tool calls in one list, newest first, and
// the part of the scene each one points at.

import type { ActivityEvent, ChangeItem } from "@/api";
import { activityTone, changeOp, type Semantic } from "@/lib/semantics";
import { activityTargets, edgeKey, type Adjacency, type EdgeMap, type NodeMap } from "./model";
import { edgeRef } from "@/live/reducer";

export interface RailItem {
  /** Stable across renders: the change's seq, or the call's seq and time. */
  key: string;
  type: "change" | "activity";
  at: number;
  semantic: Semantic;
  text: string;
  /** Who did it: the producer of a change, the tool of a call. */
  by?: string;
  failed?: boolean;
  change?: ChangeItem;
  activity?: ActivityEvent;
}

export type RailFilter = "all" | "changes" | "calls";

export function changeText(c: ChangeItem): string {
  return c.summary || c.edge?.predicate || c.node?.label || c.id;
}

export function buildRailItems(
  changes: ReadonlyArray<ChangeItem>,
  activity: ReadonlyArray<ActivityEvent>,
  filter: RailFilter,
  limit: number,
): RailItem[] {
  const items: RailItem[] = [];
  if (filter !== "calls") {
    for (const c of changes.slice(0, limit)) {
      items.push({ key: `c:${c.seq}`, type: "change", at: c.at, semantic: changeOp(c.op), text: changeText(c), by: c.producer, change: c });
    }
  }
  if (filter !== "changes") {
    for (const e of activity.slice(0, limit)) {
      items.push({
        key: `a:${e.seq}:${e.at}`,
        type: "activity",
        at: e.at,
        semantic: activityTone(e),
        text: e.text || e.tool,
        by: e.tool,
        failed: e.failed,
        activity: e,
      });
    }
  }
  items.sort((a, b) => b.at - a.at);
  return items.slice(0, limit);
}

export interface Targets {
  nodes: Set<string>;
  edges: Set<string>;
}

/** The nodes and edges of the current scene that a rail row touched. */
export function railTargets(item: RailItem, nodes: NodeMap, edges: EdgeMap, adj: Adjacency): Targets {
  const t: Targets = { nodes: new Set(), edges: new Set() };
  const addNode = (id: string | undefined) => {
    if (id && nodes.has(id)) t.nodes.add(id);
  };
  if (item.change) {
    const c = item.change;
    addNode(c.id);
    for (const id of c.entities ?? []) addNode(id);
    if (c.edge) {
      addNode(c.edge.source.id);
      addNode(c.edge.target.id);
      for (const e of adj.out.get(c.edge.source.id) ?? []) {
        if (e.target === c.edge.target.id && e.label === c.edge.predicate) t.edges.add(edgeKey(e));
      }
    }
    // A change may name an edge by its row id.
    for (const [k, e] of edges) {
      if (c.id && edgeRef(e) === c.id) {
        t.edges.add(k);
        addNode(e.source);
        addNode(e.target);
      }
    }
  } else if (item.activity) {
    const a = activityTargets(nodes, adj, item.activity);
    for (const id of a.nodes) t.nodes.add(id);
    for (const k of a.edges) t.edges.add(k);
  }
  return t;
}

/** The node a click on a row should select: the first thing it names that is on screen. */
export function railSelection(t: Targets, edges: EdgeMap): string | null {
  const first = t.nodes.values().next();
  if (!first.done) return first.value;
  for (const k of t.edges) {
    const e = edges.get(k);
    if (e) return e.source;
  }
  return null;
}
