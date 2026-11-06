// Names on the canvas.
//
// A brain is read by its names, and a hover tooltip shows one at a time. These
// labels are plain HTML laid over the canvas and placed each frame from the
// scene's own projection: the focused node and its neighbours, the predicate on
// each of its edges, whatever an answer lit, and (with nothing in focus) the
// hubs, so a first look has landmarks. Everything is capped and placed greedily
// in priority order, and a label that would land on one already placed is
// skipped rather than drawn on top of it.

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

const NODE_CLASS =
  "pointer-events-none absolute left-0 top-0 max-w-[15rem] truncate whitespace-nowrap rounded px-1 text-[11px] font-medium leading-[18px] text-foreground " +
  "[text-shadow:0_0_3px_var(--background),0_0_3px_var(--background),0_0_6px_var(--background)] will-change-transform";
const CENTRE_CLASS = " text-[12.5px] font-semibold";
const EDGE_CLASS =
  "pointer-events-none absolute left-0 top-0 whitespace-nowrap rounded-full border bg-card/95 px-1.5 text-[10.5px] font-medium leading-[16px] shadow-sm will-change-transform";

const clip = (s: string, n: number) => (s.length > n ? `${s.slice(0, n - 1)}…` : s);

function overlaps(a: Box, placed: Box[]): boolean {
  for (const q of placed) {
    if (a.x < q.x + q.w + 4 && q.x < a.x + a.w + 4 && a.y < q.y + q.h + 2 && q.y < a.y + a.h + 2) return true;
  }
  return false;
}

export class LabelLayer {
  private readonly pool = new Map<string, HTMLElement>();

  constructor(private readonly root: HTMLElement) {}

  update(host: LabelHost): void {
    const { width, height } = host.size();
    const keep = new Set<string>();
    const placed: Box[] = [];
    const f = host.focus;

    const place = (key: string, text: string, cls: string, px: number, py: number, w: number, h: number, anchor: "above" | "centre", style?: (el: HTMLElement) => void) => {
      if (px < -60 || py < -20 || px > width + 60 || py > height + 20) return false;
      const box: Box = { x: px - w / 2, y: anchor === "above" ? py - h : py - h / 2, w, h };
      if (overlaps(box, placed)) return false;
      placed.push(box);
      let el = this.pool.get(key);
      if (!el) {
        el = document.createElement("div");
        this.root.appendChild(el);
        this.pool.set(key, el);
      }
      if (el.textContent !== text) el.textContent = text;
      if (el.dataset.cls !== cls) {
        el.className = cls;
        el.dataset.cls = cls;
        style?.(el);
      }
      el.style.transform = `translate(${Math.round(px)}px,${Math.round(anchor === "above" ? py : py)}px) translate(-50%,${anchor === "above" ? "-100%" : "-50%"})`;
      keep.add(key);
      return true;
    };

    const nodeLabel = (n: LabelNode, centre: boolean) => {
      if (n.hidden || n.dying || n.x === undefined || n.y === undefined || n.z === undefined) return;
      const p = host.project(n.x, n.y, n.z);
      if (!p) return;
      const text = clip(n.label, 30);
      place(`n:${n.id}`, text, NODE_CLASS + (centre ? CENTRE_CLASS : ""), p.x, p.y - n.radius * 0.9 - 4, Math.min(240, text.length * 6.6 + 14), 19, "above");
    };

    // Edge labels first: they are the point of a selection, and a name can
    // go elsewhere.
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

    if (f.labelled.size > 0) {
      // Parallel relations between the same two nodes would sit on one
      // another at the midpoint; they are fanned out one line apart.
      const fan = new Map<string, number>();
      let drawn = 0;
      for (const key of f.labelled) {
        if (drawn >= (host.compact ? 10 : 28)) break;
        const l = host.links.get(key);
        if (!l || l.hidden || l.dying) continue;
        const s = typeof l.source === "object" ? l.source : host.nodes.get(l.source);
        const t = typeof l.target === "object" ? l.target : host.nodes.get(l.target);
        if (!s || !t || s.x === undefined || t.x === undefined || s.y === undefined || t.y === undefined || s.z === undefined || t.z === undefined) continue;
        const p = host.project((s.x + t.x) / 2, (s.y + t.y) / 2, (s.z + t.z) / 2);
        if (!p) continue;
        const pair = s.id < t.id ? `${s.id}|${t.id}` : `${t.id}|${s.id}`;
        const idx = fan.get(pair) ?? 0;
        fan.set(pair, idx + 1);
        const text = clip(l.label || "related", 22);
        const color = predicateColor(l.label);
        const ok = place(`e:${key}`, text, EDGE_CLASS, p.x, p.y + idx * 18, Math.min(190, text.length * 6.2 + 16), 17, "centre", (el) => {
          el.style.borderColor = color;
          el.style.color = color;
        });
        if (ok) drawn++;
      }
    }

    for (const id of nodeOrder) {
      if (id === f.centre) continue;
      const n = host.nodes.get(id);
      if (n) nodeLabel(n, false);
    }

    for (const [key, el] of this.pool) {
      if (!keep.has(key)) {
        el.remove();
        this.pool.delete(key);
      }
    }
  }

  clear(): void {
    for (const el of this.pool.values()) el.remove();
    this.pool.clear();
  }
}
