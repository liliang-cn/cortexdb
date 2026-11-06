// Names on the canvas.
//
// A brain is read by its names, and a hover tooltip shows one at a time. These
// labels are plain HTML laid over the canvas and placed each frame from the
// scene's own projection: the focused node and its neighbours, the predicate on
// each of its edges, whatever an answer lit, and (with nothing in focus) the
// hubs, so a first look has landmarks. Everything is capped and placed greedily
// in priority order, and a label that would land on one already placed is
// skipped rather than drawn on top of it.
//
// Placed afresh every frame, that greedy pass flickers as soon as the camera
// moves: which of two neighbouring names wins changes from one frame to the
// next, so both blink. Two things keep the names still. Within each tier a
// name already on screen is placed before a newcomer, so a newcomer cannot
// push it off by drifting into it; and a name comes and goes by fading, so one
// that does lose its place leaves rather than vanishes.

import { predicateColor } from "./colors";
import type { Focus } from "./model";

export interface LabelNode {
  id: string;
  label: string;
  x?: number;
  y?: number;
  z?: number;
  degree: number;
  radius: number;
  hidden: boolean;
  dying: unknown;
}

export interface LabelLink {
  key: string;
  label: string;
  source: string | LabelNode;
  target: string | LabelNode;
  hidden: boolean;
  dying: unknown;
}

export interface LabelHost {
  focus: Focus;
  nodes: ReadonlyMap<string, LabelNode>;
  links: ReadonlyMap<string, LabelLink>;
  /** Screen position inside the container, or null behind the camera. */
  project(x: number, y: number, z: number): { x: number; y: number } | null;
  size(): { width: number; height: number };
  /** Nodes drawn large enough on screen to deserve a name, largest first. */
  nearby(): string[];
  hubs(): string[];
  /** Whether to label hubs and nearby nodes when nothing is in focus. */
  ambient: boolean;
  compact: boolean;
}

interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

/**
 * An edge label's height, and the distance between parallel ones: the height
 * plus more than the gap overlaps() keeps, or every line after the first would
 * count as landing on the one above it and never be drawn.
 */
const EDGE_H = 17;
const EDGE_LINE = EDGE_H + 3;

/** How long a name takes to come and to go. */
export const LABEL_FADE_MS = 160;

const FADE_CLASS = " transition-opacity duration-150 ease-out";
const NODE_CLASS =
  "pointer-events-none absolute left-0 top-0 max-w-[15rem] truncate whitespace-nowrap rounded px-1 text-[11px] font-medium leading-[18px] text-foreground " +
  "[text-shadow:0_0_3px_var(--background),0_0_3px_var(--background),0_0_6px_var(--background)] will-change-transform" +
  FADE_CLASS;
const CENTRE_CLASS = " text-[12.5px] font-semibold";
const EDGE_CLASS =
  "pointer-events-none absolute left-0 top-0 whitespace-nowrap rounded-full border bg-card/95 px-1.5 text-[10.5px] font-medium leading-[16px] shadow-sm will-change-transform" +
  FADE_CLASS;

const clip = (s: string, n: number) => (s.length > n ? `${s.slice(0, n - 1)}…` : s);

function overlaps(a: Box, placed: Box[]): boolean {
  for (const q of placed) {
    if (a.x < q.x + q.w + 4 && q.x < a.x + a.w + 4 && a.y < q.y + q.h + 2 && q.y < a.y + a.h + 2) return true;
  }
  return false;
}

/** One name to draw this frame, at its anchor in container pixels. */
export interface Placement {
  key: string;
  text: string;
  cls: string;
  x: number;
  y: number;
  anchor: "above" | "centre";
  /** An edge label's predicate colour. */
  color?: string;
}

/** Stable partition: the keys already on screen first, each group in its given order. */
function incumbentsFirst<T>(items: T[], key: (t: T) => string, shown: ReadonlySet<string>): T[] {
  if (shown.size === 0) return items;
  const on: T[] = [];
  const off: T[] = [];
  for (const it of items) (shown.has(key(it)) ? on : off).push(it);
  return on.concat(off);
}

