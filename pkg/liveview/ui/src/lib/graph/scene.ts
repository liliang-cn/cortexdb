// The 3D scene: 3d-force-graph and three behind a small imperative API.
//
// Three rules shape it, and the rest follows.
//
//   1. Nothing is rebuilt on a store update. The scene keeps its own render
//      objects keyed by id (3d-force-graph writes positions and link ends into
//      the objects it is given, so it is never handed the store's), applies
//      the difference between two stores, and hands the library new arrays
//      only when a node or an edge was added or finished fading out.
//   2. Appearance is state, not accessors. Every node owns its material and
//      every link its own, so a selection, a filter, a colour mode or a flash
//      is written straight into the materials of the things that changed.
//      Re-setting a library accessor re-reads every node and link (about
//      66 ms on a 2,000-node graph), and that is done only for arrowheads,
//      which the library offers no other way to add or remove.
//   3. Nothing animates that does not have to. One frame loop drives flashes,
//      fades, pulses and labels; it sleeps while the tab is hidden, and the
//      library's own loop is paused with it.

import ForceGraph3D from "3d-force-graph";
import {
  CanvasTexture,
  Color,
  Mesh,
  MeshBasicMaterial,
  MeshLambertMaterial,
  SphereGeometry,
  Sprite,
  SpriteMaterial,
  Vector3,
  type PerspectiveCamera,
  type Object3D,
} from "three";
import type { Edge, Node } from "@/api";
import { grade as gradeSemantic } from "@/lib/semantics";
import { CO_OCCURS, heatColor, mixHex, predicateColor, typeColor } from "./colors";
import { LabelLayer, type LabelHost } from "./labels";
import {
  NO_FILTERS,
  NO_FOCUS,
  diffGraph,
  edgeKey,
  edgeShown,
  nodeShown,
  type Filters,
  type Focus,
  type GraphDiff,
} from "./model";
import type { ScenePalette } from "./palette";

export type ColorMode = "type" | "grade" | "recalls";

export interface Heat {
  nodes: Readonly<Record<string, number>>;
  max: number;
}

export interface SceneEvents {
  onNodeClick(id: string): void;
  onNodeHover(id: string | null): void;
  onLinkClick(key: string): void;
  onLinkHover(key: string | null): void;
  onBackgroundClick(): void;
  /** The person took the camera; automatic framing and orbit stand down. */
  onCameraMoved(): void;
}

interface Glow {
  color: Color;
  start: number;
  span: number;
  halo: Sprite | null;
}

interface Dying {
  start: number;
  span: number;
}

interface RNode {
  id: string;
  label: string;
  type: string;
  grade: string;
  src: Node;
  degree: number;
  radius: number;
  base: Color;
  dimmed: Color;
  dim: boolean;
  hidden: boolean;
  mesh: Mesh;
  mat: MeshLambertMaterial;
  glow: Glow | null;
  dying: Dying | null;
  x?: number;
  y?: number;
  z?: number;
}

interface RLink {
  key: string;
  src: Edge;
  sid: string;
  tid: string;
  source: string | RNode;
  target: string | RNode;
  label: string;
  grade: string;
  base: Color;
  dimmed: Color;
  dim: boolean;
  hidden: boolean;
  hover: boolean;
  arrow: boolean;
  mat: MeshBasicMaterial;
  glow: Glow | null;
  dying: Dying | null;
  __lineObj?: Object3D;
  __arrowObj?: Mesh;
}

interface Pulse {
  link: RLink;
  start: number;
  span: number;
  dots: Mesh[];
}

/** Arrowheads on every edge up to here; past it, only where they say something. */
const ARROWS_EVERYWHERE_UNDER = 1200;
const ARROW_LENGTH = 8;
const LINK_WIDTH = 1;
const FADE_OUT_MS = 1400;
const MAX_PULSES = 40;

const nodeGeometry = new SphereGeometry(1, 18, 14);
const dotGeometry = new SphereGeometry(1, 8, 6);

let haloTexture: CanvasTexture | null = null;
function halo(): CanvasTexture {
  if (haloTexture) return haloTexture;
  const c = document.createElement("canvas");
  c.width = c.height = 128;
  const g = c.getContext("2d");
  if (g) {
    const grad = g.createRadialGradient(64, 64, 0, 64, 64, 64);
    grad.addColorStop(0, "rgba(255,255,255,0.95)");
    grad.addColorStop(0.35, "rgba(255,255,255,0.45)");
    grad.addColorStop(1, "rgba(255,255,255,0)");
    g.fillStyle = grad;
    g.fillRect(0, 0, 128, 128);
  }
  haloTexture = new CanvasTexture(c);
  return haloTexture;
}

