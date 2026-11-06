// The reading room in plain three.js.
//
// Plain three rather than the graph page's force layout, because nothing here
// moves under a simulation: every case, book and plaque has the place
// lib/library.ts gives it, and this file only draws that plan. Four rules
// shape it:
//
//   1. One InstancedMesh draws every book, so a brain of thousands of memories
//      is one draw call and a tap on a book is one raycast.
//   2. A book is read where it stands. Opening one lays it open on the lectern
//      with a thread back to its shelf; a recall lights the books it read in
//      place and threads them to the reading table. Nothing leaves a shelf.
//   3. The room is rebuilt, not patched, when the snapshot changes or the
//      lights flip. A few thousand boxes build in tens of milliseconds, and a
//      rebuild cannot drift from the data the way a patch can. Everything a
//      build makes is disposed of by the next one, and by dispose().
//   4. Nothing is drawn that did not change. The room is still, so a frame is
//      rendered only while the camera moves or something in the room changed,
//      and the shadows are cast once per change rather than once per frame.

import {
  BoxGeometry,
  CanvasTexture,
  Color,
  CylinderGeometry,
  DirectionalLight,
  Fog,
  Group,
  HemisphereLight,
  InstancedMesh,
  LineCurve3,
  Mesh,
  MeshBasicMaterial,
  MeshStandardMaterial,
  NeutralToneMapping,
  Object3D,
  PCFSoftShadowMap,
  PerspectiveCamera,
  PlaneGeometry,
  PMREMGenerator,
  PointLight,
  QuadraticBezierCurve3,
  Raycaster,
  RepeatWrapping,
  Scene,
  Shape,
  ShapeGeometry,
  SphereGeometry,
  SRGBColorSpace,
  TubeGeometry,
  Vector2,
  Vector3,
  WebGLRenderer,
  type BufferGeometry,
  type Light,
  type Material,
  type MeshStandardMaterialParameters,
  type Texture,
} from "three";
import { OrbitControls } from "three/addons/controls/OrbitControls.js";
import { RoomEnvironment } from "three/addons/environments/RoomEnvironment.js";
import { RoundedBoxGeometry } from "three/addons/geometries/RoundedBoxGeometry.js";
import { CSS2DObject, CSS2DRenderer } from "three/addons/renderers/CSS2DRenderer.js";
import type { LibraryBookPage, LibraryCard } from "@/api/library";
import { aimOffset, coveredInsets, type Insets } from "@/lib/graph/insets";
import { BOOK_D, LEVEL_H, hash, shortId, whereOf, type BookSpot, type CasePlan, type Layout, type PlaquePlan, type Vec3 } from "@/lib/library";
import { bookText, clip, metaLine, paginate, provenance, wrapText } from "@/lib/libraryPages";
import { BOOK_SERIF, CANDLE_LIGHT, LAMP_LIGHT, PAGE_INK, WOOD, roomTone, type RoomTone } from "@/lib/libraryPalette";
import type { LibraryText } from "@/lib/libraryStrings";

export interface SceneEvents {
  /** A book was tapped on a shelf, or a see-also on the open book's back page. */
  onBook(id: string): void;
  /** An index term on the back page, or a relation on the table. */
  onCard(id: string): void;
  /** A reference volume was tapped: it cannot be opened, only named. */
  onDoc(spot: BookSpot): void;
  /** The catalogue's plaque: the search box is the catalogue. */
  onCatalogue(): void;
  /** The book under a mouse, or null; never for touch, which has no hover. */
  onHover(spot: BookSpot | null, clientX: number, clientY: number): void;
}

export interface BuildOptions {
  layout: Layout;
  night: boolean;
  text: LibraryText;
  /** A phone: smaller shadows, and labels sized for it. */
  compact: boolean;
}

/** An aim and a size to fit in what the panels leave of the view. */
interface Frame {
  target: Vec3;
  /** From the target towards the camera; need not be unit length. */
  dir: Vec3;
  width: number;
  height: number;
  /** Never closer than this. */
  min: number;
}

interface OpenBook {
  id: string;
  spot: BookSpot;
  book: LibraryBookPage;
  pages: string[][];
  group: Group;
  left: Mesh | null;
  leftTex: Texture | null;
  right: Mesh;
  hits: Hit[];
  page: number;
}

interface Hit {
  x: number;
  y: number;
  w: number;
  h: number;
  go: () => void;
}

/** Every resource one build or one effect made, so the next can dispose of them. */
class Bin {
  private readonly items = new Set<{ dispose(): void }>();
  add<T extends { dispose(): void }>(x: T): T {
    this.items.add(x);
    return x;
  }
  dispose(): void {
    for (const x of this.items) x.dispose();
    this.items.clear();
  }
}

const PAGE_W = 1200;
const PAGE_H = 1640;
const BOOK_PW = 1.15;
const BOOK_PH = 1.57;
const TAP_SLOP = 6;
const READ_MS = 9000;

// Label classes. The labels stand in the room, so they wear its wood and brass
// rather than the app's tokens, and they shrink on a phone.
const PLAQUE =
  "pointer-events-auto cursor-pointer select-none whitespace-nowrap rounded-lg border-2 border-[#c99a3b] bg-[linear-gradient(#3e2717,#2c1b10)] px-[11px] pb-[6px] pt-[5px] text-center text-[#f3dfb0] shadow-[0_8px_18px_-8px_rgba(30,16,6,0.7)] transition-[border-color,box-shadow] hover:border-[#ffe08a] data-[sel=true]:border-[#ffe08a] data-[sel=true]:shadow-[0_0_0_3px_rgba(255,224,138,0.35),0_8px_18px_-8px_rgba(30,16,6,0.7)] max-md:border-[1.5px] max-md:px-[7px] max-md:pb-[4px] max-md:pt-[3px]";
const PLAQUE_STILL = "pointer-events-none cursor-default";
const PLAQUE_TITLE = "block text-[15px] leading-tight max-md:text-[11px]";
const PLAQUE_SUB = "mt-px block font-sans text-[10.5px] text-[#d6b97c] max-md:hidden";
const SHELF = "whitespace-nowrap rounded-[5px] border border-[rgba(201,154,59,0.6)] bg-[rgba(40,24,12,0.8)] px-[7px] py-[2px] text-[11px] font-semibold text-[#f6e8c8] max-md:text-[10px]";
const SHELF_N = "ml-[5px] font-medium text-[#e9c46a]";
const TAG = "pointer-events-auto cursor-pointer select-none whitespace-nowrap rounded-md border border-[#b8862f] bg-[#fbf3e1] px-2 py-[3px] text-center text-[11.5px] text-[#2a1d12] shadow-[0_4px_10px_-6px_rgba(30,16,6,0.6)] hover:bg-[#fff8e8] max-md:text-[10px]";
const TAG_REL = "block font-mono text-[9.5px] text-[#1f6b52]";
const TAG_CENTER = "whitespace-nowrap rounded-md border border-[#c99a3b] bg-[#1f5e4b] px-3 py-[5px] text-center text-sm font-semibold text-[#fbf3e1] shadow-[0_4px_10px_-6px_rgba(30,16,6,0.6)]";
const TAG_CENTER_TYPE = "block font-mono text-[9.5px] font-normal text-[#bfe3d3]";
const READ = "max-w-[360px] overflow-hidden text-ellipsis whitespace-nowrap rounded-full border-[1.5px] border-[#d9a33a] bg-[#ffe7a8] px-2.5 py-[3px] text-[11.5px] font-bold text-[#5a3a12] shadow-[0_0_18px_2px_rgba(242,184,75,0.55)] max-md:max-w-[220px]";

const vec = (p: Vec3) => new Vector3(p.x, p.y, p.z);
const Y = new Vector3(0, 1, 0);