/**
 * The names to draw this frame, in the order they were placed. `shown` is
 * what is on screen now: within a tier those keys are placed first, which is
 * what keeps a name from being displaced by one that merely moved into it.
 */
export function planLabels(host: LabelHost, shown: ReadonlySet<string> = new Set()): Placement[] {
  const { width, height } = host.size();
  const out: Placement[] = [];
  const placed: Box[] = [];
  const f = host.focus;

  const place = (p: Placement, w: number, h: number): boolean => {
    if (p.x < -60 || p.y < -20 || p.x > width + 60 || p.y > height + 20) return false;
    const box: Box = { x: p.x - w / 2, y: p.anchor === "above" ? p.y - h : p.y - h / 2, w, h };
    if (overlaps(box, placed)) return false;
    placed.push(box);
    out.push(p);
    return true;
  };

  const nodeLabel = (n: LabelNode, centre: boolean) => {
    if (n.hidden || n.dying || n.x === undefined || n.y === undefined || n.z === undefined) return;
    const p = host.project(n.x, n.y, n.z);
    if (!p) return;
    const text = clip(n.label, 30);
    place(
      { key: `n:${n.id}`, text, cls: NODE_CLASS + (centre ? CENTRE_CLASS : ""), x: p.x, y: p.y - n.radius * 0.9 - 4, anchor: "above" },
      Math.min(240, text.length * 6.6 + 14),
      19,
    );
  };

  const nodeOrder: string[] = [];
  if (f.centre) {
    nodeOrder.push(f.centre);
    const rest = [...f.nodes].filter((id) => id !== f.centre);
    rest.sort((a, b) => (host.nodes.get(b)?.degree ?? 0) - (host.nodes.get(a)?.degree ?? 0));
    nodeOrder.push(...rest.slice(0, host.compact ? 8 : 16));
  } else if (f.kind !== "none") {
    const ids = [...f.nodes].sort((a, b) => (host.nodes.get(b)?.degree ?? 0) - (host.nodes.get(a)?.degree ?? 0));
    nodeOrder.push(...ids.slice(0, host.compact ? 10 : 24));
  } else if (host.ambient) {
    nodeOrder.push(...host.hubs().slice(0, host.compact ? 0 : 10));
    if (!host.compact) nodeOrder.push(...host.nearby().slice(0, 24));
  }

  if (f.centre) {
    const centre = host.nodes.get(f.centre);
    if (centre) nodeLabel(centre, true);
  }

  // Edge labels next: they are the point of a selection, and a name can go
  // elsewhere.
  if (f.labelled.size > 0) {
    // Parallel relations between the same two nodes would sit on one another
    // at the midpoint; they are fanned out one line apart. The line each takes
    // is decided in the focus's own order, so placing incumbents first does
    // not move a label from one line to another.
    const fan = new Map<string, number>();
    const edges: { key: string; mid: { x: number; y: number }; line: number; text: string; color: string }[] = [];
    for (const key of f.labelled) {
      const l = host.links.get(key);
      if (!l || l.hidden || l.dying) continue;
      const s = typeof l.source === "object" ? l.source : host.nodes.get(l.source);
      const t = typeof l.target === "object" ? l.target : host.nodes.get(l.target);
      if (!s || !t || s.x === undefined || t.x === undefined || s.y === undefined || t.y === undefined || s.z === undefined || t.z === undefined) continue;
      const mid = host.project((s.x + t.x) / 2, (s.y + t.y) / 2, (s.z + t.z) / 2);
      if (!mid) continue;
      const pair = s.id < t.id ? `${s.id}|${t.id}` : `${t.id}|${s.id}`;
      const line = fan.get(pair) ?? 0;
      fan.set(pair, line + 1);
      edges.push({ key, mid, line, text: clip(l.label || "related", 22), color: predicateColor(l.label) });
    }
    let drawn = 0;
    for (const e of incumbentsFirst(edges, (e) => `e:${e.key}`, shown)) {
      if (drawn >= (host.compact ? 10 : 28)) break;
      const ok = place(
        { key: `e:${e.key}`, text: e.text, cls: EDGE_CLASS, x: e.mid.x, y: e.mid.y + e.line * EDGE_LINE, anchor: "centre", color: e.color },
        Math.min(190, e.text.length * 6.2 + 16),
        EDGE_H,
      );
      if (ok) drawn++;
    }
  }

  for (const id of incumbentsFirst(nodeOrder, (id) => `n:${id}`, shown)) {
    if (id === f.centre) continue;
    const n = host.nodes.get(id);
    if (n) nodeLabel(n, false);
  }
  return out;
}

