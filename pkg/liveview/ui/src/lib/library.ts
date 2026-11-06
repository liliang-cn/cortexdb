// Where everything in the reading room goes: a pure function of the library.
//
// library.go decides what is shelved where; this decides where the shelves
// stand. Every case, book, plaque and shelf label has a place that follows from
// the data alone, so the room can be rebuilt from scratch whenever the snapshot
// changes and come out the same, and so the layout can be tested without a GPU.
// The scene (components/library/scene.ts) only draws what this returns.
//
// Wings are numbered by their place in Library.wings, largest first. The three
// rooms that are not wings take the negative numbers below.

import type { ActivityEvent } from "@/api";
import type { Library, LibraryBook, LibraryDoc, LibraryShelf, LibraryWing } from "@/api/library";
import { ANNEX_EARTH, LEATHERS, REFERENCE_BUCKRAM, STACK_CLOTH } from "./libraryPalette";

export const STACKS = -1;
export const ANNEX = -2;
export const REFERENCE = -3;

/** Height of one shelf level, a book's depth, and the pitch of the stacks' cases. */
export const LEVEL_H = 0.58;
export const BOOK_D = 0.34;
/** Keeps neighbouring stacks' cornices (a case's width plus 0.4) apart. */
const STACK_STEP = 3.95;
const GRAND_W = 3.4;
const SIDE_W = 3.3;
const STACK_W = 3.4;
const DOC_W = 3.8;
const GAP = 0.45;
const NAVE = 3.2;
/** Side wings stand in rows of up to this many cases either side of the nave. */
const ROW = 3;
const PER_GRAND = 54;
const PER_SIDE = 72;
const PER_STACK = 84;
const PER_DOC_CASE = 40;
/** A book is at least this far from a case's side, and this far from its neighbour. */
const CASE_INSET = 0.04;
const BOOK_GAP = 0.012;

export interface Vec3 {
  x: number;
  y: number;
  z: number;
}

const v3 = (x: number, y: number, z: number): Vec3 => ({ x, y, z });

/** A library with every list present: the server sends null for an empty one. */
export interface Shelved {
  version: number;
  source: string;
  wings: Array<Omit<LibraryWing, "shelves"> & { shelves: Array<LibraryShelf & { books: LibraryBook[] }> }>;
  annex: Array<LibraryShelf & { books: LibraryBook[] }>;
  stacks: LibraryBook[];
  cards: number;
  docs: LibraryDoc[];
  docsKnown: boolean;
  records: boolean;
}

export function shelve(lib: Library): Shelved {
  const shelf = (s: LibraryShelf) => ({ ...s, books: s.books ?? [] });
  return {
    version: lib.version,
    source: lib.source ?? "",
    wings: (lib.wings ?? []).map((w) => ({ ...w, shelves: (w.shelves ?? []).map(shelf) })),
    annex: (lib.annex ?? []).map(shelf),
    stacks: lib.stacks ?? [],
    cards: lib.cards ?? 0,
    docs: lib.docs ?? [],
    docsKnown: !!lib.docs_known,
    records: !!lib.records,
  };
}

/** Books and shelves on the floor, for the header. */
export function totals(lib: Shelved): { books: number; shelves: number } {
  let books = lib.stacks.length;
  let shelves = lib.annex.length;
  for (const w of lib.wings) {
    books += w.books;
    shelves += w.shelves.length;
  }
  for (const s of lib.annex) books += s.books.length;
  return { books, shelves };
}

/** FNV-1a of a string, as a fraction in [0, 1): a stable "random" per id. */
export function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return (h >>> 0) / 4294967296;
}

/** An id without its namespace: "memory:cortexdb-release" reads "cortexdb-release". */
export function shortId(id: string): string {
  const i = id.indexOf(":");
  return i >= 0 ? id.slice(i + 1) : id;
}

/** A book's thickness: how many things it mentions, capped so a hub memory is not a brick. */
export function bookWidth(terms: number): number {
  return 0.07 + Math.min(1, Math.max(0, terms || 0) / 12) * 0.13;
}

/** A reference volume's thickness: its chunks. */
export function docWidth(chunks: number): number {
  return 0.16 + Math.min(1, Math.max(0, chunks || 0) / 20) * 0.16;
}

/** Something that stands on a shelf: a memory, or a document in the reference room. */
export interface Volume {
  id: string;
  label: string;
  terms: number;
  chunks: number;
  /** The project shelf it is filed on; empty in the stacks and the reference room. */
  shelf: string;
}