const smooth = (k: number) => k * k * (3 - 2 * k);
const sizeOf = (degree: number) => 2.4 + Math.min(7, Math.sqrt(degree) * 1.15);

/** Dependency-free handle on the library's chained instance. */
type Chain = ReturnType<typeof makeGraph>;
function makeGraph(el: HTMLElement) {
  return ForceGraph3D()(el);
}

export class GraphScene {
  private readonly G: Chain;
  private readonly labels: LabelLayer;
  private readonly nodes = new Map<string, RNode>();
  private readonly links = new Map<string, RLink>();
  /** The store maps last synced, to diff the next against. */
  private srcNodes: ReadonlyMap<string, Node> = new Map();
  private srcEdges: ReadonlyMap<string, Edge> = new Map();

  private palette: ScenePalette | null = null;
  private colorMode: ColorMode = "type";
  private heat: Heat = { nodes: {}, max: 0 };
  private filters: Filters = NO_FILTERS;
  private focus: Focus = NO_FOCUS;
  private flowOn = true;
  private labelsOn = true;
  private compact = false;

  private readonly active = new Set<RNode | RLink>();
  private pulses: Pulse[] = [];
  private restyleIn = 0;
  private linkRes = 5;
  private orbiting = 0;
  private orbitAngle = 0;
  private followId: string | null = null;
  private followUntil = 0;
  private hoverLink: RLink | null = null;
  private hidden = false;
  private raf = 0;
  private lastFrame = 0;
  private disposed = false;
  private readonly resizer: ResizeObserver;
  private readonly tmp = new Color();
  private nearCache = { at: 0, ids: [] as string[] };
  private hubCache = { at: 0, ids: [] as string[] };

  constructor(
    private readonly el: HTMLElement,
    labelRoot: HTMLElement,
    private readonly ev: SceneEvents,
  ) {
    const G = makeGraph(el);
    this.G = G;
    this.labels = new LabelLayer(labelRoot);

    G.showNavInfo(false)
      .nodeLabel(() => "")
      .linkLabel(() => "")
      .nodeRelSize(1)
      .nodeVal((o: object) => (o as RNode).radius ** 3)
      .nodeThreeObject((o: object) => (o as RNode).mesh)
      .linkMaterial((o: object) => (o as RLink).mat)
      .linkWidth(LINK_WIDTH)
      .linkResolution(5)
      .linkOpacity(0.34)
      .linkHoverPrecision(2)
      .linkDirectionalArrowLength((o: object) => ((o as RLink).arrow ? ARROW_LENGTH : 0))
      .linkDirectionalArrowRelPos(1)
      .linkDirectionalArrowColor((o: object) => `#${(o as RLink).base.getHexString()}`)
      .onNodeClick((o: object) => ev.onNodeClick((o as RNode).id))
      .onNodeHover((o: object | null) => ev.onNodeHover(o ? (o as RNode).id : null))
      .onLinkClick((o: object) => ev.onLinkClick((o as RLink).key))
      .onLinkHover((o: object | null) => {
        this.setHoverLink(o ? (o as RLink) : null);
        ev.onLinkHover(o ? (o as RLink).key : null);
      })
      .onBackgroundClick(() => ev.onBackgroundClick());

    // The layout is allowed to cool: a warm simulation costs a full pass over
    // every node and link on every frame and buys nothing while nothing is
    // arriving. A delta that brings newcomers reheats it just enough for them
    // to find a place among the settled ones.
    G.d3VelocityDecay(0.32);
    G.d3Force("charge")?.strength(-58).distanceMax(380);
    G.d3Force("link")?.distance(34);

    el.addEventListener("pointerdown", this.onPointerDown);
    el.addEventListener("wheel", this.onWheel, { passive: true });
    document.addEventListener("visibilitychange", this.onVisibility);

    this.resizer = new ResizeObserver(() => this.resize());
    this.resizer.observe(el);
    this.resize();
    this.onVisibility();
  }

  // ---- configuration ----------------------------------------------------