interface Entry {
  el: HTMLElement;
  /** When it started to fade out, or null while it is placed. */
  leaving: number | null;
  /** Created this frame: shown on the next, so the fade in has a start. */
  fresh: boolean;
  /** Where the last placement put it relative to its anchor point. */
  dy: number;
}

export class LabelLayer {
  private readonly pool = new Map<string, Entry>();

  constructor(private readonly root: HTMLElement) {}

  update(host: LabelHost, now = performance.now()): void {
    const shown = new Set<string>();
    for (const [key, e] of this.pool) if (e.leaving === null) shown.add(key);

    const keep = new Set<string>();
    for (const p of planLabels(host, shown)) {
      let e = this.pool.get(p.key);
      if (!e) {
        const el = document.createElement("div");
        el.style.opacity = "0";
        this.root.appendChild(el);
        e = { el, leaving: null, fresh: true, dy: 0 };
        this.pool.set(p.key, e);
      } else if (e.fresh) {
        e.fresh = false;
        e.el.style.opacity = "1";
      } else if (e.leaving !== null) {
        e.leaving = null;
        e.el.style.opacity = "1";
      }
      const el = e.el;
      if (el.textContent !== p.text) el.textContent = p.text;
      if (el.dataset.cls !== p.cls) {
        el.className = p.cls;
        el.dataset.cls = p.cls;
        if (p.color) {
          el.style.borderColor = p.color;
          el.style.color = p.color;
        }
      }
      e.dy = p.y - this.anchorY(host, p.key);
      el.style.transform = transformOf(p.x, p.y, p.anchor);
      keep.add(p.key);
    }

    for (const [key, e] of this.pool) {
      if (keep.has(key)) continue;
      if (e.leaving === null) {
        e.leaving = now;
        e.fresh = false;
        e.el.style.opacity = "0";
      }
      // A name on its way out still follows what it names, so it fades where
      // the thing is rather than where it was.
      const at = this.anchorOf(host, key);
      if (!at || now - e.leaving >= LABEL_FADE_MS) {
        e.el.remove();
        this.pool.delete(key);
        continue;
      }
      e.el.style.transform = transformOf(at.x, at.y + e.dy, key.startsWith("e:") ? "centre" : "above");
    }
  }

  /** The screen point a key's label hangs from, before its own offset. */
  private anchorOf(host: LabelHost, key: string): { x: number; y: number } | null {
    if (key.startsWith("n:")) {
      const n = host.nodes.get(key.slice(2));
      if (!n || n.hidden || n.x === undefined || n.y === undefined || n.z === undefined) return null;
      return host.project(n.x, n.y, n.z);
    }
    const l = host.links.get(key.slice(2));
    if (!l || l.hidden) return null;
    const s = typeof l.source === "object" ? l.source : host.nodes.get(l.source);
    const t = typeof l.target === "object" ? l.target : host.nodes.get(l.target);
    if (!s || !t || s.x === undefined || t.x === undefined || s.y === undefined || t.y === undefined || s.z === undefined || t.z === undefined) return null;
    return host.project((s.x + t.x) / 2, (s.y + t.y) / 2, (s.z + t.z) / 2);
  }

  private anchorY(host: LabelHost, key: string): number {
    return this.anchorOf(host, key)?.y ?? 0;
  }

  clear(): void {
    for (const e of this.pool.values()) e.el.remove();
    this.pool.clear();
  }
}

function transformOf(x: number, y: number, anchor: "above" | "centre"): string {
  return `translate(${Math.round(x)}px,${Math.round(y)}px) translate(-50%,${anchor === "above" ? "-100%" : "-50%"})`;
}