export class LibraryScene {
  private readonly renderer: WebGLRenderer;
  private readonly labels: CSS2DRenderer;
  private readonly camera: PerspectiveCamera;
  private readonly controls: OrbitControls;
  private readonly scene = new Scene();
  private readonly env: Texture;
  private readonly resizer: ResizeObserver;
  private readonly ray = new Raycaster();
  private readonly ndc = new Vector2();

  private readonly worldBin = new Bin();
  private readonly fxBin = new Bin();
  private world: Group | null = null;
  private fx: Group | null = null;
  private books: InstancedMesh | null = null;
  private lecternLabel: CSS2DObject | null = null;
  private plaqueEls = new Map<number, HTMLElement>();
  private shelfLabels: Array<{ o: CSS2DObject; wing: number }> = [];
  private geoCache = new Map<string, BufferGeometry>();
  private M: Record<keyof typeof WOOD | "wall" | "glass" | "glow" | "found" | "thread" | "ink" | "lampShade" | "waxLit", Material> | null = null;

  private layout: Layout | null = null;
  private text: LibraryText | null = null;
  private tone: RoomTone = roomTone(false);
  private compact = false;
  private focusWing: number | null = null;
  private open: OpenBook | null = null;
  private readTimer = 0;
  private placed = false;

  private tween: ((now: number) => boolean) | null = null;
  private raf = 0;
  private shadowsDirty = true;
  private hidden = document.hidden;
  private disposed = false;
  private down: { x: number; y: number; many: boolean } | null = null;
  private pointers = new Set<number>();
  private hoverQueued: PointerEvent | null = null;

  constructor(
    private readonly host: HTMLElement,
    private readonly labelHost: HTMLElement,
    /** The stage the panels float over, for what they cover. */
    private readonly stage: HTMLElement,
    private readonly ev: SceneEvents,
  ) {
    const renderer = new WebGLRenderer({ antialias: true });
    renderer.setPixelRatio(Math.min(2, window.devicePixelRatio || 1));
    renderer.shadowMap.enabled = true;
    renderer.shadowMap.type = PCFSoftShadowMap;
    // The room is still: shadows are cast again only when it changes.
    renderer.shadowMap.autoUpdate = false;
    renderer.toneMapping = NeutralToneMapping;
    renderer.domElement.style.display = "block";
    renderer.domElement.style.touchAction = "none";
    host.appendChild(renderer.domElement);
    this.renderer = renderer;

    const labels = new CSS2DRenderer();
    labels.domElement.style.position = "absolute";
    labels.domElement.style.inset = "0";
    labels.domElement.style.pointerEvents = "none";
    labelHost.appendChild(labels.domElement);
    this.labels = labels;

    // near at 1, not lower: depth precision is spent close to the camera, and
    // the hall is seen from tens of metres away.
    this.camera = new PerspectiveCamera(32, 1, 1, 900);
    const controls = new OrbitControls(this.camera, renderer.domElement);
    controls.enableDamping = true;
    controls.dampingFactor = 0.08;
    controls.minPolarAngle = 0.2;
    controls.maxPolarAngle = 1.32;
    // Pan along the floor, not the screen: a reader walks, they do not float.
    controls.screenSpacePanning = false;
    controls.addEventListener("change", this.request);
    controls.addEventListener("start", this.onControlStart);
    this.controls = controls;

    const pmrem = new PMREMGenerator(renderer);
    const room = new RoomEnvironment();
    this.env = pmrem.fromScene(room, 0.04).texture;
    room.dispose();
    pmrem.dispose();

    const el = renderer.domElement;
    el.addEventListener("pointerdown", this.onPointerDown);
    el.addEventListener("pointermove", this.onPointerMove);
    el.addEventListener("pointerup", this.onPointerUp);
    el.addEventListener("pointercancel", this.onPointerCancel);
    el.addEventListener("pointerleave", this.onPointerLeave);
    document.addEventListener("visibilitychange", this.onVisibility);

    this.resizer = new ResizeObserver(() => this.resize());
    this.resizer.observe(host);
    this.resize();
  }

  // ---- building the room ----------------------------------------------

  /** Builds the room from scratch, disposing of everything the last build made. */
  build(o: BuildOptions): void {
    this.clearFx();
    this.teardownWorld();
    this.layout = o.layout;
    this.text = o.text;
    this.tone = roomTone(o.night);
    this.compact = o.compact;
    const T = this.tone;
    const P = o.layout.plan;
    const W = P.hall;

    this.materials();
    const world = new Group();
    this.world = world;
    this.scene.add(world);

    this.scene.background = new Color(T.background);
    this.scene.environment = this.env;
    this.scene.environmentIntensity = T.environment;
    this.scene.fog = T.fog ? new Fog(T.background, 120, 220) : null;
    this.fitFog();
    this.renderer.toneMappingExposure = T.exposure;

    const floor = this.worldBin.add(new MeshStandardMaterial({ map: this.parquet(), roughness: 0.55 }));
    floor.map?.repeat.set((W.x1 - W.x0) / 5, (W.z1 - W.z0) / 5);
    this.add(world, this.worldBin.add(new BoxGeometry(W.x1 - W.x0, 0.4, W.z1 - W.z0)), floor, (W.x0 + W.x1) / 2, -0.2, (W.z0 + W.z1) / 2, true);
    this.blk(world, this.m("carpet"), 6, 0.04, W.z1 - W.z0 - 3, 0, 0, (W.z0 + W.z1) / 2 + 1.5, 0.01);
    this.walls();

    for (const c of P.cases) this.bookcase(c);
    this.shelfLabels = P.shelfLabels.map((s) => {
      const el = document.createElement("div");
      el.className = SHELF;
      el.append(document.createTextNode(s.name));
      const n = document.createElement("span");
      n.className = SHELF_N;
      n.textContent = String(s.n);
      el.append(n);
      const lo = this.label(world, el, s.x, s.y, s.z);
      lo.visible = false;
      return { o: lo, wing: s.wing };
    });
    this.plaqueEls.clear();
    for (const p of P.plaques) this.plaque(p);
    this.furniture();
    this.bookMesh();
    this.lights();

    if (this.focusWing !== null && !P.wings.has(this.focusWing)) this.focusWing = null;
    if (this.focusWing !== null) this.selectWing(this.focusWing, true);
    if (!this.placed) this.home(false);
    this.shadowsDirty = true;
    this.request();
  }

  private materials(): void {
    const T = this.tone;
    const std = (color: string, extra: MeshStandardMaterialParameters = {}) =>
      this.worldBin.add(new MeshStandardMaterial({ color, roughness: 0.7, metalness: 0, ...extra }));
    const basic = (color: string, opacity?: number) =>
      this.worldBin.add(new MeshBasicMaterial(opacity === undefined ? { color } : { color, transparent: true, opacity, depthWrite: false }));
    this.M = {
      walnut: std(WOOD.walnut, { roughness: 0.55 }),
      walnutDark: std(WOOD.walnutDark, { roughness: 0.6 }),
      oak: std(WOOD.oak, { roughness: 0.6 }),
      wainscot: std(WOOD.wainscot, { roughness: 0.6 }),
      brass: std(WOOD.brass, { roughness: 0.3, metalness: 0.7 }),
      carpet: std(WOOD.carpet, { roughness: 0.95 }),
      parchment: std(WOOD.parchment, { roughness: 0.85 }),
      paperEdge: std(WOOD.paperEdge),
      lamp: std(WOOD.lamp),
      lampShade: std(WOOD.lamp, { roughness: 0.25, metalness: 0.2, emissive: WOOD.lamp, emissiveIntensity: T.lampGlow }),
      wax: std(WOOD.wax, { roughness: 0.5 }),
      waxLit: std(WOOD.wax, { roughness: 0.5, emissive: WOOD.waxGlow, emissiveIntensity: T.waxGlow }),
      waxGlow: basic(WOOD.waxGlow),
      flame: basic(WOOD.flame),
      wall: std(T.wall, { roughness: 0.9 }),
      glass: std(T.glass, { roughness: 0.1, emissive: T.glassGlow, emissiveIntensity: T.glassGlowIntensity }),
      glow: basic(WOOD.glow, T.haloOpacity),
      found: basic(WOOD.found, 0.34),
      thread: basic(WOOD.thread, T.threadOpacity),
      ink: basic(WOOD.ink, 0.7),
      line: basic(WOOD.line),
    };
  }