  setPalette(p: ScenePalette): void {
    this.palette = p;
    this.G.backgroundColor(p.background);
    this.restyle();
  }

  setColorMode(mode: ColorMode): void {
    if (this.colorMode === mode) return;
    this.colorMode = mode;
    this.restyle();
  }

  setHeat(heat: Heat): void {
    this.heat = heat;
    if (this.colorMode === "recalls") this.restyle();
  }

  setFilters(f: Filters): void {
    this.filters = f;
    this.restyle();
  }

  setFocus(f: Focus): void {
    this.focus = f;
    this.restyle();
  }

  setFlow(on: boolean): void {
    this.flowOn = on;
    if (!on) this.clearPulses();
  }

  setLabels(on: boolean): void {
    this.labelsOn = on;
    if (!on) this.labels.clear();
  }

  setCompact(on: boolean): void {
    this.compact = on;
  }

  // ---- store to scene ---------------------------------------------------

  /** Apply the difference between the maps last given and these. */
  sync(nodes: ReadonlyMap<string, Node>, edges: ReadonlyMap<string, Edge>): GraphDiff {
    const diff = diffGraph(this.srcNodes, this.srcEdges, nodes, edges);
    this.srcNodes = nodes;
    this.srcEdges = edges;
    if (diff.empty) return diff;
    const now = performance.now();
    const first = this.nodes.size === 0;
    let structural = false;

    for (const n of diff.changedNodes) {
      const r = this.nodes.get(n.id);
      if (!r) continue;
      r.src = n;
      r.label = n.label;
      r.type = n.type;
      r.grade = n.grade ?? "";
    }
    for (const n of diff.addedNodes) {
      const old = this.nodes.get(n.id);
      if (old) {
        // Came back while it was fading out.
        old.dying = null;
        old.src = n;
        old.label = n.label;
        old.type = n.type;
        old.grade = n.grade ?? "";
        this.active.delete(old);
        continue;
      }
      this.nodes.set(n.id, this.makeNode(n));
      structural = true;
    }
    for (const e of diff.changedEdges) {
      const l = this.links.get(edgeKey(e));
      if (!l) continue;
      l.src = e;
      l.grade = e.grade ?? "";
    }
    for (const e of diff.addedEdges) {
      const k = edgeKey(e);
      const old = this.links.get(k);
      if (old) {
        old.dying = null;
        old.src = e;
        this.active.delete(old);
        continue;
      }
      this.links.set(k, this.makeLink(k, e));
      structural = true;
    }
    for (const id of diff.removedNodes) {
      const r = this.nodes.get(id);
      if (r && !r.dying) r.dying = { start: now, span: FADE_OUT_MS };
      if (r) this.active.add(r);
    }
    for (const k of diff.removedEdges) {
      const l = this.links.get(k);
      if (l && !l.dying) l.dying = { start: now, span: FADE_OUT_MS };
      if (l) this.active.add(l);
    }

    if (diff.addedNodes.length) this.place(diff.addedNodes, first);
    this.refreshDegrees();
    if (structural) this.pushData();
    if (diff.addedNodes.length && !first) this.G.d3ReheatSimulation();
    this.restyle();
    return diff;
  }

  private makeNode(n: Node): RNode {
    const mat = new MeshLambertMaterial({ transparent: true, opacity: 1 });
    const mesh = new Mesh(nodeGeometry, mat);
    return {
      id: n.id,
      label: n.label,
      type: n.type,
      grade: n.grade ?? "",
      src: n,
      degree: 0,
      radius: sizeOf(0),
      base: new Color("#94a3b8"),
      dimmed: new Color("#cbd5e1"),
      dim: false,
      hidden: false,
      mesh,
      mat,
      glow: null,
      dying: null,
    };
  }

  private makeLink(key: string, e: Edge): RLink {
    return {
      key,
      src: e,
      sid: e.source,
      tid: e.target,
      source: e.source,
      target: e.target,
      label: e.label,
      grade: e.grade ?? "",
      base: new Color("#94a3b8"),
      dimmed: new Color("#cbd5e1"),
      dim: false,
      hidden: false,
      hover: false,
      arrow: false,
      mat: new MeshBasicMaterial({ transparent: true, opacity: 0.5, depthWrite: false }),
      glow: null,
      dying: null,
    };
  }