/** Where a shelf's run of books starts inside a case, for its label. */
export interface ShelfStart {
  id: string;
  name: string;
  /** Index of the shelf's first book in this case. */
  at: number;
  /** How many books the whole shelf holds, across cases. */
  n: number;
}

export interface PackedCase {
  books: Volume[];
  starts: ShelfStart[];
}

const volume = (b: LibraryBook, shelf: string): Volume => ({ id: b.id, label: b.label, terms: b.terms, chunks: 0, shelf });

/**
 * Packs shelves into cases of perCase books, in order. A shelf that does not
 * fit runs on into the next case; its label goes where it starts, and every
 * book keeps its shelf's name wherever it lands.
 */
export function packCases(shelves: ReadonlyArray<LibraryShelf & { books: LibraryBook[] }>, perCase: number): PackedCase[] {
  const cases: PackedCase[] = [];
  let cur: PackedCase = { books: [], starts: [] };
  for (const sh of shelves) {
    let rest = sh.books;
    let first = true;
    while (rest.length) {
      const room = perCase - cur.books.length;
      if (room <= 0) {
        cases.push(cur);
        cur = { books: [], starts: [] };
        continue;
      }
      if (first) cur.starts.push({ id: sh.id, name: sh.project, at: cur.books.length, n: sh.books.length });
      first = false;
      cur.books.push(...rest.slice(0, room).map((b) => volume(b, sh.project)));
      rest = rest.slice(room);
    }
  }
  if (cur.books.length) cases.push(cur);
  return cases;
}

export interface CasePlan {
  x: number;
  z: number;
  /** Turn about the vertical, in radians. */
  rot: number;
  levels: number;
  width: number;
  books: Volume[];
  starts: ShelfStart[];
  palette: readonly string[];
  wing: number;
  docs: boolean;
}

export interface PlaquePlan {
  x: number;
  z: number;
  rot: number;
  title: string;
  sub: string;
  /** The wing a click selects, or null for a plaque that only names. */
  wing: number | null;
}

/** Where a wing stands, for the camera: its middle and how wide it is. */
export interface WingSpot {
  x: number;
  z: number;
  w: number;
}

export interface ShelfLabel {
  x: number;
  y: number;
  z: number;
  name: string;
  n: number;
  wing: number;
}

export interface Hall {
  x0: number;
  x1: number;
  z0: number;
  z1: number;
}

export interface Plan {
  hall: Hall;
  cases: CasePlan[];
  plaques: PlaquePlan[];
  wings: Map<number, WingSpot>;
  shelfLabels: ShelfLabel[];
  /** The reading table, where recalls send their threads. */
  desk: Vec3;
  lectern: Vec3;
  catalog: Vec3;
  /** The round table a card lays its relations out on. */
  table: Vec3;
}

export interface PlanText {
  wingSub: (shelves: number, books: number) => string;
  stacks: string;
  stacksSub: string;
  annex: string;
  reference: string;
  docs: string;
}

const caseHeight = (levels: number) => levels * LEVEL_H + 0.5;

function chunk<T>(xs: readonly T[], n: number): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < xs.length; i += n) out.push(xs.slice(i, i + n));
  return out;
}

/**
 * Lays the room out. The largest wing stands at the back in tall cases, the
 * general stacks either side of it along the back wall, the other wings (and
 * the annex as one more) in rows either side of the nave, and the documents in
 * the reference room along the left wall. The reading table, the lectern, the
 * catalogue and the relation table stand in the front of the hall.
 */
