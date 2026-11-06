// Everything a person (or the page that frames this one) can ask the graph to
// do. Plain functions over the stores and the scene, so a search result, a
// rail row and a keyboard shortcut all take the same path.

import { api, getJSON, isAbort } from "@/api";
import type { Edge, Node } from "@/api";
import { useLive } from "@/live";
import { tracePath } from "@/lib/graph/model";
import { mergedGraph } from "./bind";
import { getScene } from "./controller";
import { OPTS, isNarrow, useGraphUI, type ExploreMode } from "./state";
import { edgeKey } from "@/lib/graph/model";

const ui = () => useGraphUI.getState();

// ---- toast --------------------------------------------------------------

let toastTimer: ReturnType<typeof setTimeout> | undefined;
export function toast(text: string): void {
  if (!text) return;
  ui().set({ toast: text });
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => ui().set({ toast: "" }), 2600);
}

const messageOf = (err: unknown) => (err instanceof Error ? err.message : String(err));

// ---- selection ----------------------------------------------------------

export function selectNode(id: string, opts: { fly?: boolean } = {}): void {
  ui().set({ selected: id, selectedEdge: null, mobile: "inspector", resultsOpen: false });
  if (opts.fly !== false) getScene()?.flyTo(id, 900, isNarrow());
}

export function selectEdge(key: string): void {
  ui().set({ selected: null, selectedEdge: key, mobile: "inspector" });
}

export function clearSelection(): void {
  ui().set({ selected: null, selectedEdge: null, mobile: "none" });
}

/** A click on a node: the path picker takes it first, otherwise it opens the inspector. */
export function onNodeClick(id: string): void {
  const s = ui();
  if (s.pick !== "off") {
    pickPathNode(id);
    return;
  }
  selectNode(id);
}

// ---- tracing a path -----------------------------------------------------

export function startPathPick(): void {
  ui().set({ pick: "first", pathFrom: null, path: null, pathNote: "Pick the first node." });
}

export function clearPath(): void {
  ui().set({ pick: "off", pathFrom: null, path: null, pathNote: "" });
}

function pickPathNode(id: string): void {
  const s = ui();
  const g = mergedGraph();
  if (s.pick === "first") {
    getScene()?.lightNodes([id], "#d97706", 60000);
    ui().set({ pick: "second", pathFrom: id, pathNote: `From ${g.nodes.get(id)?.label ?? id}. Pick the other end.` });
    return;
  }
  const from = s.pathFrom ?? id;
  const found = tracePath(g.adj, from, id);
  if (!found) {
    ui().set({ pick: "off", pathFrom: null, path: null, pathNote: "No path between them." });
    return;
  }
  const names = found.nodes.map((n) => g.nodes.get(n)?.label ?? n);
  ui().set({
    pick: "off",
    pathFrom: null,
    path: { nodes: new Set(found.nodes), edges: new Set(found.edges), ids: found.nodes },
    pathNote: `${found.nodes.length - 1} hop${found.nodes.length === 2 ? "" : "s"}: ${names.join(" → ")}`,
  });
}

// ---- lighting what an answer named --------------------------------------

export function setHighlight(ids: Iterable<string>, fly: boolean): void {
  const g = mergedGraph();
  const set = new Set<string>();
  for (const id of ids) if (g.nodes.has(id)) set.add(id);
  ui().set({ highlight: set, selected: set.size ? null : ui().selected });
  if (fly && set.size) {
    ui().set({ highlightFly: ui().highlightFly + 1 });
    getScene()?.fit(900, set);
  }
}

export function clearHighlight(): void {
  ui().set({ highlight: new Set() });
}

// ---- exploring the store ------------------------------------------------