  /** Newcomers start beside a neighbour that is already placed, so the layout settles them into the neighbourhood. */
  private place(added: Node[], first: boolean): void {
    if (first) return;
    for (const n of added) {
      const r = this.nodes.get(n.id);
      if (!r || r.x !== undefined) continue;
      let anchor: RNode | undefined;
      for (const l of this.links.values()) {
        const other = l.sid === n.id ? l.tid : l.tid === n.id ? l.sid : null;
        const o = other ? this.nodes.get(other) : undefined;
        if (o && o.x !== undefined) {
          anchor = o;
          break;
        }
      }
      const jitter = () => (Math.random() - 0.5) * (anchor ? 30 : 60);
      r.x = (anchor?.x ?? 0) + jitter();
      r.y = (anchor?.y ?? 0) + jitter();
      r.z = (anchor?.z ?? 0) + jitter();
    }
  }

  private refreshDegrees(): void {
    const deg = new Map<string, number>();
    for (const l of this.links.values()) {
      if (l.dying) continue;
      deg.set(l.sid, (deg.get(l.sid) ?? 0) + 1);
      if (l.tid !== l.sid) deg.set(l.tid, (deg.get(l.tid) ?? 0) + 1);
    }
    for (const n of this.nodes.values()) {
      n.degree = deg.get(n.id) ?? 0;
      n.radius = sizeOf(n.degree);
    }
    this.hubCache.at = 0;
  }

  /** Hand the library new arrays of the same objects; it keeps what it already placed. */
  private pushData(): void {
    // Past a couple of thousand links the cylinders are the frame budget: a
    // triangular prism reads the same at that size and draws a third less.
    const res = this.links.size > 1500 ? 3 : 5;
    if (res !== this.linkRes) {
      this.linkRes = res;
      this.G.linkResolution(res);
    }
    this.G.graphData({ nodes: [...this.nodes.values()], links: [...this.links.values()] });
    this.restyleIn = 3;
  }

  // ---- appearance -------------------------------------------------------

  private nodeBase(n: RNode): string {
    const p = this.palette;
    const dark = p?.dark ?? false;
    if (this.colorMode === "grade") return p?.grades[gradeSemantic(n.grade).key as keyof ScenePalette["grades"]] ?? "#94a3b8";
    if (this.colorMode === "recalls") return heatColor(this.heat.nodes[n.id] ?? 0, this.heat.max, dark);
    return typeColor(n.type, dark);
  }

  private linkBase(l: RLink): string {
    const p = this.palette;
    if (this.colorMode === "grade" && p) return p.grades[gradeSemantic(l.grade).key as keyof ScenePalette["grades"]];
    return predicateColor(l.label);
  }

  /** Recompute every node's and link's resting appearance, and paint it. */
  private restyle(): void {
    const p = this.palette;
    if (!p) return;
    const bg = p.background;
    const now = performance.now();
    const f = this.focus;
    const focused = f.kind !== "none";
    const total = this.links.size;
    let arrowsChanged = false;

    for (const n of this.nodes.values()) {
      n.hidden = !nodeShown(this.filters, n.src);
      n.dim = focused && !f.nodes.has(n.id);
      const hex = this.nodeBase(n);
      n.base.set(hex);
      n.dimmed.set(mixHex(hex, bg, p.dark ? 0.78 : 0.82));
    }
    const nodeView = new Map<string, Node>();
    for (const [id, n] of this.nodes) nodeView.set(id, n.src);
    const everywhere = total <= ARROWS_EVERYWHERE_UNDER;
    for (const l of this.links.values()) {
      l.hidden = !edgeShown(this.filters, l.src, nodeView);
      l.dim = focused && !f.edges.has(l.key);
      const hex = this.linkBase(l);
      l.base.set(hex);
      l.dimmed.set(mixHex(hex, bg, 0.9));
      const arrow = !l.hidden && (everywhere || (focused && f.edges.has(l.key)) || l.glow !== null);
      if (arrow !== l.arrow) arrowsChanged = true;
      l.arrow = arrow;
    }
    // Arrowheads are the one thing only the library can add or remove, so the
    // accessor is replaced (a new function counts as a change) when the set
    // of links that carry one differs from last time.
    if (arrowsChanged) {
      this.G.linkDirectionalArrowLength((o: object) => ((o as RLink).arrow ? ARROW_LENGTH : 0));
      this.G.linkDirectionalArrowColor((o: object) => `#${(o as RLink).base.getHexString()}`);
      this.restyleIn = Math.max(this.restyleIn, 3);
    }

    for (const n of this.nodes.values()) this.paintNode(n, now);
    for (const l of this.links.values()) this.paintLink(l, now);
  }