  private m(k: keyof NonNullable<LibraryScene["M"]>): Material {
    if (!this.M) throw new Error("library scene: no materials before build");
    return this.M[k];
  }

  /** A rounded box, shared by every block of the same size in one build. */
  private rbox(w: number, h: number, d: number, r = 0.04): BufferGeometry {
    const k = `${w}:${h}:${d}:${r}`;
    let g = this.geoCache.get(k);
    if (!g) {
      g = this.worldBin.add(r > 0 ? new RoundedBoxGeometry(w, h, d, 2, Math.min(r, w / 2, h / 2, d / 2) - 0.001) : new BoxGeometry(w, h, d));
      this.geoCache.set(k, g);
    }
    return g;
  }

  private shared(k: string, make: () => BufferGeometry): BufferGeometry {
    let g = this.geoCache.get(k);
    if (!g) {
      g = this.worldBin.add(make());
      this.geoCache.set(k, g);
    }
    return g;
  }

  private add(parent: Object3D, geo: BufferGeometry, mat: Material, x: number, y: number, z: number, noCast = false): Mesh {
    const o = new Mesh(geo, mat);
    o.position.set(x, y, z);
    o.castShadow = !noCast;
    o.receiveShadow = true;
    parent.add(o);
    return o;
  }

  /** A block standing on y, rather than centred on it. */
  private blk(parent: Object3D, mat: Material, w: number, h: number, d: number, x: number, y: number, z: number, r?: number): Mesh {
    return this.add(parent, this.rbox(w, h, d, r), mat, x, y + h / 2, z);
  }

  private label(parent: Object3D, el: HTMLElement, x: number, y: number, z: number, onClick?: () => void): CSS2DObject {
    if (onClick) {
      // The controls listen on the canvas under the labels; a press on a
      // label is the label's, not the start of an orbit.
      el.addEventListener("pointerdown", (e) => e.stopPropagation());
      el.addEventListener("click", (e) => {
        e.stopPropagation();
        onClick();
      });
    }
    const o = new CSS2DObject(el);
    o.position.set(x, y, z);
    parent.add(o);
    return o;
  }

  private parquet(): Texture {
    const c = document.createElement("canvas");
    c.width = c.height = 512;
    const g = c.getContext("2d");
    const [A, B] = this.tone.parquet;
    if (g) {
      for (let i = 0; i < 16; i++) {
        for (let j = 0; j < 4; j++) {
          g.fillStyle = (i + j) % 2 ? A : B;
          g.fillRect(j * 128 + (i % 2) * 64 - 64, i * 32, 128, 32);
          g.fillRect(j * 128 + (i % 2) * 64 + 64, i * 32, 128, 32);
          g.strokeStyle = "rgba(60,35,18,0.25)";
          g.lineWidth = 2;
          g.strokeRect(j * 128 + (i % 2) * 64, i * 32, 128, 32);
        }
      }
    }
    const t = this.worldBin.add(new CanvasTexture(c));
    t.colorSpace = SRGBColorSpace;
    t.wrapS = t.wrapT = RepeatWrapping;
    t.anisotropy = Math.min(8, this.renderer.capabilities.getMaxAnisotropy());
    return t;
  }

  private walls(): void {
    const world = this.world!;
    const W = this.layout!.plan.hall;
    const WALL_H = 11;
    // cap is the cornice's height. It stands a little above the wall it sits
    // on (whose top is exactly WALL_H), and the two walls get different ones,
    // so no two faces ever share a plane: coplanar tops z-fight and flicker.
    const wall = (len: number, n: number, cap: number) => {
      // n windows between n+1 piers, laid end to end: no two boxes overlap
      // and no two faces share a plane.
      const g = new Group();
      const seg = len / (n + 0.42);
      const pierW = seg * 0.42;
      const winW = seg - pierW;
      const gh = WALL_H - 3.8 - winW / 2;
      const glass = this.m("glass");
      const pane = this.shared(`pane:${winW}:${gh}`, () => new BoxGeometry(winW - 0.02, gh, 0.12));
      const arch = this.shared(`arch:${winW}`, () => {
        const s = new Shape();
        s.moveTo(-winW / 2 + 0.01, 0);
        s.absarc(0, 0, winW / 2 - 0.01, Math.PI, 0, true);
        s.lineTo(-winW / 2 + 0.01, 0);
        return new ShapeGeometry(s, 24);
      });
      for (let i = 0; i < n; i++) {
        const x0 = -len / 2 + i * seg;
        const wc = x0 + pierW + winW / 2;
        this.blk(g, this.m("wall"), pierW, WALL_H, 0.8, x0 + pierW / 2, 0, 0, 0.02);
        this.blk(g, this.m("wall"), winW, 2.2, 0.8, wc, 0, 0, 0.02);
        this.blk(g, this.m("wall"), winW, 1.6, 0.8, wc, WALL_H - 1.6, 0, 0.02);
        // The glass sits back from the wall's faces, the arch above the pane.
        this.add(g, pane, glass, wc, 2.2 + gh / 2, 0, true);
        this.add(g, arch, glass, wc, 2.2 + gh + 0.001, 0.03, true);
        for (let k = 1; k < 3; k++) this.blk(g, this.m("walnutDark"), 0.08, gh - 0.02, 0.2, wc - winW / 2 + (winW * k) / 3, 2.21, 0, 0.01);
        for (let k = 1; k < 4; k++) this.blk(g, this.m("walnutDark"), winW - 0.04, 0.08, 0.17, wc, 2.2 + (gh * k) / 4, 0, 0.01);
      }
      this.blk(g, this.m("wall"), pierW, WALL_H, 0.8, len / 2 - pierW / 2, 0, 0, 0.02);
      // The wainscot stops short of the ends and the cornice runs past them,
      // so neither shares an end face with the wall; the wainscot stands proud
      // of the room side only, so its back stays inside the wall.
      this.blk(g, this.m("wainscot"), len - 0.04, 1.2, 0.84, 0, 0, 0.08, 0.02);
      this.blk(g, this.m("walnutDark"), len + 0.06, 0.4 + cap, 1.0, 0, WALL_H - 0.4, 0.05, 0.02);
      return g;
    };
    const back = wall(W.x1 - W.x0, Math.max(4, Math.round((W.x1 - W.x0) / 7.5)), 0.06);
    back.position.set(0, 0, W.z0 + 0.4);
    world.add(back);
    // The left wall starts where the back wall ends, so the corner is one
    // wall's, not two walls overlapping (their faces would share planes).
    const left = wall(W.z1 - W.z0 - 1.0, Math.max(3, Math.round((W.z1 - W.z0) / 7.5)), 0.1);
    left.rotation.y = Math.PI / 2;
    left.position.set(W.x0 + 0.4, 0, (W.z0 + W.z1) / 2 + 0.5);
    world.add(left);
  }