export function planLibrary(lib: Shelved, text: PlanText): Plan {
  const cases: CasePlan[] = [];
  const plaques: PlaquePlan[] = [];
  const wings = new Map<number, WingSpot>();

  const main = lib.wings[0];
  const grand = main ? packCases(main.shelves, PER_GRAND) : [];
  const grandW = grand.length ? grand.length * (GRAND_W + GAP) - GAP : 0;

  const stackCases = chunk(lib.stacks, PER_STACK);
  const half = Math.ceil(stackCases.length / 2);
  const stL = stackCases.slice(0, half);
  const stR = stackCases.slice(half);

  // The other wings, and the annex as one more.
  interface Side {
    name: string;
    sub: string;
    shelves: Array<LibraryShelf & { books: LibraryBook[] }>;
    palette: readonly string[];
    wing: number;
    cases: PackedCase[];
    width: number;
  }
  const side: Side[] = lib.wings.slice(1).map((w, i) => ({
    name: w.name,
    sub: text.wingSub(w.shelves.length, w.books),
    shelves: w.shelves,
    palette: LEATHERS[(i + 1) % LEATHERS.length] ?? ANNEX_EARTH,
    wing: i + 1,
    cases: [],
    width: 0,
  }));
  if (lib.annex.length) {
    const n = lib.annex.reduce((k, s) => k + s.books.length, 0);
    side.push({ name: text.annex, sub: text.wingSub(lib.annex.length, n), shelves: lib.annex, palette: ANNEX_EARTH, wing: ANNEX, cases: [], width: 0 });
  }
  for (const w of side) {
    w.cases = packCases(w.shelves, PER_SIDE);
    w.width = w.cases.length * (SIDE_W + GAP) - GAP;
  }

  // Each side of the nave is rows of up to ROW cases, nearest the nave first.
  // Small wings share a row: a wing of one case does not need a row to itself,
  // and a hall laid out one wing per row is mostly floor.
  const fill: [Side[][], Side[][]] = [[], []];
  for (const w of side) {
    const rows = fill[0].length <= fill[1].length ? fill[0] : fill[1];
    const last = rows[rows.length - 1];
    const used = last ? last.reduce((k, x) => k + x.cases.length, 0) : ROW;
    if (!last || used + w.cases.length > ROW) rows.push([w]);
    else last.push(w);
  }
  const rows = Math.max(fill[0].length, fill[1].length, 1);
  // A wing of more than ROW cases has a row to itself and runs on past it, so
  // the hall is as wide as its widest row, not as ROW cases.
  const widest = Math.max(ROW, ...fill.flat().map((row) => row.reduce((k, x) => k + x.cases.length, 0)));
  const docCases = chunk(lib.docs, PER_DOC_CASE);
  const ref = docCases.length ? 4.5 : 0;
  const halfW = Math.max(14, grandW / 2 + Math.max(stL.length, stR.length) * STACK_STEP + 2.5, NAVE + widest * (SIDE_W + GAP) + 4 + ref);
  const front = 12.5;
  // The reference room runs along the left wall from the back; a room of many
  // documents deepens the hall rather than running out past its front.
  const back = Math.min(-(8 + rows * 6.4), front - 3.6 - docCases.length * 4.2);
  const hall: Hall = { x0: -halfW, x1: halfW, z0: back - 2.4, z1: front };

  grand.forEach((c, i) => {
    cases.push({ x: -grandW / 2 + GRAND_W / 2 + i * (GRAND_W + GAP), z: back, rot: 0, levels: 9, width: GRAND_W, books: c.books, starts: c.starts, palette: LEATHERS[0] ?? ANNEX_EARTH, wing: 0, docs: false });
  });
  if (main) {
    plaques.push({ x: 0, z: back + 3.4, rot: 0, title: main.name, sub: text.wingSub(main.shelves.length, main.books), wing: 0 });
    wings.set(0, { x: 0, z: back + 1, w: Math.max(grandW, 6) });
  }

  const stackCase = (books: LibraryBook[], x: number): CasePlan => ({
    x,
    z: back - 0.2,
    rot: 0,
    levels: 8,
    width: STACK_W,
    books: books.map((b) => volume(b, "")),
    starts: [],
    palette: STACK_CLOTH,
    wing: STACKS,
    docs: false,
  });
  stL.forEach((b, i) => cases.push(stackCase(b, -grandW / 2 - 2.3 - i * STACK_STEP)));
  stR.forEach((b, i) => cases.push(stackCase(b, grandW / 2 + 2.3 + i * STACK_STEP)));
  if (lib.stacks.length) {
    const x = stR.length ? grandW / 2 + 2.3 + ((stR.length - 1) * STACK_STEP) / 2 : -grandW / 2 - 2.3;
    plaques.push({ x, z: back + 3.2, rot: 0, title: text.stacks, sub: `${lib.stacks.length} · ${text.stacksSub}`, wing: STACKS });
    const xs = [...stL.map((_, i) => -grandW / 2 - 2.3 - i * STACK_STEP), ...stR.map((_, i) => grandW / 2 + 2.3 + i * STACK_STEP)];
    // The camera frames the side the plaque stands on: the stacks either
    // side of the grand cases are too far apart to frame as one.
    const near = stR.length ? xs.slice(stL.length) : xs;
    const mid = (Math.min(...near) + Math.max(...near)) / 2;
    wings.set(STACKS, { x: mid, z: back, w: Math.max(6, near.length * STACK_STEP) });
  }

  ([-1, 1] as const).forEach((sign, si) => {
    (fill[si] ?? []).forEach((row, r) => {
      const z = back + 7.4 + r * 6.4;
      let slot = 0;
      for (const w of row) {
        const x0 = NAVE + 1.8 + SIDE_W / 2 + slot * (SIDE_W + GAP);
        w.cases.forEach((c, i) => {
          cases.push({ x: sign * (x0 + i * (SIDE_W + GAP)), z, rot: 0, levels: 5, width: SIDE_W, books: c.books, starts: c.starts, palette: w.palette, wing: w.wing, docs: false });
        });
        const mid = sign * (x0 + ((w.cases.length - 1) * (SIDE_W + GAP)) / 2);
        plaques.push({ x: mid, z: z + 2.3, rot: 0, title: w.name, sub: w.sub, wing: w.wing });
        wings.set(w.wing, { x: mid, z, w: w.width });
        slot += w.cases.length;
      }
    });
  });

  // The reference room along the left wall, its spines facing the nave.
  docCases.forEach((docs, k) => {
    cases.push({
      x: hall.x0 + 1.2,
      z: hall.z0 + 5 + k * 4.2,
      rot: Math.PI / 2,
      levels: 6,
      width: DOC_W,
      books: docs.map((d) => ({ id: d.id, label: d.title || d.id, terms: 0, chunks: d.chunks, shelf: "" })),
      starts: [],
      palette: REFERENCE_BUCKRAM,
      wing: REFERENCE,
      docs: true,
    });
  });
  if (docCases.length) {
    plaques.push({ x: hall.x0 + 3.1, z: hall.z0 + 5 + docCases.length * 2.1 + 1.5, rot: Math.PI / 2, title: text.reference, sub: `${lib.docs.length} ${text.docs}`, wing: null });
  }

  const shelfLabels: ShelfLabel[] = [];
  for (const c of cases) {
    const H = caseHeight(c.levels);
    c.starts.forEach((s, k) => {
      shelfLabels.push({
        x: c.x - c.width / 2 + 0.5 + ((s.at % 14) / 14) * (c.width - 1),
        y: H + 0.35 + (k % 2) * 0.75,
        z: c.z + 0.3,
        name: s.name,
        n: s.n,
        wing: c.wing,
      });
    });
  }

  return {
    hall,
    cases,
    plaques,
    wings,
    shelfLabels,
    desk: v3(0, 1.2, front - 5.2),
    lectern: v3(0, 1.32, front - 8.6),
    catalog: v3(NAVE + 6.5, 0, front - 4),
    table: v3(-NAVE - 8.5, 0, front - 6.0),
  };
}