  private paintNode(n: RNode, now: number): void {
    const p = this.palette;
    if (!p) return;
    let opacity = n.dim ? 0.4 : 1;
    let scale = n.dim ? n.radius * 0.72 : n.radius;
    const c = this.tmp.copy(n.dim ? n.dimmed : n.base);
    let haloOpacity = 0;
    let haloScale = 0;
    if (n.glow) {
      const k = 1 - (now - n.glow.start) / n.glow.span;
      if (k <= 0) {
        this.endGlow(n);
      } else {
        const e = smooth(k);
        c.lerp(n.glow.color, e);
        scale *= 1 + 0.9 * e;
        opacity = opacity + (1 - opacity) * e;
        haloOpacity = 0.55 * e;
        haloScale = n.radius * (4 + 5 * (1 - k));
      }
    }
    if (n.dying) {
      const k = Math.min(1, (now - n.dying.start) / n.dying.span);
      opacity *= 1 - k;
      scale *= 1 - 0.45 * k;
    }
    n.mat.color.copy(c);
    n.mat.opacity = opacity;
    n.mesh.scale.setScalar(scale);
    n.mesh.visible = !n.hidden || !!n.dying;
    if (n.glow?.halo) {
      n.glow.halo.material.opacity = haloOpacity * (n.dying ? 1 - Math.min(1, (now - n.dying.start) / n.dying.span) : 1);
      n.glow.halo.scale.setScalar(Math.max(0.001, haloScale / Math.max(0.001, scale)));
      n.glow.halo.visible = n.mesh.visible;
    }
  }

  private paintLink(l: RLink, now: number): void {
    const p = this.palette;
    if (!p) return;
    const dark = p.dark;
    const quiet = l.label === CO_OCCURS;
    let opacity = l.dim ? 0.07 : this.focus.kind === "none" ? (quiet ? 0.3 : dark ? 0.8 : 0.75) : 1;
    if (l.hover) opacity = 1;
    const c = this.tmp.copy(l.dim ? l.dimmed : l.base);
    if (l.glow) {
      const k = 1 - (now - l.glow.start) / l.glow.span;
      if (k <= 0) {
        this.endGlow(l);
      } else {
        const e = smooth(k);
        c.lerp(l.glow.color, e);
        opacity = opacity + (1 - opacity) * e;
      }
    }
    if (l.dying) opacity *= 1 - Math.min(1, (now - l.dying.start) / l.dying.span);
    l.mat.color.copy(c);
    l.mat.opacity = opacity;
    const visible = !l.hidden || !!l.dying;
    if (l.__lineObj) l.__lineObj.visible = visible;
    const arrow = l.__arrowObj;
    if (arrow) {
      arrow.visible = visible && l.arrow;
      const m = arrow.material as MeshLambertMaterial;
      m.color.copy(c);
      m.opacity = Math.min(1, opacity * 1.6);
    }
  }

  private endGlow(item: RNode | RLink): void {
    const g = item.glow;
    if (!g) return;
    if (g.halo) {
      g.halo.removeFromParent();
      g.halo.material.dispose();
    }
    item.glow = null;
    this.active.delete(item);
    // A link that was lit gave up its arrowhead when the glow ended.
    if (!("radius" in item) && !item.arrow) this.restyleIn = Math.max(this.restyleIn, 2);
    else if (!("radius" in item)) this.restyleIn = Math.max(this.restyleIn, 2);
  }

  private setHoverLink(l: RLink | null): void {
    if (this.hoverLink === l) return;
    if (this.hoverLink) {
      this.hoverLink.hover = false;
      this.paintLink(this.hoverLink, performance.now());
    }
    this.hoverLink = l;
    if (l) {
      l.hover = true;
      this.paintLink(l, performance.now());
    }
  }

  // ---- events on the canvas ---------------------------------------------