  private bookcase(c: CasePlan): void {
    const g = new Group();
    g.position.set(c.x, 0, c.z);
    g.rotation.y = c.rot;
    this.world!.add(g);
    const H = c.levels * LEVEL_H + 0.5;
    const D = 0.62;
    const w = c.width;
    const walnut = this.m("walnut");
    const dark = this.m("walnutDark");
    this.blk(g, walnut, 0.14, H, D, -w / 2 - 0.07, 0, 0);
    this.blk(g, walnut, 0.14, H, D, w / 2 + 0.07, 0, 0);
    this.blk(g, dark, w + 0.3, 0.12, D + 0.08, 0, H - 0.04, 0, 0.03);
    this.blk(g, dark, w + 0.4, 0.2, D + 0.12, 0, H + 0.08, 0, 0.04);
    // The back panel stands a hair proud of the sides and the shelves stop
    // short of it, so nothing shares the case's back plane.
    this.blk(g, dark, w, H, 0.06, 0, 0, -D / 2 + 0.01, 0.01);
    for (let l = 0; l <= c.levels; l++) this.blk(g, walnut, w - 0.01, 0.07, D - 0.08, 0, 0.25 + l * LEVEL_H - 0.07, 0.04, 0.01);
  }

  private plaque(p: PlaquePlan): void {
    const g = new Group();
    g.position.set(p.x, 0, p.z);
    g.rotation.y = p.rot;
    this.world!.add(g);
    this.add(g, this.shared("post", () => new CylinderGeometry(0.06, 0.09, 1.3, 12)), this.m("brass"), 0, 0.65, 0);
    this.add(g, this.shared("foot", () => new CylinderGeometry(0.35, 0.4, 0.08, 20)), this.m("brass"), 0, 0.04, 0);
    this.blk(g, this.m("walnutDark"), 1.5, 0.75, 0.08, 0, 1.25, 0, 0.03);
    const el = document.createElement("div");
    el.className = p.wing === null ? `${PLAQUE} ${PLAQUE_STILL}` : PLAQUE;
    el.style.fontFamily = BOOK_SERIF;
    const b = document.createElement("b");
    b.className = PLAQUE_TITLE;
    b.textContent = p.title;
    const s = document.createElement("span");
    s.className = PLAQUE_SUB;
    s.textContent = p.sub;
    el.append(b, s);
    const wing = p.wing;
    if (wing !== null) {
      el.setAttribute("role", "button");
      el.title = p.title;
      this.plaqueEls.set(wing, el);
    }
    this.label(g, el, 0, 2.55, 0, wing === null ? undefined : () => this.selectWing(wing));
  }

  private furniture(): void {
    const world = this.world!;
    const P = this.layout!.plan;
    const T = this.tone;
    const text = this.text!;

    // The lectern.
    const L0 = P.lectern;
    this.add(world, this.shared("lectern", () => new CylinderGeometry(0.25, 0.45, 1.3, 16)), this.m("walnut"), L0.x, 0.65, L0.z);
    const top = this.blk(world, this.m("walnutDark"), 1.6, 0.1, 1.1, L0.x, 1.3, L0.z, 0.03);
    top.rotation.x = 0.35;
    const lect = document.createElement("div");
    lect.className = SHELF;
    lect.textContent = text.lectern;
    this.lecternLabel = this.label(world, lect, L0.x, 2.3, L0.z);

    // The reading table: recalled books send their threads here.
    const D = P.desk;
    this.blk(world, this.m("walnut"), 5.0, 0.14, 1.8, D.x, 1.0, D.z, 0.05);
    for (const dx of [-2.2, 2.2]) this.blk(world, this.m("walnutDark"), 0.3, 1.0, 1.4, D.x + dx, 0, D.z, 0.04);
    for (const dx of [-1.4, 1.4]) {
      this.add(world, this.shared("lampstem", () => new CylinderGeometry(0.06, 0.06, 0.5, 10)), this.m("brass"), D.x + dx, 1.37, D.z - 0.3);
      this.add(world, this.shared("shade", () => new CylinderGeometry(0.12, 0.32, 0.22, 20, 1, true)), this.m("lampShade"), D.x + dx, 1.68, D.z - 0.3);
      if (T.candles) this.light(world, new PointLight(LAMP_LIGHT, 6, 7, 1.6), D.x + dx, 1.55, D.z - 0.3);
    }

    // The card catalogue: its plaque opens the search box.
    const C = P.catalog;
    const cg = new Group();
    cg.position.set(C.x, C.y, C.z);
    world.add(cg);
    this.blk(cg, this.m("oak"), 4.2, 2.6, 1.3, 0, 0, 0, 0.05);
    const pull = this.shared("pull", () => new BoxGeometry(0.14, 0.04, 0.04));
    for (let r = 0; r < 6; r++) {
      for (let c = 0; c < 8; c++) {
        this.blk(cg, this.m("walnut"), 0.44, 0.32, 0.06, -1.82 + c * 0.52, 0.2 + r * 0.4, 0.66, 0.02);
        this.add(cg, pull, this.m("brass"), -1.82 + c * 0.52, 0.36 + r * 0.4, 0.7);
      }
    }
    const cat = document.createElement("div");
    cat.className = PLAQUE;
    cat.style.fontFamily = BOOK_SERIF;
    cat.setAttribute("role", "button");
    const cb = document.createElement("b");
    cb.className = PLAQUE_TITLE;
    cb.textContent = text.catalog;
    const cs = document.createElement("span");
    cs.className = PLAQUE_SUB;
    cs.textContent = `${this.layout!.lib.cards.toLocaleString()} ${text.cards}`;
    cat.append(cb, cs);
    this.label(cg, cat, 0, 3.4, 0, () => this.ev.onCatalogue());

    // Floating candles over the nave, placed by hash so they stay put across builds.
    const wax = T.candles ? this.m("waxLit") : this.m("wax");
    const stick = this.shared("candle", () => new CylinderGeometry(0.07, 0.07, 0.45, 10));
    const flame = this.shared("flame", () => new SphereGeometry(0.06, 8, 6));
    for (let i = 0; i < 40; i++) {
      const x = (hash(`cx${i}`) - 0.5) * 16;
      const z = P.hall.z0 + 4 + hash(`cz${i}`) * (P.hall.z1 - P.hall.z0 - 6);
      const y = 8.4 + hash(`cy${i}`) * 1.6;
      this.add(world, stick, wax, x, y, z, true);
      this.add(world, flame, this.m("flame"), x, y + 0.3, z, true);
      if (T.candles && i % 7 === 0) this.light(world, new PointLight(CANDLE_LIGHT, 9, 12, 1.8), x, y + 0.3, z);
    }
  }

  private light<T extends Light>(parent: Object3D, l: T, x: number, y: number, z: number): T {
    l.position.set(x, y, z);
    parent.add(l);
    this.worldBin.add(l);
    return l;
  }

  /** Every book in one instanced mesh, and a ring of gilt on the reference volumes. */
  private bookMesh(): void {
    const world = this.world!;
    const spots = this.layout!.books;
    const geo = this.worldBin.add(new RoundedBoxGeometry(1, 1, 1, 1, 0.08));
    const mat = this.worldBin.add(new MeshStandardMaterial({ roughness: 0.62, metalness: 0.02 }));
    const mesh = this.worldBin.add(new InstancedMesh(geo, mat, Math.max(1, spots.length)));
    const gilt = spots.filter((b) => b.doc);
    const gm = this.worldBin.add(new InstancedMesh(this.shared("unit", () => new BoxGeometry(1, 1, 1)), this.m("brass"), Math.max(1, gilt.length * 2)));
    const o = new Object3D();
    const col = new Color();
    let gi = 0;
    for (const b of spots) {
      o.position.set(b.p.x, b.p.y, b.p.z);
      o.rotation.set(0, b.rot, 0);
      o.scale.set(b.w, b.h, BOOK_D);
      o.updateMatrix();
      mesh.setMatrixAt(b.index, o.matrix);
      mesh.setColorAt(b.index, col.set(b.color));
      if (b.doc) {
        for (const f of [0.28, -0.3]) {
          const q = new Vector3(0, b.h * f, BOOK_D / 2 + 0.004).applyAxisAngle(Y, b.rot).add(vec(b.p));
          o.position.copy(q);
          o.scale.set(b.w * 1.01, 0.025, 0.01);
          o.updateMatrix();
          gm.setMatrixAt(gi++, o.matrix);
        }
      }
    }
    mesh.count = spots.length;
    gm.count = gi;
    mesh.castShadow = mesh.receiveShadow = true;
    mesh.computeBoundingSphere();
    world.add(mesh, gm);
    this.books = mesh;
  }

