// The ontology diagram's layout: a deterministic 2D drawing in lanes.
//
// WHY THIS IS NOT THE 3D SCENE ON THE GRAPH PAGE.
//
// The graph page is force-directed and rendered in WebGL because of what it
// draws: thousands of nodes with no inherent order, where the only structure
// worth showing is emergent (which clumps are dense, what sits at the centre).
// A force layout is the right tool for that, and a hand-placed one would be
// impossible.
//
// An ontology is the opposite graph on every axis that decides a layout:
//
//   - It is tens of nodes, not thousands, and every one has a name that must
//     be read. Text in a rotating 3D scene is either billboarded and
//     unreadable at depth, or readable and no longer where the thing is.
//   - Its structure is declared rather than emergent. An interface is a shape
//     several object types share; a link type has a direction and a
//     cardinality per side, and the ONE side is the one carrying the foreign
//     key. None of those are distances, and a force simulation can only
//     express distance: asked to draw "Airport implements Locatable" it puts
//     the two near each other, which is also what it does for two types that
//     merely have a lot of links.
//   - Its numbers are read by comparison. The gap the page exists for is a
//     quantity per box, and comparing quantities across refreshes needs the
//     boxes to be in the same place both times. A force layout settles
//     somewhere different on every load, by design.
//
// So: lanes. One lane per interface with its implementors beside it, which is
// what "these object types share a shape" looks like when it is drawn rather
// than annotated, and it needs no crossing lines because the grouping is the
// layout. Object types implementing nothing get the last lane. Link types are
// curves between the boxes, under them, with the multiplicity glyph at each
// end and the foreign key marked on the side that declares it. Types the store
// uses but the schema never declared are not drawn here at all; the page puts
// them in a band visibly outside the model, because that is what they are.
//
// Everything below is a pure function of the report and a width: the same
// report draws the same picture at the same width every time. Ordering comes
// off the schema as declared, because that order was authored and re-sorting
// it would throw away the only grouping the modeller expressed.

import type { OntologyAnswer, OntologyInterfaceNode, OntologyLinkEdge, OntologyLinkEnd, OntologyObjectNode } from "@/api/ontology";
import { num } from "./ontologyFinding";

export const BOX_W = 176;
export const BOX_H = 66;
// The gutters are where the curves run and their names sit, so they are wide
// enough to hold one: a curve squeezed into a hairline gap spends its whole
// length behind the boxes either side of it.
const GAP_X = 36;
const GAP_Y = 26;
/** The interface header beside a lane, in the wide layout. */
const LANE_W = 156;
const LANE_PAD = 16;
const PAD = 10;
const LANE_GAP = 44;
/** Above the first lane: a link bowed upwards out of the top row lands here rather than off the canvas. */
const TOP_ROOM = 54;
/** The interface header above a lane, in the compact layout. */
const HEAD_H = 40;
/**
 * Below this width the header moves from beside a lane to above it. Beside
 * it, the header and one box need 368 px, and a phone has about 340: the
 * diagram would open already scrolled sideways, with the box a reader came to
 * see cut off at the edge.
 */
export const COMPACT_BELOW = 560;

/** Case-folded, the way the ontology itself matches names (ontologyKey in Go). */
export const key = (s: string | null | undefined) => String(s ?? "").trim().toLowerCase();

export function clip(s: string | null | undefined, n: number): string {
  const t = String(s ?? "");
  return t.length > n ? `${t.slice(0, n - 1)}…` : t;
}

/**
 * The multiplicity glyph for one side. Per side, because that is how the
 * schema models it: the ONE side carries the key, and a reader given only
 * "one-to-many" cannot tell which end holds the column. A side the schema
 * left blank is "?", never a guessed MANY.
 */
export function cardinalityGlyph(cardinality: string): "1" | "∗" | "?" {
  if (cardinality === "ONE") return "1";
  if (cardinality === "MANY") return "∗";
  return "?";
}

export function cardinalityWord(cardinality: string): "one" | "many" | "?" {
  if (cardinality === "ONE") return "one";
  if (cardinality === "MANY") return "many";
  return "?";
}

/** Whether the store's own vocabulary was read, so a zero under a box means something. */
export const counted = (rep: OntologyAnswer) => !!rep.usage?.available;

export interface CountLabel {
  text: string;
  /** Declared, counted, and nothing in the store is of this type: drawn dashed. */
  unused: boolean;
  /** No count was taken, so there is no number to print, not even a zero. */
  uncounted: boolean;
}

