// Wires the scene to the two stores that drive it.
//
// The live store says what the brain looks like and what just happened to it;
// the graph UI store says how it is being looked at. This file turns a change
// in either into the smallest call on the scene: a diff for structure, a light
// for each id newly touched, a refocus for a selection. Nothing here renders.

import type { ChangeOp } from "@/api";
import { useLive, edgeRef } from "@/live";
import { activityTone } from "@/lib/semantics";
import {
  buildAdjacency,
  computeFocus,
  edgeKey,
  edgeShown,
  mergeExplored,
  activityTargets,
  type Adjacency,
  type EdgeMap,
  type NodeMap,
} from "@/lib/graph/model";
import { readPalette } from "@/lib/graph/palette";
import type { GraphScene } from "@/lib/graph/scene";
import { useGraphUI } from "./state";

const LIGHT_MS: Record<ChangeOp, number> = {
  added: 4600,
  updated: 3600,
  superseded: 4200,
  retracted: 4200,
  merged: 4200,
  deleted: 2600,
};

export interface Merged {
  nodes: NodeMap;
  edges: EdgeMap;
  adj: Adjacency;
}

let memo: { n: NodeMap; e: EdgeMap; xn: NodeMap; xe: EdgeMap; value: Merged } | null = null;

/** The core graph with whatever exploring brought in, memoised on the identity of its four maps. */
export function mergedGraph(): Merged {
  const live = useLive.getState();
  const ex = useGraphUI.getState().explored;
  if (memo && memo.n === live.nodes && memo.e === live.edges && memo.xn === ex.nodes && memo.xe === ex.edges) return memo.value;
  const nodes = mergeExplored(live.nodes, ex.nodes);
  const edges = mergeExplored(live.edges, ex.edges);
  const value: Merged = { nodes, edges, adj: buildAdjacency(edges.values()) };
  memo = { n: live.nodes, e: live.edges, xn: ex.nodes, xe: ex.edges, value };
  return value;
}

export function bindScene(scene: GraphScene): () => void {
  const off: Array<() => void> = [];

  // ---- theme ----------------------------------------------------------
  const applyPalette = () => scene.setPalette(readPalette());
  applyPalette();
  const mo = new MutationObserver(applyPalette);
  mo.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
  off.push(() => mo.disconnect());

  // ---- structure and lights -------------------------------------------
  const refs = new Map<string, string>();
  const seenTouch = new Map<string, object>();
  const seenCalls = new Set<string>();
  let lastTouched = useLive.getState().touched;
  let lastActivity = useLive.getState().activity;
  for (const e of useLive.getState().activity) seenCalls.add(`${e.seq}:${e.at}`);

  const noteRefs = (edges: EdgeMap) => {
    for (const [k, e] of edges) refs.set(edgeRef(e), k);
    if (refs.size > 30000) refs.clear();
  };

  const pushStructure = () => {
    const g = mergedGraph();
    noteRefs(g.edges);
    scene.sync(g.nodes, g.edges);
  };

  const light = () => {
    const live = useLive.getState();
    if (live.pinned) return;
    const g = mergedGraph();
    const p = readPaletteCached();
    for (const [id, t] of Object.entries(live.touched)) {
      if (seenTouch.get(id) === t) continue;
      seenTouch.set(id, t);
      const color = p.ops[t.op];
      const ms = LIGHT_MS[t.op];
      if (scene.lightNodes([id], color, ms) > 0) continue;
      const key = refs.get(id) ?? (g.edges.has(id) ? id : undefined);
      if (!key) continue;
      scene.lightEdges([key], color, ms, t.op === "added" || t.op === "updated");
      const e = g.edges.get(key);
      if (e) scene.lightNodes([e.source, e.target], color, ms * 0.8);
    }
    if (seenTouch.size > 4000) seenTouch.clear();

    if (live.activity !== lastActivity) {
      lastActivity = live.activity;
      let n = 0;
      for (const ev of live.activity) {
        const key = `${ev.seq}:${ev.at}`;
        if (seenCalls.has(key)) break;
        seenCalls.add(key);
        if (n++ >= 8) continue;
        const color = p.kinds[activityTone(ev).key as keyof typeof p.kinds];
        const hit = activityTargets(g.nodes, g.adj, ev);
        scene.lightNodes(hit.nodes, color, 3200);
        scene.lightEdges(hit.edges, color, 3200, ev.kind === "relate");
      }
      if (seenCalls.size > 2000) seenCalls.clear();
    }
  };

  let palette = readPalette();
  const readPaletteCached = () => palette;
  const refreshPalette = new MutationObserver(() => {
    palette = readPalette();
  });
  refreshPalette.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
  off.push(() => refreshPalette.disconnect());

  pushStructure();

  off.push(
    useLive.subscribe((s, prev) => {
      const structural = s.nodes !== prev.nodes || s.edges !== prev.edges;
      if (structural) pushStructure();
      if (structural || s.touched !== lastTouched || s.activity !== lastActivity || s.pinned !== prev.pinned) {
        lastTouched = s.touched;
        light();
      }
    }),
  );

  // ---- how it is looked at -------------------------------------------
  let focusQueued = false;
  const applyFocus = () => {
    focusQueued = false;
    const ui = useGraphUI.getState();
    const g = mergedGraph();
    scene.setFilters(ui.filters);
    scene.setFocus(
      computeFocus({
        nodes: g.nodes,
        adj: g.adj,
        path: ui.path,
        preview: ui.preview,
        selected: ui.selected,
        hovered: ui.hovered,
        highlight: ui.highlight,
        query: ui.query,
        edgeShown: (e) => edgeShown(ui.filters, e, g.nodes),
      }),
    );
  };
  const queueFocus = () => {
    if (focusQueued) return;
    focusQueued = true;
    requestAnimationFrame(applyFocus);
  };

  scene.setColorMode(useGraphUI.getState().colorMode);
  scene.setFlow(useGraphUI.getState().flow);
  scene.setLabels(useGraphUI.getState().labels);
  applyFocus();

  off.push(
    useGraphUI.subscribe((s, prev) => {
      if (s.explored !== prev.explored) {
        pushStructure();
        light();
      }
      if (s.colorMode !== prev.colorMode) scene.setColorMode(s.colorMode);
      if (s.flow !== prev.flow) scene.setFlow(s.flow);
      if (s.labels !== prev.labels) scene.setLabels(s.labels);
      if (
        s.selected !== prev.selected ||
        s.hovered !== prev.hovered ||
        s.highlight !== prev.highlight ||
        s.query !== prev.query ||
        s.preview !== prev.preview ||
        s.path !== prev.path ||
        s.filters !== prev.filters ||
        s.explored !== prev.explored
      ) {
        queueFocus();
      }
    }),
  );
  // Structure changed: the focus may name a node that is gone, or reach a new one.
  off.push(
    useLive.subscribe((s, prev) => {
      if (s.nodes !== prev.nodes || s.edges !== prev.edges) queueFocus();
    }),
  );

  return () => off.forEach((f) => f());
}

export { edgeKey };