  private lights(): void {
    const world = this.world!;
    const T = this.tone;
    const W = this.layout!.plan.hall;
    world.add(this.worldBin.add(new HemisphereLight(T.sky, T.ground, T.hemisphere)));
    const sun = this.worldBin.add(new DirectionalLight(T.sun, T.sunIntensity));
    const span = Math.max(W.x1 - W.x0, W.z1 - W.z0) * 0.75;
    sun.position.set(W.x0 - 4, 40, W.z0 - 14);
    sun.castShadow = true;
    sun.shadow.camera.left = -span;
    sun.shadow.camera.right = span;
    sun.shadow.camera.top = span;
    sun.shadow.camera.bottom = -span;
    sun.shadow.camera.far = 220;
    const size = this.compact ? 2048 : 4096;
    sun.shadow.mapSize.set(size, size);
    sun.shadow.bias = -0.0004;
    sun.shadow.normalBias = 0.03;
    world.add(sun);
    const fill = this.worldBin.add(new DirectionalLight(T.fill, T.fillIntensity));
    fill.position.set(30, 20, 40);
    world.add(fill);
  }

  private teardownWorld(): void {
    this.cancelRead();
    if (this.world) {
      this.scene.remove(this.world);
      removeLabels(this.world);
    }
    this.worldBin.dispose();
    this.geoCache.clear();
    this.world = null;
    this.books = null;
    this.lecternLabel = null;
    this.shelfLabels = [];
    this.plaqueEls.clear();
    this.M = null;
  }

  // ---- the camera -------------------------------------------------------

  private fly(pos: Vector3, target: Vector3, ms = 900): void {
    const p0 = this.camera.position.clone();
    const t0 = this.controls.target.clone();
    const start = performance.now();
    this.tween = (now) => {
      const k = Math.min(1, (now - start) / ms);
      const e = k < 0.5 ? 2 * k * k : 1 - Math.pow(-2 * k + 2, 2) / 2;
      this.camera.position.lerpVectors(p0, pos, e);
      this.controls.target.lerpVectors(t0, target, e);
      return k < 1;
    };
    this.request();
  }

  /**
   * Flies to a frame: near enough that its width and height fill the part of
   * the view the panels leave uncovered, and aimed at the middle of that part.
   */
  private flyTo(f: Frame, ms?: number, instant = false): void {
    // Whatever was under the mouse will not be once the camera moves.
    this.ev.onHover(null, 0, 0);
    const ins = coveredInsets(this.stage);
    const w = Math.max(1, this.host.clientWidth);
    const h = Math.max(1, this.host.clientHeight);
    const keepW = Math.max(0.25, (w - ins.left - ins.right) / w);
    const keepH = Math.max(0.25, (h - ins.top - ins.bottom) / h);
    const vf = (this.camera.fov * Math.PI) / 180;
    const tv = Math.tan(vf / 2) * keepH;
    const th = Math.tan(vf / 2) * (w / h) * keepW;
    const d = Math.max(f.min, f.width / 2 / th, f.height / 2 / tv) * 1.08;
    const dir = vec(f.dir).normalize();
    const target = vec(f.target);
    const pos = target.clone().addScaledVector(dir, d);
    const off = this.aim(dir.clone().negate(), d, ins, h);
    target.add(off);
    pos.add(off);
    if (instant) {
      this.tween = null;
      this.camera.position.copy(pos);
      this.controls.target.copy(target);
      this.request();
    } else this.fly(pos, target, ms);
  }

  private aim(forward: Vector3, distance: number, ins: Insets, heightPx: number): Vector3 {
    const o = aimOffset(forward, this.camera.up, this.camera.fov, heightPx, distance, ins);
    return new Vector3(o.x, o.y, o.z);
  }

  /** The whole hall, from the front corner. */
  /** Where the whole room is seen from, for the screen as it is now shaped. */
  private homeView(): { pos: Vector3; target: Vector3; d: number } | null {
    const P = this.layout?.plan;
    if (!P) return null;
    const W = P.hall;
    const cx = (W.x0 + W.x1) / 2;
    const cz = (W.z0 + W.z1) / 2;
    const r = Math.max(W.x1 - W.x0, W.z1 - W.z0);
    // Far enough that the hall's width fits the horizontal field of view,
    // which on a portrait phone is the narrow one.
    const aspect = Math.max(0.1, this.host.clientWidth / Math.max(1, this.host.clientHeight));
    const vf = (this.camera.fov * Math.PI) / 180;
    const hf = 2 * Math.atan(Math.tan(vf / 2) * aspect);
    const d = Math.max(r * 1.25, ((W.x1 - W.x0) * 0.62) / Math.tan(hf / 2));
    return { pos: new Vector3(cx + d * 0.55, d * 0.62, cz + d * 0.62), target: new Vector3(cx, 0, cz - 1), d };
  }

  /**
   * The night fog is there to let the room sink into the dark as the camera
   * pulls away past its home view. It starts at twice the home distance: a
   * portrait phone sees the room from much further off than a desktop does,
   * and fog fixed at the desktop's distance left the phone a black screen.
   */
  private fitFog(): void {
    const fog = this.scene.fog;
    const v = this.homeView();
    if (!(fog instanceof Fog) || !v) return;
    fog.near = Math.max(120, v.d * 2.1);
    fog.far = fog.near + 100;
  }

  home(animate = true): void {
    const v = this.homeView();
    if (!v) return;
    const { pos, target, d } = v;
    this.controls.maxDistance = d * 2.2;
    this.controls.minDistance = 3;
    if (!animate || !this.placed) {
      this.placed = true;
      this.tween = null;
      this.camera.position.copy(pos);
      this.controls.target.copy(target);
      this.request();
    } else this.fly(pos, target);
  }

  /** Flies to a wing and shows the names of its shelves. */
  selectWing(wing: number, quiet = false): void {
    const P = this.layout?.plan;
    if (!P) return;
    this.focusWing = wing;
    for (const s of this.shelfLabels) s.o.visible = s.wing === wing;
    for (const [w, el] of this.plaqueEls) el.dataset.sel = String(w === wing);
    this.request();
    const spot = P.wings.get(wing);
    if (!spot || quiet) return;
    this.flyTo({ target: { x: spot.x, y: 1.5, z: spot.z }, dir: { x: 0.35, y: 0.55, z: 0.9 }, width: spot.w + 2, height: 5, min: 9 });
  }

  // ---- effects: glowing books, threads, the relation table ---------------

  /** Takes every effect out of the room: halos, threads, the open book and the table. */
  clearFx(): void {
    this.cancelRead();
    if (this.fx) {
      this.fx.parent?.remove(this.fx);
      removeLabels(this.fx);
    }
    this.fxBin.dispose();
    this.fx = null;
    if (this.open) {
      // The left page is replaced on every turn, so it is not in the bin.
      disposePage(this.open.left, this.open.leftTex);
      this.open = null;
      if (this.lecternLabel) this.lecternLabel.visible = true;
    }
    this.shadowsDirty = true;
    this.request();
  }

  private freshFx(): Group {
    this.clearFx();
    const g = new Group();
    this.world?.add(g);
    this.fx = g;
    return g;
  }

  private halo(g: Group, b: BookSpot, mat: Material): Vector3 {
    const front = new Vector3(0, 0, 0.2).applyAxisAngle(Y, b.rot);
    const p = vec(b.p).add(front);
    const m = this.add(g, this.rbox(1, 1, 1, 0.05), mat, p.x, p.y, p.z, true);
    m.rotation.y = b.rot;
    m.scale.set(b.w * 2.4, b.h * 1.3, 0.5);
    return p;
  }