  /** Light nodes in a colour that fades over `ms`. */
  lightNodes(ids: Iterable<string>, hex: string, ms: number): number {
    const now = performance.now();
    let lit = 0;
    for (const id of ids) {
      const n = this.nodes.get(id);
      if (!n) continue;
      lit++;
      if (n.glow?.halo) {
        n.glow.halo.removeFromParent();
        n.glow.halo.material.dispose();
      }
      const sprite = new Sprite(new SpriteMaterial({ map: halo(), color: hex, transparent: true, depthWrite: false, opacity: 0 }));
      sprite.raycast = () => undefined;
      n.mesh.add(sprite);
      n.glow = { color: new Color(hex), start: now, span: ms, halo: sprite };
      this.active.add(n);
    }
    return lit;
  }

  /** Light edges, and optionally run a pulse along each in its own direction. */
  lightEdges(keys: Iterable<string>, hex: string, ms: number, pulse: boolean): void {
    const now = performance.now();
    let needArrows = false;
    for (const k of keys) {
      const l = this.links.get(k);
      if (!l) continue;
      l.glow = { color: new Color(hex), start: now, span: ms, halo: null };
      if (!l.arrow) needArrows = true;
      this.active.add(l);
      if (pulse && this.flowOn) this.addPulse(l, hex, Math.min(ms, 1800));
    }
    if (needArrows) this.restyle();
  }

  private addPulse(l: RLink, hex: string, span: number): void {
    if (this.pulses.length >= MAX_PULSES) this.removePulse(this.pulses[0] as Pulse);
    const dots: Mesh[] = [];
    for (let i = 0; i < 3; i++) {
      const m = new Mesh(dotGeometry, new MeshBasicMaterial({ color: hex, transparent: true, opacity: 0.95, depthWrite: false }));
      m.scale.setScalar(1.5);
      m.visible = false;
      this.G.scene().add(m);
      dots.push(m);
    }
    this.pulses.push({ link: l, start: performance.now(), span, dots });
  }

  private removePulse(p: Pulse): void {
    for (const d of p.dots) {
      d.removeFromParent();
      (d.material as MeshBasicMaterial).dispose();
    }
    this.pulses = this.pulses.filter((x) => x !== p);
  }

  private clearPulses(): void {
    for (const p of [...this.pulses]) this.removePulse(p);
  }

  private tickPulses(now: number): void {
    for (const p of [...this.pulses]) {
      const t = (now - p.start) / p.span;
      if (t >= 1.3) {
        this.removePulse(p);
        continue;
      }
      const s = typeof p.link.source === "object" ? p.link.source : undefined;
      const e = typeof p.link.target === "object" ? p.link.target : undefined;
      if (!s || !e || s.x === undefined || e.x === undefined) continue;
      p.dots.forEach((d, i) => {
        const u = t * 1.3 - i * 0.14;
        d.visible = u > 0 && u < 1;
        if (!d.visible) return;
        d.position.set(
          s.x! + (e.x! - s.x!) * u,
          (s.y ?? 0) + ((e.y ?? 0) - (s.y ?? 0)) * u,
          (s.z ?? 0) + ((e.z ?? 0) - (s.z ?? 0)) * u,
        );
      });
    }
  }

  private finishDying(now: number): void {
    let removed = false;
    for (const [id, n] of this.nodes) {
      if (n.dying && now - n.dying.start >= n.dying.span) {
        this.endGlow(n);
        n.mat.dispose();
        this.nodes.delete(id);
        removed = true;
      }
    }
    for (const [k, l] of this.links) {
      const ended = l.dying && now - l.dying.start >= l.dying.span;
      const orphan = !this.nodes.has(l.sid) || !this.nodes.has(l.tid);
      if (ended || orphan) {
        l.mat.dispose();
        this.links.delete(k);
        removed = true;
      }
    }
    if (removed) {
      this.refreshDegrees();
      this.pushData();
      this.restyle();
    }
  }

  // ---- the frame loop ---------------------------------------------------