/** One book where it stands: the scene draws it, and the page reads it back by id. */
export interface BookSpot {
  id: string;
  label: string;
  terms: number;
  /** A reference volume's chunks; 0 for a memory. */
  chunks: number;
  doc: boolean;
  /** Thickness and height. */
  w: number;
  h: number;
  /** Its middle, in the room. */
  p: Vec3;
  rot: number;
  color: string;
  wing: number;
  shelf: string;
  /** Its instance in the one mesh that draws every book. */
  index: number;
}

/** Turns a point about the vertical axis by rot radians, the way three's applyAxisAngle does. */
export function turnY(p: Vec3, rot: number): Vec3 {
  const c = Math.cos(rot);
  const s = Math.sin(rot);
  return { x: p.x * c + p.z * s, y: p.y, z: -p.x * s + p.z * c };
}

/**
 * Stands a case's books left to right, bottom shelf up. Each book's height
 * and leather come from the hash of its id, so a book keeps its look when the
 * room is rebuilt around it.
 */
export function standBooks(c: CasePlan, first = 0): BookSpot[] {
  const out: BookSpot[] = [];
  let lvl = 0;
  let cx = -c.width / 2 + CASE_INSET;
  for (const b of c.books) {
    const bw = c.docs ? docWidth(b.chunks) : bookWidth(b.terms);
    if (cx + bw > c.width / 2 - CASE_INSET) {
      lvl++;
      cx = -c.width / 2 + CASE_INSET;
    }
    if (lvl >= c.levels) break;
    const h0 = hash(b.id);
    const h = LEVEL_H * (0.62 + h0 * 0.26) * (c.docs ? 1.08 : 1);
    const local = v3(cx + bw / 2, 0.25 + lvl * LEVEL_H + h / 2, 0.02);
    const t = turnY(local, c.rot);
    out.push({
      id: b.id,
      label: b.label || b.id,
      terms: b.terms,
      chunks: b.chunks,
      doc: c.docs,
      w: bw,
      h,
      p: v3(t.x + c.x, t.y, t.z + c.z),
      rot: c.rot,
      color: c.palette[Math.floor(h0 * 997) % c.palette.length] ?? "#7a553a",
      wing: c.wing,
      shelf: b.shelf,
      index: first + out.length,
    });
    cx += bw + BOOK_GAP;
  }
  return out;
}