  private thread(g: Group, a: Vector3, b: Vector3, mat: Material, lift: number): void {
    const mid = a.clone().lerp(b, 0.5);
    mid.y += lift;
    this.add(g, this.fxBin.add(new TubeGeometry(new QuadraticBezierCurve3(a, mid, b), 48, 0.03, 5, false)), mat, 0, 0, 0, true);
  }

  /** The books a search found, lit where they stand. */
  showFound(ids: readonly string[]): void {
    if (!this.layout) return;
    const g = this.freshFx();
    for (const id of ids) {
      const b = this.layout.byId.get(id);
      if (b) this.halo(g, b, this.m("found"));
    }
  }

  /**
   * A call that read books: they light where they stand and a thread runs from
   * each to the reading table, until the next thing happens or a few seconds pass.
   */
  readInPlace(spots: readonly BookSpot[], line: string): void {
    if (!this.layout || !spots.length) return;
    const g = this.freshFx();
    const D = vec(this.layout.plan.desk);
    spots.forEach((b, i) => this.thread(g, this.halo(g, b, this.m("glow")), D, this.m("thread"), 5 + (i % 4) * 0.6));
    const el = document.createElement("div");
    el.className = READ;
    el.textContent = line;
    this.label(g, el, D.x, D.y + 2.2, D.z);
    this.readTimer = window.setTimeout(() => {
      if (this.fx === g) this.clearFx();
    }, READ_MS);
  }

  private cancelRead(): void {
    if (this.readTimer) clearTimeout(this.readTimer);
    this.readTimer = 0;
  }

  /** An index card: every book that mentions it threaded to the catalogue, and its relations on the table. */
  showCard(card: LibraryCard, fly = true): void {
    const lay = this.layout;
    if (!lay) return;
    const g = this.freshFx();
    const P = lay.plan;
    const C = vec(P.catalog).add(new Vector3(0, 3.0, 0.7));
    (card.books ?? []).forEach((id, i) => {
      const b = lay.byId.get(id);
      if (b) this.thread(g, C, this.halo(g, b, this.m("found")), this.m("ink"), 4 + (i % 5) * 0.5);
    });
    // The table: the card in the middle, what it relates to around it.
    const T = P.table;
    const rels = card.relations ?? [];
    const R = Math.min(rels.length, 12);
    this.add(g, this.shared("tabletop", () => new CylinderGeometry(6.2, 6.2, 0.16, 64)), this.m("walnut"), T.x, 1.0, T.z);
    this.add(g, this.shared("tablefoot", () => new CylinderGeometry(0.6, 1.0, 1.0, 24)), this.m("walnutDark"), T.x, 0.5, T.z);
    this.add(g, this.shared("tablecloth", () => new CylinderGeometry(5.9, 5.9, 0.02, 64)), this.m("parchment"), T.x, 1.09, T.z, true);
    const center = document.createElement("div");
    center.className = TAG_CENTER;
    center.append(document.createTextNode(card.label || shortId(card.id)));
    const ct = document.createElement("small");
    ct.className = TAG_CENTER_TYPE;
    ct.textContent = card.type;
    center.append(ct);
    this.label(g, center, T.x, 1.5, T.z);
    for (let i = 0; i < R; i++) {
      const rel = rels[i];
      if (!rel) continue;
      const a = (i / R) * Math.PI * 2 + 0.3;
      const q = new Vector3(T.x + Math.cos(a) * 4.9, 1.1, T.z + Math.sin(a) * 4.1);
      this.blk(g, this.m("parchment"), 1.15, 0.05, 0.75, q.x, 1.1, q.z, 0.02);
      const spoke = new LineCurve3(new Vector3(T.x, 1.16, T.z), q.clone().setY(1.16));
      this.add(g, this.fxBin.add(new TubeGeometry(spoke, 1, 0.025, 4, false)), this.m("line"), 0, 0, 0, true);
      const el = document.createElement("div");
      el.className = TAG;
      el.setAttribute("role", "link");
      const small = document.createElement("small");
      small.className = TAG_REL;
      small.textContent = rel.out ? rel.rel : `← ${rel.rel}`;
      el.append(small, document.createTextNode(rel.label || shortId(rel.id)));
      this.label(g, el, q.x, 1.45, q.z, () => (lay.byId.has(rel.id) ? this.ev.onBook(rel.id) : this.ev.onCard(rel.id)));
    }
    const head = document.createElement("div");
    head.className = SHELF;
    head.textContent = this.text?.relTable ?? "";
    this.label(g, head, T.x, 2.2, T.z - 5.4);
    // From above the front of the hall: the table in the foreground, the
    // catalogue the threads start from, and the shelves they run back to.
    const left = T.x - 6.4;
    const right = P.catalog.x + 1.8;
    if (fly) this.flyTo({ target: { x: (left + right) / 2, y: 0, z: T.z - 6 }, dir: { x: 3, y: 30, z: 33 }, width: right - left + 2, height: 20, min: 40 }, 1000);
  }

  // ---- a book: open on the lectern, read page by page ------------------

  /** Lays a book open on the lectern at a page; returns how many pages it has. */
  openBook(book: LibraryBookPage, page = 0, fly = true): number {
    const lay = this.layout;
    const text = this.text;
    const spot = lay?.byId.get(book.id);
    if (!lay || !text || !spot) return 0;
    const g = this.freshFx();
    this.halo(g, spot, this.m("glow"));
    const pages = this.paginate(book, text);
    const Lp = lay.plan.lectern;
    const group = new Group();
    group.position.set(Lp.x, Lp.y + 0.12, Lp.z);
    group.rotation.x = 0.35;
    g.add(group);
    const cover = this.fxBin.add(new MeshStandardMaterial({ color: spot.color, roughness: 0.55 }));
    this.blk(group, cover, BOOK_PW * 2 + 0.12, 0.05, BOOK_PH + 0.1, 0, -0.085, 0, 0.02);
    this.blk(group, this.m("paperEdge"), BOOK_PW - 0.02, 0.07, BOOK_PH - 0.02, -BOOK_PW / 2, -0.045, 0, 0.01);
    this.blk(group, this.m("paperEdge"), BOOK_PW - 0.02, 0.07, BOOK_PH - 0.02, BOOK_PW / 2, -0.045, 0, 0.01);
    const back = this.backPage(book, text);
    const right = this.bentPage(back.tex, 1);
    group.add(right);
    // The thread from the shelf to the lectern says where it came from. It
    // lands on the far edge of the open book, which tilts up towards the
    // shelves, so it never crosses the page being read.
    const tilt = group.rotation.x;
    const edge = new Vector3(Lp.x, Lp.y + 0.12 + Math.sin(tilt) * (BOOK_PH / 2 + 0.05), Lp.z - Math.cos(tilt) * (BOOK_PH / 2 + 0.05));
    this.thread(g, vec(spot.p), edge, this.m("thread"), 6);
    if (this.lecternLabel) this.lecternLabel.visible = false;
    this.open = { id: book.id, spot, book, pages, group, left: null, leftTex: null, right, hits: back.hits, page: 0 };
    this.setPage(page);
    // The open book tilts towards the reader, so its near edge looks wider
    // than the book is: the frame is fitted with room to spare.
    if (fly) this.flyTo({ target: { x: Lp.x, y: Lp.y + 0.1, z: Lp.z - 0.1 }, dir: { x: 0.5, y: 3.2, z: 3.2 }, width: BOOK_PW * 2 + 0.9, height: 2.5, min: 3.2 }, 1100);
    return pages.length;
  }