/**
 * What sits in a box's corner. "unused" is a finding and only exists when the
 * count was taken; "not counted" is the absence of one. Printing "0" for both
 * would make a count nobody took look exactly like a count that came back
 * empty.
 */
export function countLabel(instances: number, isCounted: boolean): CountLabel {
  if (!isCounted) return { text: "not counted", unused: false, uncounted: true };
  if (!instances) return { text: "unused", unused: true, uncounted: false };
  return { text: num(instances), unused: false, uncounted: false };
}

/**
 * The diagram is drawn whenever there are boxes, and gated on nothing else.
 * Deliberately not on `saved`: a draft has object types and nothing saved,
 * and a guard asking whether somebody saved this would hide the whole point
 * of drafting it. For a saved report the two coincide anyway.
 */
export const drawsDiagram = (rep: OntologyAnswer) => rep.object_types.length > 0;

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Point {
  x: number;
  y: number;
}

export interface Lane {
  /** The interface this lane draws, or null for the lane of types implementing nothing. */
  iface: OntologyInterfaceNode | null;
  /** Its position in the schema's interfaces, which picks its colour; null for the loose lane. */
  colorIndex: number | null;
  /** The band behind the lane. */
  band: Rect;
  /** The header naming the interface. */
  header: Rect;
  /** Header lines, already clipped. */
  title: string;
  subtitle: string;
  footnote: string;
  boxes: string[];
}

export interface Box extends Rect {
  ot: OntologyObjectNode;
  /** The lane it sits in. */
  lane: number;
  colorIndex: number | null;
  name: string;
  pk: string;
  status: string | null;
  count: CountLabel;
  /** Fraction of the largest count, square-rooted; null when nothing was counted or nothing is there. */
  bar: number | null;
  /** The interfaces this type implements, as dots down its left border in their lanes' colours. */
  dots: Array<{ x: number; y: number; colorIndex: number; iface: string }>;
}

export interface LinkEndMark extends Point {
  side: "a" | "b";
  glyph: "1" | "∗" | "?";
  /** This side declares the foreign key. */
  fk: boolean;
  end: OntologyLinkEnd;
}

export interface LinkRoute {
  lt: OntologyLinkEdge;
  /** The SVG path of the curve. */
  d: string;
  /** Edges in the store carry it; drawn solid rather than dashed. */
  live: boolean;
  self: boolean;
  ends: [LinkEndMark, LinkEndMark];
  label: Rect & { text: string };
  /** The control point, which decides how far the canvas has to reach. */
  control: Point;
  /** The curve's ends, for placing the label along it. */
  from: Point;
  to: Point;
}

export interface Layout {
  /** The canvas: x from 0, y from top, which is above zero when nothing reaches the room kept over the first lane. */
  top: number;
  width: number;
  height: number;
  compact: boolean;
  lanes: Lane[];
  boxes: Map<string, Box>;
  links: LinkRoute[];
  /** Link types with a side naming a type this schema does not declare, which have nowhere to be drawn. */
  dangling: OntologyLinkEdge[];
}

interface LaneDraft {
  iface: OntologyInterfaceNode | null;
  colorIndex: number | null;
  types: OntologyObjectNode[];
}

/**
 * Which lane each object type sits in. Every type lands in exactly one, its
 * first declared interface that the schema has, so no box is drawn twice. The
 * others it implements travel with the box as dots, and lighting an interface
 * lights all of its implementors wherever they sit, which is the part a single
 * lane cannot show.
 */
export function buildLanes(rep: OntologyAnswer): LaneDraft[] {
  const lanes: LaneDraft[] = rep.interfaces.map((iface, i) => ({ iface, colorIndex: i, types: [] }));
  const byName = new Map(rep.interfaces.map((it, i) => [key(it.api_name), lanes[i] as LaneDraft]));
  const loose: LaneDraft = { iface: null, colorIndex: null, types: [] };
  for (const ot of rep.object_types) {
    const home = (ot.implements ?? []).map((n) => byName.get(key(n))).find(Boolean);
    (home ?? loose).types.push(ot);
  }
  if (loose.types.length) lanes.push(loose);
  return lanes;
}

/** A point on a quadratic curve, for placing the glyphs a little way in from each end rather than under the box they touch. */
export function qpoint(p0: Point, c: Point, p1: Point, t: number): Point {
  const m = 1 - t;
  return { x: m * m * p0.x + 2 * m * t * c.x + t * t * p1.x, y: m * m * p0.y + 2 * m * t * c.y + t * t * p1.y };
}