/** Add nodes and relations an answer brought to the scene; returns the ids that were not there before. */
export function mergeIntoScene(nodes: Node[] | undefined, edges: Edge[] | undefined): string[] {
  const live = useLive.getState();
  const prev = ui().explored;
  const nn = new Map(prev.nodes);
  const ee = new Map(prev.edges);
  const fresh: string[] = [];
  for (const n of nodes ?? []) {
    if (!live.nodes.has(n.id) && !nn.has(n.id)) fresh.push(n.id);
    nn.set(n.id, { id: n.id, label: n.label || n.id, type: n.type ?? "", grade: n.grade });
  }
  for (const e of edges ?? []) ee.set(edgeKey(e), e);
  ui().set({ explored: { nodes: nn, edges: ee } });
  return fresh;
}

export async function expandNode(id: string, quiet: boolean): Promise<{ nodes: Node[]; edges: Edge[] }> {
  const live = useLive.getState();
  const nb = await api.expand(id, undefined, live.pinned ? (live.pinnedAt ?? undefined) : undefined);
  const fresh = mergeIntoScene(nb.nodes, nb.edges);
  const addedColor = "#15803d";
  requestAnimationFrame(() => getScene()?.lightNodes(fresh, addedColor, 5000));
  const label = mergedGraph().nodes.get(id)?.label ?? id;
  if (!quiet) toast(fresh.length ? `${fresh.length} new around ${label}` : `Nothing new around ${label}`);
  return nb;
}