  /** Turns the open book to a page. */
  setPage(n: number): void {
    const st = this.open;
    const text = this.text;
    if (!st || !text) return;
    const page = Math.max(0, Math.min(st.pages.length - 1, n));
    // Opening a book draws its first page, and the page's own state then asks
    // for that page again: drawing a full page canvas twice is wasted work.
    if (st.left && st.page === page) return;
    if (st.left) st.group.remove(st.left);
    disposePage(st.left, st.leftTex);
    const tex = this.textPage(st, page, text);
    st.left = this.bentPage(tex, -1, false);
    st.leftTex = tex;
    st.group.add(st.left);
    st.page = page;
    this.request();
  }

  private paginate(book: LibraryBookPage, text: LibraryText): string[][] {
    const c = document.createElement("canvas").getContext("2d");
    if (!c) return [[book.label]];
    c.font = `36px ${BOOK_SERIF}`;
    return paginate(wrapText(bookText(book, text.noRecords), 990, (s) => c.measureText(s).width));
  }

  private canvasPage(draw: (g: CanvasRenderingContext2D, hits: Hit[]) => void): { tex: Texture; hits: Hit[] } {
    const c = document.createElement("canvas");
    c.width = PAGE_W;
    c.height = PAGE_H;
    const g = c.getContext("2d");
    const hits: Hit[] = [];
    if (g) {
      const grad = g.createRadialGradient(PAGE_W / 2, PAGE_H / 2, 200, PAGE_W / 2, PAGE_H / 2, 1100);
      grad.addColorStop(0, PAGE_INK.paper);
      grad.addColorStop(1, PAGE_INK.paperEdge);
      g.fillStyle = grad;
      g.fillRect(0, 0, PAGE_W, PAGE_H);
      draw(g, hits);
    }
    const tex = new CanvasTexture(c);
    tex.colorSpace = SRGBColorSpace;
    tex.anisotropy = Math.min(16, this.renderer.capabilities.getMaxAnisotropy());
    return { tex, hits };
  }

  /** The left page: the title and meta on the first, then the text. */
  private textPage(st: OpenBook, n: number, text: LibraryText): Texture {
    const where = whereOf(st.spot, text);
    return this.canvasPage((g) => {
      const x = 110;
      let y = 130;
      g.fillStyle = PAGE_INK.brass;
      g.font = `600 30px ${BOOK_SERIF}`;
      g.fillText(`${where} · ${text.shelf}`.replace(/^ · /, ""), x, y);
      if (n === 0) {
        g.fillStyle = PAGE_INK.ink;
        g.font = `700 50px ${BOOK_SERIF}`;
        y = 210;
        for (const l of wrapText(shortId(st.book.id), PAGE_W - 220, (s) => g.measureText(s).width).slice(0, 2)) {
          g.fillText(l, x, y);
          y += 64;
        }
        g.fillStyle = PAGE_INK.mute;
        g.font = `30px ${BOOK_SERIF}`;
        g.fillText(metaLine(st.book.record, text), x, y);
        y += 30;
        g.strokeStyle = PAGE_INK.brass;
        g.lineWidth = 2;
        g.beginPath();
        g.moveTo(x, y + 14);
        g.lineTo(PAGE_W - x, y + 14);
        g.stroke();
        y += 76;
      } else y = 210;
      g.fillStyle = PAGE_INK.ink;
      g.font = `36px ${BOOK_SERIF}`;
      for (const l of st.pages[n] ?? []) {
        g.fillText(l, x, y);
        y += 56;
      }
      g.fillStyle = PAGE_INK.mute;
      g.font = `italic 30px ${BOOK_SERIF}`;
      g.textAlign = "center";
      g.fillText(`— ${n + 1} / ${st.pages.length} —`, PAGE_W / 2, PAGE_H - 80);
    }).tex;
  }

  /** The right page: the book's index, what to read next and where it came from, each entry a link. */
  private backPage(book: LibraryBookPage, text: LibraryText): { tex: Texture; hits: Hit[] } {
    const lay = this.layout!;
    const out = this.canvasPage((g, hits) => {
      const x = 100;
      let y = 130;
      const maxW = PAGE_W - 190;
      const rule = (at: number) => {
        g.strokeStyle = PAGE_INK.brass;
        g.lineWidth = 2;
        g.beginPath();
        g.moveTo(x, at);
        g.lineTo(PAGE_W - x, at);
        g.stroke();
      };
      g.fillStyle = PAGE_INK.brass;
      g.font = `600 30px ${BOOK_SERIF}`;
      g.fillText(text.index, x, y);
      y = 200;
      const index = (book.index ?? []).slice(0, 24);
      index.forEach((t, i) => {
        const cx = x + (i % 2) * (maxW / 2);
        const cy = y + Math.floor(i / 2) * 56;
        g.fillStyle = t.project ? PAGE_INK.green : PAGE_INK.ink;
        g.font = `${t.project ? "700 " : ""}34px ${BOOK_SERIF}`;
        g.fillText(clip(t.label, 22), cx + 30, cy);
        g.fillStyle = t.project ? PAGE_INK.green : PAGE_INK.brass;
        g.beginPath();
        g.arc(cx + 10, cy - 11, t.project ? 8 : 5, 0, Math.PI * 2);
        g.fill();
        hits.push({ x: cx, y: cy - 40, w: maxW / 2, h: 52, go: () => this.ev.onCard(t.id) });
      });
      y += Math.ceil(index.length / 2) * 56 + 40;
      rule(y - 30);
      g.fillStyle = PAGE_INK.brass;
      g.font = `600 30px ${BOOK_SERIF}`;
      g.fillText(text.seeAlso, x, y + 24);
      y += 92;
      for (const s of book.see_also ?? []) {
        const t = clip(shortId(s.id), 44);
        g.fillStyle = PAGE_INK.green;
        g.font = `34px ${BOOK_SERIF}`;
        g.fillText(t, x, y);
        g.fillRect(x, y + 8, g.measureText(t).width, 2.5);
        g.fillStyle = PAGE_INK.mute;
        g.font = `26px ${BOOK_SERIF}`;
        g.fillText(`${whereOf(lay.byId.get(s.id), text)} · ${text.shared(s.shared)}`.replace(/^ · /, ""), x, y + 44);
        hits.push({ x, y: y - 40, w: maxW, h: 96, go: () => this.ev.onBook(s.id) });
        y += 102;
      }
      const prov = provenance(book.record, text);
      if (prov.length) {
        y += 10;
        rule(y - 30);
        g.fillStyle = PAGE_INK.brass;
        g.font = `600 30px ${BOOK_SERIF}`;
        g.fillText(text.provenance, x, y + 24);
        y += 80;
        g.fillStyle = PAGE_INK.ink;
        g.font = `30px ${BOOK_SERIF}`;
        for (const [k, v] of prov) {
          if (y > PAGE_H - 60) break;
          g.fillText(clip(`${k}: ${v}`, 52), x, y);
          y += 46;
        }
      }
    });
    this.fxBin.add(out.tex);
    return out;
  }

  /** A page, curved up from the spine the way an open book's pages are. */
  private bentPage(tex: Texture, side: -1 | 1, own = true): Mesh {
    const geo = new PlaneGeometry(BOOK_PW, BOOK_PH, 40, 1);
    const pos = geo.attributes.position;
    if (pos) {
      for (let i = 0; i < pos.count; i++) {
        const u = (pos.getX(i) + BOOK_PW / 2) / BOOK_PW;
        const t = side < 0 ? 1 - u : u;
        pos.setZ(i, 0.11 * Math.sin((Math.min(1, t * 1.6) * Math.PI) / 2) - 0.035 * t * t);
      }
    }
    geo.computeVertexNormals();
    // Lit by day, white paper comes out brighter than the page was drawn and
    // the ink washes out; a tint takes the light back down to the page's own.
    const mat = new MeshStandardMaterial({ map: tex, roughness: 0.92, color: this.tone.candles ? "#ffffff" : "#cfc8bb" });
    // The right page lives as long as the open book; the left one is
    // replaced on every turn and disposed of by setPage.
    if (own) {
      this.fxBin.add(geo);
      this.fxBin.add(mat);
    }
    const m = new Mesh(geo, mat);
    m.rotation.x = -Math.PI / 2;
    m.position.set(side * (BOOK_PW / 2 + 0.008), 0.03, 0);
    m.receiveShadow = true;
    return m;
  }