/** Where a ray from a box's centre towards a point leaves the box, so a curve starts on the border instead of under the label. */
export function edgePoint(box: Rect, tx: number, ty: number): Point {
  const cx = box.x + box.w / 2;
  const cy = box.y + box.h / 2;
  const dx = tx - cx;
  const dy = ty - cy;
  if (dx === 0 && dy === 0) return { x: cx, y: cy };
  const sx = dx === 0 ? Infinity : box.w / 2 / Math.abs(dx);
  const sy = dy === 0 ? Infinity : box.h / 2 / Math.abs(dy);
  const s = Math.min(sx, sy);
  return { x: cx + dx * s, y: cy + dy * s };
}

const round = (n: number) => Math.round(n * 10) / 10;

function headerLines(lane: LaneDraft): { title: string; subtitle: string; footnote: string } {
  if (!lane.iface) return { title: "implements nothing", subtitle: `${lane.types.length} object ${lane.types.length === 1 ? "type" : "types"}`, footnote: "" };
  const it = lane.iface;
  const ext = it.extends ?? [];
  return {
    title: clip(it.api_name, 20),
    subtitle: clip(ext.length ? `extends ${clip(ext.join(", "), 16)}` : `${(it.properties ?? []).length} shared properties`, 24),
    footnote: `${(it.implementors ?? []).length} implementors`,
  };
}

/**
 * Lays the report out for a canvas of the given width. The width only decides
 * how many boxes a row holds and whether headers sit beside or above a lane;
 * nothing else about the picture depends on it.
 */