  private readonly frame = (t: number) => {
    if (this.disposed || this.hidden) return;
    this.raf = requestAnimationFrame(this.frame);
    const now = performance.now();
    const dt = Math.min(64, t - this.lastFrame || 16);
    this.lastFrame = t;

    if (this.restyleIn > 0 && --this.restyleIn === 0) this.restyle();

    let dyingDue = false;
    for (const item of this.active) {
      if ("radius" in item) this.paintNode(item, now);
      else this.paintLink(item, now);
      if (item.dying && now - item.dying.start >= item.dying.span) dyingDue = true;
    }
    if (dyingDue) this.finishDying(now);
    if (this.pulses.length) this.tickPulses(now);

    if (this.orbiting > 0) {
      const c = this.G.cameraPosition();
      const radius = Math.hypot(c.x, c.z) || 400;
      this.orbitAngle += 0.0022 * this.orbiting * (dt / 16);
      this.G.cameraPosition({ x: radius * Math.sin(this.orbitAngle), y: c.y, z: radius * Math.cos(this.orbitAngle) });
    }
    if (this.followId && now > this.followUntil) this.followId = null;
    if (this.followId && Math.floor(now / 380) !== Math.floor((now - dt) / 380)) this.followStep();

    if (this.labelsOn && this.palette) this.labels.update(this.labelHost);
  };

  private readonly labelHost: LabelHost = (() => {
    const me = this;
    return {
      get focus() {
        return me.focus;
      },
      get nodes() {
        return me.nodes;
      },
      get links() {
        return me.links;
      },
      get ambient() {
        return me.labelsOn;
      },
      get compact() {
        return me.compact;
      },
      project: (x: number, y: number, z: number) => me.project(x, y, z),
      size: () => ({ width: me.el.clientWidth, height: me.el.clientHeight }),
      nearby: () => me.nearIds(),
      hubs: () => me.hubIds(),
    };
  })();

  private readonly tmpVec = new Vector3();

  private project(x: number, y: number, z: number): { x: number; y: number } | null {
    const cam = this.G.camera();
    const d = cam.getWorldDirection(this.tmpVec);
    const along = (x - cam.position.x) * d.x + (y - cam.position.y) * d.y + (z - cam.position.z) * d.z;
    if (along <= 0) return null;
    return this.G.graph2ScreenCoords(x, y, z);
  }

  private hubIds(): string[] {
    const now = performance.now();
    if (now - this.hubCache.at < 4000 && this.hubCache.ids.length) return this.hubCache.ids;
    this.hubCache = { at: now, ids: [...this.nodes.values()].filter((n) => !n.hidden && !n.dying).sort((a, b) => b.degree - a.degree).slice(0, 16).map((n) => n.id) };
    return this.hubCache.ids;
  }

  /** Nodes the camera has come close enough to that their sphere is a few pixels across, largest first. */
  private nearIds(): string[] {
    const now = performance.now();
    if (now - this.nearCache.at < 200) return this.nearCache.ids;
    const cam = this.G.camera();
    const d = cam.getWorldDirection(this.tmpVec);
    const w = this.el.clientWidth;
    const h = this.el.clientHeight;
    const focal = h / 2 / Math.tan(((cam as PerspectiveCamera).fov * Math.PI) / 360);
    const found: [number, string][] = [];
    for (const n of this.nodes.values()) {
      if (n.hidden || n.dying || n.x === undefined || n.y === undefined || n.z === undefined) continue;
      const along = (n.x - cam.position.x) * d.x + (n.y - cam.position.y) * d.y + (n.z - cam.position.z) * d.z;
      if (along <= 1) continue;
      const px = (n.radius * focal) / along;
      if (px < 7) continue;
      const s = this.G.graph2ScreenCoords(n.x, n.y, n.z);
      if (s.x < 0 || s.y < 0 || s.x > w || s.y > h) continue;
      found.push([px, n.id]);
    }
    found.sort((a, b) => b[0] - a[0]);
    this.nearCache = { at: now, ids: found.slice(0, 36).map((x) => x[1]) };
    return this.nearCache.ids;
  }

  // ---- camera -----------------------------------------------------------

  private readonly onPointerDown = () => {
    this.followId = null;
    this.setOrbit(0);
    this.ev.onCameraMoved();
  };
  private readonly onWheel = () => this.ev.onCameraMoved();

  setOrbit(speed: number): void {
    if (speed > 0 && this.orbiting === 0) {
      const c = this.G.cameraPosition();
      this.orbitAngle = Math.atan2(c.x, c.z);
    }
    this.orbiting = speed;
  }
  get orbitSpeed(): number {
    return this.orbiting;
  }