  /** Puts the open book back: the room as it was, seen from the front. */
  closeBook(): void {
    if (!this.open) return;
    this.clearFx();
    this.home(true);
  }

  // ---- picking ----------------------------------------------------------

  private pick(clientX: number, clientY: number): void {
    const r = this.renderer.domElement.getBoundingClientRect();
    this.ndc.set(((clientX - r.left) / r.width) * 2 - 1, -((clientY - r.top) / r.height) * 2 + 1);
    this.ray.setFromCamera(this.ndc, this.camera);
  }

  private bookAt(clientX: number, clientY: number): BookSpot | null {
    if (!this.books || !this.layout) return null;
    this.pick(clientX, clientY);
    const h = this.ray.intersectObject(this.books, false)[0];
    return h && h.instanceId !== undefined ? (this.layout.books[h.instanceId] ?? null) : null;
  }

  private readonly onPointerDown = (e: PointerEvent) => {
    this.pointers.add(e.pointerId);
    // A press either drags the room or opens something; either way the tip is stale.
    this.ev.onHover(null, e.clientX, e.clientY);
    const many = this.pointers.size > 1;
    if (many && this.down) this.down.many = true;
    else this.down = { x: e.clientX, y: e.clientY, many };
    this.request();
  };

  private readonly onPointerMove = (e: PointerEvent) => {
    if (e.pointerType === "touch" || this.pointers.size > 0) return;
    // At most one raycast a frame, however fast the mouse moves.
    if (!this.hoverQueued) requestAnimationFrame(this.hover);
    this.hoverQueued = e;
  };

  private readonly hover = () => {
    const e = this.hoverQueued;
    this.hoverQueued = null;
    if (!e || this.disposed) return;
    const b = this.open ? null : this.bookAt(e.clientX, e.clientY);
    this.renderer.domElement.style.cursor = b ? "pointer" : "";
    this.ev.onHover(b, e.clientX, e.clientY);
  };

  private readonly onPointerUp = (e: PointerEvent) => {
    this.pointers.delete(e.pointerId);
    const d = this.down;
    if (this.pointers.size === 0) this.down = null;
    if (!d || d.many || Math.hypot(e.clientX - d.x, e.clientY - d.y) > TAP_SLOP) return;
    this.tap(e.clientX, e.clientY);
  };

  private readonly onPointerCancel = (e: PointerEvent) => {
    this.pointers.delete(e.pointerId);
    if (this.pointers.size === 0) this.down = null;
  };

  private readonly onPointerLeave = (e: PointerEvent) => {
    if (e.pointerType !== "touch") this.ev.onHover(null, e.clientX, e.clientY);
  };

  private tap(x: number, y: number): void {
    const st = this.open;
    if (st) {
      this.pick(x, y);
      const hp = this.ray.intersectObject(st.right, false)[0];
      if (hp?.uv) {
        const px = hp.uv.x * PAGE_W;
        const py = (1 - hp.uv.y) * PAGE_H;
        st.hits.find((z) => px >= z.x && px <= z.x + z.w && py >= z.y && py <= z.y + z.h)?.go();
        return;
      }
      // A tap on the open book is the book's, not the shelf's behind it.
      if (st.left && this.ray.intersectObject(st.left, false).length) return;
    }
    const b = this.bookAt(x, y);
    if (!b) return;
    if (b.doc) this.ev.onDoc(b);
    else this.ev.onBook(b.id);
  }

  // ---- frames -----------------------------------------------------------

  private readonly onControlStart = () => {
    // Someone took the camera: a flight in progress stops where it is.
    this.tween = null;
  };

  /** Asks for a frame; frames keep coming only while something moves. */
  readonly request = () => {
    if (!this.raf && !this.hidden && !this.disposed) this.raf = requestAnimationFrame(this.frame);
  };

  private readonly frame = (now: number) => {
    this.raf = 0;
    if (this.disposed || this.hidden) return;
    const flying = this.tween ? this.tween(now) : false;
    if (!flying) this.tween = null;
    // update() reports whether the camera moved, which damping keeps true
    // for a while after the hand lets go.
    const moved = this.controls.update();
    this.keepInHall();
    if (this.shadowsDirty) {
      this.renderer.shadowMap.needsUpdate = true;
      this.shadowsDirty = false;
    }
    this.renderer.render(this.scene, this.camera);
    this.labels.render(this.scene, this.camera);
    if (flying || moved || this.tween) this.raf = requestAnimationFrame(this.frame);
  };

  /** Panning cannot walk the reader out of the room and lose it. */
  private keepInHall(): void {
    const W = this.layout?.plan.hall;
    if (!W) return;
    const t = this.controls.target;
    const x = Math.min(W.x1, Math.max(W.x0, t.x));
    const z = Math.min(W.z1 + 6, Math.max(W.z0, t.z));
    const y = Math.min(10, Math.max(-1, t.y));
    if (x !== t.x || y !== t.y || z !== t.z) {
      const dx = x - t.x;
      const dy = y - t.y;
      const dz = z - t.z;
      t.set(x, y, z);
      this.camera.position.x += dx;
      this.camera.position.y += dy;
      this.camera.position.z += dz;
    }
  }

  private resize(): void {
    const w = this.host.clientWidth;
    const h = this.host.clientHeight;
    if (w < 2 || h < 2) return;
    this.renderer.setSize(w, h, false);
    this.renderer.domElement.style.width = `${w}px`;
    this.renderer.domElement.style.height = `${h}px`;
    this.labels.setSize(w, h);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
    this.fitFog();
    this.request();
  }

  private readonly onVisibility = () => {
    this.hidden = document.hidden;
    if (!this.hidden) this.request();
  };

  /** The canvas and the renderer's counts, for a test or a screenshot. */
  get info(): { canvas: HTMLCanvasElement; memory: { geometries: number; textures: number }; programs: number } {
    const i = this.renderer.info;
    return { canvas: this.renderer.domElement, memory: { ...i.memory }, programs: i.programs?.length ?? 0 };
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    cancelAnimationFrame(this.raf);
    this.clearFx();
    this.teardownWorld();
    this.resizer.disconnect();
    const el = this.renderer.domElement;
    el.removeEventListener("pointerdown", this.onPointerDown);
    el.removeEventListener("pointermove", this.onPointerMove);
    el.removeEventListener("pointerup", this.onPointerUp);
    el.removeEventListener("pointercancel", this.onPointerCancel);
    el.removeEventListener("pointerleave", this.onPointerLeave);
    document.removeEventListener("visibilitychange", this.onVisibility);
    this.controls.removeEventListener("change", this.request);
    this.controls.removeEventListener("start", this.onControlStart);
    this.controls.dispose();
    this.env.dispose();
    this.scene.environment = null;
    this.scene.background = null;
    this.renderer.dispose();
    // Let go of the context now rather than when the collector gets to it:
    // moving between pages must not pile up WebGL contexts.
    this.renderer.forceContextLoss();
    el.remove();
    this.labels.domElement.remove();
    this.labelHost.replaceChildren();
  }
}

function disposePage(m: Mesh | null, tex: Texture | null): void {
  m?.geometry.dispose();
  (m?.material as Material | undefined)?.dispose();
  tex?.dispose();
}

/** CSS2DObjects leave their elements behind when an ancestor is removed, so take them out by hand. */
function removeLabels(root: Object3D): void {
  root.traverse((o) => {
    if (o instanceof CSS2DObject) o.element.remove();
  });
}