export function layout(rep: OntologyAnswer, available: number): Layout {
  const compact = available < COMPACT_BELOW;
  const width = Math.max(compact ? 2 * PAD + 20 + BOX_W : COMPACT_BELOW, Math.floor(available));
  const isCounted = counted(rep);
  const drafts = buildLanes(rep);
  const ifaceIndex = new Map(rep.interfaces.map((it, i) => [key(it.api_name), i]));
  const maxN = Math.max(1, ...rep.object_types.map((o) => o.instances || 0));

  const bodyX = compact ? PAD + 10 : PAD + LANE_W + LANE_PAD;
  const cols = Math.max(1, Math.floor((width - bodyX - PAD + GAP_X) / (BOX_W + GAP_X)));
  const gridW = Math.max(width, bodyX + cols * (BOX_W + GAP_X) + PAD);

  const boxes = new Map<string, Box>();
  const lanes: Lane[] = [];
  let y = TOP_ROOM;
  drafts.forEach((lane, li) => {
    const rows = Math.max(1, Math.ceil(lane.types.length / cols));
    const head = compact ? HEAD_H + 12 : 0;
    const boxTop = y + head;
    const h = head + rows * BOX_H + (rows - 1) * GAP_Y;
    const header: Rect = compact ? { x: PAD + 10, y, w: Math.min(gridW - 2 * PAD - 20, 280), h: HEAD_H } : { x: PAD + 10, y, w: LANE_W - 12, h: 54 };
    lanes.push({ iface: lane.iface, colorIndex: lane.colorIndex, band: { x: PAD, y: y - 8, w: gridW - 2 * PAD, h: h + 16 }, header, ...headerLines(lane), boxes: lane.types.map((t) => key(t.api_name)) });

    lane.types.forEach((ot, i) => {
      const c = i % cols;
      const r = Math.floor(i / cols);
      const bx = bodyX + c * (BOX_W + GAP_X);
      const by = boxTop + r * (BOX_H + GAP_Y);
      const count = countLabel(ot.instances || 0, isCounted);
      const dots: Box["dots"] = [];
      (ot.implements ?? []).forEach((n, slot) => {
        const idx = ifaceIndex.get(key(n));
        if (idx !== undefined) dots.push({ x: bx + 5, y: by + 15 + slot * 9, colorIndex: idx, iface: key(n) });
      });
      boxes.set(key(ot.api_name), {
        x: bx,
        y: by,
        w: BOX_W,
        h: BOX_H,
        ot,
        lane: li,
        colorIndex: lane.colorIndex,
        name: clip(ot.api_name, 21),
        pk: clip(`pk ${ot.primary_key || "—"}`, 26),
        status: ot.status && ot.status !== "active" ? ot.status : null,
        count,
        // Square-rooted: on a real store one type holds thousands and the rest
        // hold single figures, and a linear bar would draw every one of them
        // as nothing at all.
        bar: isCounted && ot.instances > 0 ? Math.sqrt(ot.instances) / Math.sqrt(maxN) : null,
        dots,
      });
    });
    y += h + LANE_GAP;
  });

  const last = lanes[lanes.length - 1];
  let needW = gridW;
  let needH = last ? last.band.y + last.band.h + PAD : y;
  const seen = new Map<string, number>();
  const links: LinkRoute[] = [];
  const dangling: OntologyLinkEdge[] = [];
  for (const lt of rep.link_types) {
    const a = boxes.get(key(lt.a.object_type));
    const bb = boxes.get(key(lt.b.object_type));
    if (!a || !bb) {
      dangling.push(lt);
      continue;
    }
    // Parallel links between one pair are bowed by increasing amounts so they
    // stay countable rather than stacking into one line.
    const pair = `${key(lt.a.object_type)}|${key(lt.b.object_type)}`;
    const n = (seen.get(pair) ?? 0) + 1;
    seen.set(pair, n);
    const live = isCounted && (lt.instances || 0) > 0;

    // The name's width is estimated from its length, which is close enough
    // for the 10.5 px label font to keep names off each other; placeLabels
    // decides where along the curve it rides.
    const text = clip(lt.api_name, 22) + (isCounted ? `  ${num(lt.instances || 0)}` : "");
    const lw = text.length * 6.1 + 10;

    let p0: Point;
    let c: Point;
    let p1: Point;
    const self = a === bb;
    if (self) {
      // A link from a type to itself goes out to the right of the box rather
      // than over the top: a lane is only as tall as its boxes, so a loop
      // drawn above one lands inside the lane above and reads as a link
      // between two types that have none.
      const rx = a.x + BOX_W;
      const by = a.y + BOX_H;
      p0 = { x: rx - 30, y: by };
      p1 = { x: rx, y: by - 22 };
      c = { x: rx + 40 + (n - 1) * 22, y: by + 34 + (n - 1) * 14 };
    } else {
      const ac = { x: a.x + BOX_W / 2, y: a.y + BOX_H / 2 };
      const bc = { x: bb.x + BOX_W / 2, y: bb.y + BOX_H / 2 };
      const mx = (ac.x + bc.x) / 2;
      const my = (ac.y + bc.y) / 2;
      const dx = bc.x - ac.x;
      const dy = bc.y - ac.y;
      const len = Math.max(1, Math.hypot(dx, dy));
      let px = -dy / len;
      let py = dx / len;
      // Lanes stack vertically, and a lane with one implementor leaves the
      // whole width beside it empty, so a link between two lanes is close to
      // vertical and its natural bow is sideways. Sent rightwards on purpose:
      // leftwards is where the interface headers are, and an arc through them
      // crosses out the labels that say which lane is which. Only for the
      // near-vertical ones; a horizontal link inside one lane has no such free
      // side and keeps the bow the geometry gives it.
      const vertical = Math.abs(px) > Math.abs(py);
      if (vertical && px < 0) {
        px = -px;
        py = -py;
      }
      // The apex of a quadratic sits half the bow out from the chord, and it
      // is put in a gutter: beside the column for a near-vertical link, in the
      // corridor above or below the row for a near-horizontal one. A bow
      // shorter than that leaves the curve inside the column it was meant to
      // step out of, drawn behind the very boxes it runs past.
      const bow = (vertical ? BOX_W + GAP_X : BOX_H + GAP_Y) + (n - 1) * 28;
      c = { x: mx + px * bow, y: my + py * bow };
      p0 = edgePoint(a, c.x, c.y);
      p1 = edgePoint(bb, c.x, c.y);
    }
    const ea = qpoint(p0, c, p1, 0.12);
    const eb = qpoint(p0, c, p1, 0.88);
    const mid = qpoint(p0, c, p1, 0.5);
    links.push({
      lt,
      d: `M${round(p0.x)},${round(p0.y)} Q${round(c.x)},${round(c.y)} ${round(p1.x)},${round(p1.y)}`,
      live,
      self,
      ends: [
        { x: round(ea.x), y: round(ea.y), side: "a", glyph: cardinalityGlyph(lt.a.cardinality), fk: !!lt.a.foreign_key, end: lt.a },
        { x: round(eb.x), y: round(eb.y), side: "b", glyph: cardinalityGlyph(lt.b.cardinality), fk: !!lt.b.foreign_key, end: lt.b },
      ],
      label: { x: round(mid.x - lw / 2), y: round(mid.y - 9), w: round(lw), h: 17, text },
      control: { x: round(c.x), y: round(c.y) },
      from: p0,
      to: p1,
    });
  }

  placeLabels(links, [...Array.from(boxes.values()), ...lanes.map((l) => l.header)]);

  // Whatever the arcs and their names need, the canvas gives them: an arc
  // past the right edge would simply be cut off. The room kept above the
  // first lane is for links bowed upwards out of the top row; when none is,
  // the canvas starts lower rather than opening on an empty strip.
  let top = lanes.length ? (lanes[0] as Lane).band.y - PAD : 0;
  for (const r of links) {
    const mid = qpoint(r.from, r.control, r.to, 0.5);
    needW = Math.max(needW, r.control.x + 24, mid.x + 12, r.label.x + r.label.w + PAD);
    needH = Math.max(needH, mid.y + PAD, r.label.y + r.label.h + PAD);
    top = Math.min(top, mid.y - PAD, r.label.y - PAD, ...r.ends.map((e) => e.y - 8 - PAD));
  }
  top = Math.floor(top);
  needW = Math.ceil(needW);
  needH = Math.ceil(needH);
  // The lane bands were sized to the grid, not to the arcs that ended up
  // reaching past it; a band that stopped short would read as the lane ending
  // there.
  for (const lane of lanes) lane.band.w = needW - 2 * PAD;
  return { top, width: needW, height: needH - top, compact, lanes, boxes, links, dangling };
}