export interface Layout {
  lib: Shelved;
  plan: Plan;
  books: BookSpot[];
  byId: Map<string, BookSpot>;
}

/** The plan and every book's place in it. */
export function layoutLibrary(lib: Shelved, text: PlanText): Layout {
  const plan = planLibrary(lib, text);
  const books: BookSpot[] = [];
  for (const c of plan.cases) books.push(...standBooks(c, books.length));
  const byId = new Map<string, BookSpot>();
  for (const b of books) byId.set(b.id, b);
  return { lib, plan, books, byId };
}

/** Which wing a book belongs to, for words: its wing's name, the stacks, the annex or the reference room. */
export function wingName(lib: Shelved, wing: number, text: { stacks: string; annex: string; reference: string }): string {
  if (wing === STACKS) return text.stacks;
  if (wing === ANNEX) return text.annex;
  if (wing === REFERENCE) return text.reference;
  return lib.wings[wing]?.name ?? "";
}

/** Where a book stands, in a few words: its project shelf, or the room it is in. */
export function whereOf(b: BookSpot | undefined, text: { stacks: string; reference: string }): string {
  if (!b) return "";
  if (b.wing === STACKS) return text.stacks;
  if (b.wing === REFERENCE) return text.reference;
  return b.shelf;
}

/** Where a card's books are, by wing, most first. Books not on the floor are left out. */
export function whereBooks(lib: Shelved, byId: ReadonlyMap<string, BookSpot>, ids: readonly string[], text: { stacks: string; annex: string; reference: string }): Array<[string, number]> {
  const count = new Map<string, number>();
  for (const id of ids) {
    const b = byId.get(id);
    if (!b) continue;
    const k = wingName(lib, b.wing, text) || "?";
    count.set(k, (count.get(k) ?? 0) + 1);
  }
  return [...count.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
}

/** Books whose id or title holds the query, at most limit; reference volumes are not books here. */
export function findBooks(books: readonly BookSpot[], q: string, limit = 8): BookSpot[] {
  const lq = q.trim().toLowerCase();
  if (!lq) return [];
  const out: BookSpot[] = [];
  for (const b of books) {
    if (b.doc) continue;
    if (b.id.toLowerCase().includes(lq) || b.label.toLowerCase().includes(lq)) out.push(b);
    if (out.length >= limit) break;
  }
  return out;
}

/** The terms of a call worth lighting books by: three letters or more, lower-cased. */
export function recallTerms(ev: Pick<ActivityEvent, "terms">): string[] {
  return (ev.terms ?? []).map((t) => String(t).toLowerCase()).filter((t) => t.length >= 3);
}

/**
 * The books a call read, by its terms. Matched loosely against titles and ids,
 * because a tool's arguments name things the way a person does and the graph
 * keys them the way a database does (see Event.Terms in live.go).
 */
export function booksRead(books: readonly BookSpot[], ev: Pick<ActivityEvent, "terms">, limit = 12): BookSpot[] {
  const terms = recallTerms(ev);
  if (!terms.length) return [];
  const out: BookSpot[] = [];
  for (const b of books) {
    const s = `${b.label} ${b.id}`.toLowerCase();
    if (terms.some((t) => s.includes(t))) out.push(b);
    if (out.length >= limit) break;
  }
  return out;
}

/** What the page was asked to open. A book wins over a card when a link names both. */
export type Opening = { kind: "book"; id: string } | { kind: "card"; id: string } | null;

export function readOpening(search: string | URLSearchParams): Opening {
  const q = typeof search === "string" ? new URLSearchParams(search) : search;
  const book = q.get("book")?.trim();
  if (book) return { kind: "book", id: book };
  const card = q.get("card")?.trim();
  if (card) return { kind: "card", id: card };
  return null;
}

/**
 * The page's query with something else open (or nothing), keeping every other
 * option the link carried: ?lang=, ?mode= and whatever an embedder added.
 */
export function withOpening(search: string | URLSearchParams, open: Opening): string {
  const q = new URLSearchParams(typeof search === "string" ? search : search.toString());
  q.delete("book");
  q.delete("card");
  if (open) q.set(open.kind, open.id);
  const s = q.toString();
  return s ? `?${s}` : "";
}