  /** Frame the graph, not its outliers: the far tail is left out of the calculation but still drawn. */
  fit(ms: number, only?: ReadonlySet<string>): void {
    const placed = [...this.nodes.values()].filter((n) => typeof n.x === "number" && !n.hidden && !n.dying);
    if (only && only.size) {
      this.G.zoomToFit(ms, 90, (o: object) => only.has((o as RNode).id));
      return;
    }
    if (placed.length < 12) {
      this.G.zoomToFit(ms, 70);
      return;
    }
    const radii = placed.map((n) => Math.hypot(n.x ?? 0, n.y ?? 0, n.z ?? 0)).sort((a, b) => a - b);
    const cut = radii[Math.floor(radii.length * 0.93)] ?? Infinity;
    this.G.zoomToFit(ms, 70, (o: object) => {
      const n = o as RNode;
      return !n.hidden && Math.hypot(n.x ?? 0, n.y ?? 0, n.z ?? 0) <= cut;
    });
  }

  /** Aim the camera at a node from a fixed distance along the current line of sight. */
  flyTo(id: string, ms: number, lowered = false): boolean {
    const n = this.nodes.get(id);
    if (!n || n.x === undefined || n.y === undefined || n.z === undefined) return false;
    const cam = this.G.cameraPosition();
    const dx = cam.x - n.x;
    const dy = cam.y - n.y;
    const dz = cam.z - n.z;
    const d = Math.hypot(dx, dy, dz) || 1;
    const D = lowered ? 300 : 240;
    const up = this.G.camera().up;
    const off = lowered ? 0.11 * D : 0;
    const look = { x: n.x - up.x * off, y: n.y - up.y * off, z: n.z - up.z * off };
    this.G.cameraPosition({ x: look.x + (dx / d) * D, y: look.y + (dy / d) * D, z: look.z + (dz / d) * D }, look, ms);
    return true;
  }

  /** Keep a node in view while the layout is still moving, until it settles or the person takes the camera. */
  follow(id: string): void {
    this.followId = id;
    this.followUntil = performance.now() + 30000;
  }

  private followStep(): void {
    const n = this.followId ? this.nodes.get(this.followId) : undefined;
    if (!n || n.x === undefined || n.y === undefined || n.z === undefined) return;
    const target = this.G.controls() as unknown as { target: { x: number; y: number; z: number } };
    const dx = n.x - target.target.x;
    const dy = n.y - target.target.y;
    const dz = n.z - target.target.z;
    if (Math.hypot(dx, dy, dz) < 0.5) return;
    const c = this.G.cameraPosition();
    this.G.cameraPosition({ x: c.x + dx, y: c.y + dy, z: c.z + dz }, { x: n.x, y: n.y, z: n.z }, 350);
  }

  /** Where a node is on screen, relative to the canvas; null when it has no place yet or is behind the camera. */
  screenOf(id: string): { x: number; y: number } | null {
    const n = this.nodes.get(id);
    if (!n || n.x === undefined || n.y === undefined || n.z === undefined) return null;
    return this.project(n.x, n.y, n.z);
  }

  has(id: string): boolean {
    const n = this.nodes.get(id);
    return !!n && !n.dying;
  }

  /** Whether the layout has given this node a position yet. */
  placed(id: string): boolean {
    return typeof this.nodes.get(id)?.x === "number";
  }

  // ---- lifecycle --------------------------------------------------------

  resize(): void {
    const w = this.el.clientWidth;
    const h = this.el.clientHeight;
    if (w < 2 || h < 2) return;
    this.G.width(w).height(h);
  }

  private readonly onVisibility = () => {
    const hide = document.hidden;
    if (hide === this.hidden && this.raf) return;
    this.hidden = hide;
    if (hide) {
      cancelAnimationFrame(this.raf);
      this.raf = 0;
      this.G.pauseAnimation();
    } else {
      this.G.resumeAnimation();
      this.lastFrame = performance.now();
      this.raf = requestAnimationFrame(this.frame);
    }
  };

  /** The canvas, for a screenshot or a test. */
  get canvas(): HTMLCanvasElement {
    return this.G.renderer().domElement;
  }

  dispose(): void {
    this.disposed = true;
    cancelAnimationFrame(this.raf);
    this.resizer.disconnect();
    this.el.removeEventListener("pointerdown", this.onPointerDown);
    this.el.removeEventListener("wheel", this.onWheel);
    document.removeEventListener("visibilitychange", this.onVisibility);
    this.clearPulses();
    this.labels.clear();
    this.G.pauseAnimation();
    this.G._destructor();
  }
}