function overlap(a: Rect, b: Rect): number {
  const w = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
  const h = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
  return w > 0 && h > 0 ? w * h : 0;
}

/** Where along its curve a name may ride, nearest the middle first. */
const LABEL_STOPS = [0.5, 0.42, 0.58, 0.34, 0.66, 0.26, 0.74, 0.18, 0.82];

/**
 * How a name may hang off its point on the curve: centred on it, or starting
 * or ending just past it, so it still touches the curve it names.
 */
const LABEL_ANCHORS = [0.5, 0.06, 0.94];

/**
 * Slides each link's name along its own curve to the first place it covers
 * no box, no lane header, no end glyph and no name already placed, or the
 * place it covers least. Where several curves meet (a type linked to three
 * others, or to itself) their names would otherwise stack at the same
 * midpoint and read as one smudge. The name stays on its curve, so which
 * curve it names is still answered by looking; and the order is the schema's,
 * so the same report places the same names in the same places every time.
 */
function placeLabels(links: LinkRoute[], fixed: Rect[]) {
  const glyphs: Rect[] = links.flatMap((r) => r.ends.map((e) => ({ x: e.x - 8, y: e.y - 8, w: 16, h: 16 })));
  const placed: Rect[] = [];
  for (const r of links) {
    let best: Rect | null = null;
    let bestCost = Infinity;
    search: for (const anchor of LABEL_ANCHORS) {
      for (const t of LABEL_STOPS) {
        const p = qpoint(r.from, r.control, r.to, t);
        const cand: Rect = { x: round(p.x - r.label.w * anchor), y: round(p.y - r.label.h / 2), w: r.label.w, h: r.label.h };
        let cost = 0;
        for (const o of fixed) cost += overlap(cand, o);
        for (const o of glyphs) cost += overlap(cand, o);
        // Another name is the worst thing to cover: two names on top of each
        // other are two unreadable names.
        for (const o of placed) cost += 4 * overlap(cand, o);
        if (cost < bestCost) {
          best = cand;
          bestCost = cost;
        }
        if (cost === 0) break search;
      }
    }
    if (best) {
      r.label = { ...best, text: r.label.text };
      placed.push(best);
    }
  }
}

/**
 * The object types an interface lights: its implementors, Extends already
 * followed by Go. Null when no interface is chosen, so nothing is dimmed.
 */
export function litBy(rep: OntologyAnswer, iface: string | null): Set<string> | null {
  if (!iface) return null;
  const it = rep.interfaces.find((i) => key(i.api_name) === key(iface));
  return new Set((it?.implementors ?? []).map(key));
}

export type Selection = { kind: "object" | "link" | "interface"; name: string };

/**
 * The thing a selection names in this report, or null. A selection is kept
 * across a refresh by name, so the panel a reader had open on the type they
 * were studying stays open; a name the new report no longer has resolves to
 * nothing rather than to whatever the panel showed last.
 */
export function resolveSelection(rep: OntologyAnswer, sel: Selection | null) {
  if (!sel) return null;
  const k = key(sel.name);
  if (sel.kind === "object") {
    const ot = rep.object_types.find((o) => key(o.api_name) === k);
    return ot ? ({ kind: "object", ot } as const) : null;
  }
  if (sel.kind === "link") {
    const lt = rep.link_types.find((l) => key(l.api_name) === k);
    return lt ? ({ kind: "link", lt } as const) : null;
  }
  const it = rep.interfaces.find((i) => key(i.api_name) === k);
  return it ? ({ kind: "interface", it } as const) : null;
}