/** Expand outward ring by ring: the node, then up to twelve of what that brought, and so on. Bounded: three unbounded hops from a hub is most of the store. */
async function expandRings(id: string, hops: number): Promise<void> {
  let frontier = [id];
  const seen = new Set([id]);
  for (let h = 0; h < hops && frontier.length; h++) {
    const batch = frontier.slice(0, h === 0 ? 1 : 12);
    frontier = [];
    const answers = await Promise.all(batch.map((x) => expandNode(x, true).catch(() => ({ nodes: [] as Node[], edges: [] as Edge[] }))));
    for (const nb of answers) for (const n of nb.nodes) if (!seen.has(n.id)) {
      seen.add(n.id);
      frontier.push(n.id);
    }
  }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Fly to a node and open it, fetching it first when it is not in the scene. */
export async function focusNode(raw: string, hops = 0): Promise<void> {
  const id = raw.trim();
  if (!id) return;
  const live = useLive.getState();
  if (!getScene()?.has(id) || hops > 0) {
    if (!live.caps.explore) {
      if (!getScene()?.has(id)) toast("Not in view, and this source cannot be explored.");
    } else {
      try {
        await expandRings(id, Math.max(1, hops));
      } catch (err) {
        toast(messageOf(err));
      }
    }
  }
  // A node that has just arrived has no settled position yet; give the layout a moment.
  for (let i = 0; i < 8 && !getScene()?.placed(id); i++) await sleep(150);
  if (!getScene()?.has(id)) {
    toast(`No node ${id}`);
    return;
  }
  selectNode(id);
  getScene()?.follow(id);
}

let exploreGen = 0;

export async function runExplore(mode: ExploreMode, raw: string): Promise<void> {
  const text = raw.trim();
  if (!text) return;
  const live = useLive.getState();
  ui().set({ mode, panels: { ...ui().panels, explore: true }, resultsOpen: true, mobile: isNarrow() ? "results" : ui().mobile });
  if (!live.caps.explore) {
    ui().set({ result: { status: "error", mode, message: "This view's source cannot be explored." } });
    return;
  }
  const gen = ++exploreGen;
  ui().set({ result: { status: "loading", mode }, lastQuery: { mode, text } });
  const asOf = live.pinned ? (live.pinnedAt ?? undefined) : undefined;
  try {
    if (mode === "find") {
      const r = await api.find(text, asOf);
      if (gen !== exploreGen) return;
      const results = r.results ?? [];
      ui().set({ result: { status: "find", results } });
      const inView = results.filter((n) => mergedGraph().nodes.has(n.id));
      setHighlight(results.map((n) => n.id), inView.length > 0);
    } else if (mode === "ask") {
      const answer = await api.ask(text);
      if (gen !== exploreGen) return;
      ui().set({ result: { status: "ask", answer } });
      setHighlight(answer.touched ?? [], true);
    } else {
      const answer = await api.cypher(text);
      if (gen !== exploreGen) return;
      const fresh = mergeIntoScene(answer.nodes, answer.edges);
      ui().set({ result: { status: "cypher", answer, fresh: fresh.length } });
      requestAnimationFrame(() => setHighlight((answer.nodes ?? []).map((n) => n.id), (answer.nodes ?? []).length > 0));
    }
  } catch (err) {
    if (gen !== exploreGen || isAbort(err)) return;
    ui().set({ result: { status: "error", mode, message: messageOf(err) } });
  }
}

export function clearExplore(): void {
  exploreGen++;
  ui().set({ result: { status: "idle" }, lastQuery: null, highlight: new Set() });
}

/** Bring in the nodes an answer named that the scene does not hold yet. */
export async function bringIn(ids: string[]): Promise<void> {
  await Promise.all(ids.slice(0, 8).map((x) => expandNode(x, true).catch(() => undefined)));
  requestAnimationFrame(() => setHighlight(ids, true));
}

// ---- sharing ------------------------------------------------------------

export function shareLink(pairs: Array<[string, string]>): void {
  const u = new URL(location.href);
  for (const k of ["focus", "find", "ask", "cypher"]) u.searchParams.delete(k);
  for (const [k, v] of pairs) if (v) u.searchParams.set(k, v);
  const link = u.toString();
  const done = () => toast("Link copied");
  if (navigator.clipboard?.writeText) navigator.clipboard.writeText(link).then(done, () => window.prompt("Link", link));
  else window.prompt("Link", link);
}

// ---- opening actions ----------------------------------------------------

let opened = false;

/** The URL options that open the page somewhere, run once, after the first picture exists. */
export function runOpeningActions(): void {
  if (opened) return;
  opened = true;
  const g = mergedGraph();
  if (OPTS.type) {
    const hidden = new Set<string>();
    for (const n of g.nodes.values()) if ((n.type || "(untyped)") !== OPTS.type) hidden.add(n.type || "(untyped)");
    ui().set({ filters: { ...ui().filters, hiddenTypes: hidden } });
  }
  if (OPTS.edge) {
    const hidden = new Set<string>();
    for (const e of g.edges.values()) if (e.label !== OPTS.edge) hidden.add(e.label);
    ui().set({ filters: { ...ui().filters, hiddenPredicates: hidden, showCoOccurs: ui().filters.showCoOccurs || OPTS.edge === "co_occurs" } });
  }
  if (OPTS.contract) ui().setPanel("contract", ui().panels.contract);
  const caps = useLive.getState().caps;
  if (!caps.explore) return;
  if (OPTS.replay) void replayRecall(OPTS.replay);
  if (OPTS.cypher) void runExplore("cypher", OPTS.cypher);
  else if (OPTS.ask) void runExplore("ask", OPTS.ask);
  else if (OPTS.find) void runExplore("find", OPTS.find);
  if (OPTS.focus) {
    const f = OPTS.focus;
    // An id names a node; anything else is a name, looked up first.
    if (g.nodes.has(f) || f.indexOf(":") > 0) void focusNode(f, OPTS.hops);
    else {
      api
        .find(f)
        .then((r) => {
          const first = r.results?.[0];
          if (first) void focusNode(first.id, OPTS.hops);
          else toast(`Nothing called ${f}`);
        })
        .catch((err) => toast(messageOf(err)));
    }
  }
}

interface RecallDetail {
  query: string;
  hits?: Array<{ node_id?: string }>;
}

export async function replayRecall(id: string): Promise<void> {
  try {
    const r = await getJSON<RecallDetail>("api/recalls/get", { id });
    const ids = (r.hits ?? []).map((h) => h.node_id).filter((x): x is string => !!x);
    const shown = ids.filter((x) => mergedGraph().nodes.has(x));
    setHighlight(shown, true);
    getScene()?.lightNodes(shown, "#4f46e5", 6000);
    toast(`Recall "${r.query}": ${ids.length} hits, ${shown.length} in this scene`);
  } catch (err) {
    toast(messageOf(err));
  }
}
