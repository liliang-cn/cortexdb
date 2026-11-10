// What the graph page remembers about how it is being looked at: the
// selection, what an answer lit, the filters, which panels are open. The graph
// itself stays in the live store; this is only the view of it.

import { create } from "zustand";
import type { AskAnswer, CypherAnswer, Edge, FindResult, Node } from "@/api";
import { NO_FILTERS, type Filters } from "@/lib/graph/model";
import type { ColorMode } from "@/lib/graph/scene";
import type { Targets } from "@/lib/graph/rail";
import type { PathNote } from "@/lib/graphStrings";
import { pageShown } from "@/lib/pages";

export type ExploreMode = "find" | "ask" | "cypher";

export type ExploreResult =
  | { status: "idle" }
  | { status: "loading"; mode: ExploreMode }
  | { status: "error"; mode: ExploreMode; message: string }
  | { status: "find"; results: FindResult[] }
  | { status: "ask"; answer: AskAnswer }
  | { status: "cypher"; answer: CypherAnswer; fresh: number };

export interface Panels {
  explore: boolean;
  legend: boolean;
  rail: boolean;
  controls: boolean;
  contract: boolean;
}

export type MobilePanel = "none" | "inspector" | "rail" | "legend" | "controls" | "results" | "contract";

export type PathPick = "off" | "first" | "second";

const PANELS_KEY = "cortexdb.liveview.graph.panels.v1";

interface Stored {
  panels?: Partial<Panels>;
  flow?: boolean;
  labels?: boolean;
  showCoOccurs?: boolean;
}

function readStored(): Stored {
  try {
    const raw = localStorage.getItem(PANELS_KEY);
    return raw ? (JSON.parse(raw) as Stored) : {};
  } catch {
    return {};
  }
}

/** Panels start open on a desktop and shut on a phone, until a person decides. */
export function defaultPanels(narrow: boolean): Panels {
  return { explore: true, legend: !narrow, rail: !narrow, controls: false, contract: false };
}

export const isNarrow = () => typeof window !== "undefined" && window.matchMedia("(max-width: 767px)").matches;

export function readOptions(search: string) {
  const q = new URLSearchParams(search);
  const flag = (k: string) => (q.has(k) ? q.get(k) !== "0" : undefined);
  const spin = Number(q.get("spin"));
  return {
    panels: q.get("panels") !== "0",
    explore: q.get("explore") !== "0",
    contract: q.get("contract") !== "0",
    glow: flag("glow"),
    flow: flag("flow"),
    fit: q.get("fit") !== "0",
    spin: Number.isFinite(spin) && spin > 0 ? spin : 0,
    color: (q.get("color") === "grade" || (q.get("color") === "recalls" && pageShown("/recalls")) ? q.get("color") : "type") as ColorMode,
    bg: /^[0-9a-f]{6}$/i.test(q.get("bg") ?? "") ? `#${q.get("bg")}` : null,
    q: q.get("q") ?? "",
    type: q.get("type") ?? "",
    edge: q.get("edge") ?? "",
    focus: q.get("focus") ?? "",
    hops: Math.max(0, Math.min(3, Number(q.get("hops")) || 0)),
    find: q.get("find") ?? "",
    ask: q.get("ask") ?? "",
    cypher: q.get("cypher") ?? "",
    // Colouring by recalls and replaying one read the recall log, which goes
    // with the Recalls page when a host leaves it out; a link that asks for
    // either opens the graph without it.
    replay: pageShown("/recalls") ? (q.get("replay") ?? "") : "",
  };
}

export const OPTS = typeof location === "undefined" ? readOptions("") : readOptions(location.search);

export interface GraphUI {
  selected: string | null;
  selectedEdge: string | null;
  hovered: string | null;
  hoverEdge: string | null;
  highlight: ReadonlySet<string>;
  highlightFly: number;
  query: string;
  preview: Targets | null;
  path: { nodes: ReadonlySet<string>; edges: ReadonlySet<string>; ids: string[] } | null;
  pick: PathPick;
  pathFrom: string | null;
  pathNote: PathNote;

  filters: Filters;
  colorMode: ColorMode;
  flow: boolean;
  labels: boolean;
  orbit: boolean;

  explored: { nodes: ReadonlyMap<string, Node>; edges: ReadonlyMap<string, Edge> };
  mode: ExploreMode;
  result: ExploreResult;
  resultsOpen: boolean;
  lastQuery: { mode: ExploreMode; text: string } | null;

  panels: Panels;
  mobile: MobilePanel;
  toast: string;
  /** Bumped to ask the search box for focus. */
  focusSearch: number;

  set: (patch: Partial<GraphUI>) => void;
  setPanel: (name: keyof Panels, open: boolean) => void;
  toggleType: (type: string) => void;
  togglePredicate: (predicate: string) => void;
  setCoOccurs: (on: boolean) => void;
  showAll: () => void;
}

const stored = readStored();

export const useGraphUI = create<GraphUI>()((set, get) => ({
  selected: null,
  selectedEdge: null,
  hovered: null,
  hoverEdge: null,
  highlight: new Set(),
  highlightFly: 0,
  query: OPTS.q,
  preview: null,
  path: null,
  pick: "off",
  pathFrom: null,
  pathNote: { kind: "none" },

  filters: { ...NO_FILTERS, showCoOccurs: stored.showCoOccurs ?? false },
  colorMode: OPTS.color,
  flow: OPTS.flow ?? stored.flow ?? !isNarrow(),
  labels: stored.labels ?? true,
  orbit: false,

  explored: { nodes: new Map(), edges: new Map() },
  mode: "find",
  result: { status: "idle" },
  resultsOpen: true,
  lastQuery: null,

  panels: { ...defaultPanels(isNarrow()), ...stored.panels },
  mobile: "none",
  toast: "",
  focusSearch: 0,

  set: (patch) => set(patch),
  setPanel: (name, open) => {
    const panels = { ...get().panels, [name]: open };
    set({ panels });
    persist(get());
  },
  toggleType: (type) => {
    const f = get().filters;
    const hidden = new Set(f.hiddenTypes);
    if (!hidden.delete(type)) hidden.add(type);
    set({ filters: { ...f, hiddenTypes: hidden } });
  },
  togglePredicate: (predicate) => {
    const f = get().filters;
    const hidden = new Set(f.hiddenPredicates);
    if (!hidden.delete(predicate)) hidden.add(predicate);
    set({ filters: { ...f, hiddenPredicates: hidden } });
  },
  setCoOccurs: (on) => {
    set({ filters: { ...get().filters, showCoOccurs: on } });
    persist(get());
  },
  showAll: () => set({ filters: { ...get().filters, hiddenTypes: new Set(), hiddenPredicates: new Set() } }),
}));

export function persist(s: GraphUI): void {
  try {
    // Controls and the contract are popovers: they open when asked and are not remembered.
    const { explore, legend, rail } = s.panels;
    const payload: Stored = { panels: { explore, legend, rail }, flow: s.flow, labels: s.labels, showCoOccurs: s.filters.showCoOccurs };
    localStorage.setItem(PANELS_KEY, JSON.stringify(payload));
  } catch {
    // not remembered; the page still works
  }
}

useGraphUI.subscribe((s, prev) => {
  if (s.flow !== prev.flow || s.labels !== prev.labels) persist(s);
});
