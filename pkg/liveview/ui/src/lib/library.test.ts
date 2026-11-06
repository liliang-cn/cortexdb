import { describe, expect, it } from "vitest";
import type { Library, LibraryBook, LibraryShelf } from "@/api/library";
import {
  ANNEX,
  REFERENCE,
  STACKS,
  bookWidth,
  booksRead,
  findBooks,
  hash,
  layoutLibrary,
  packCases,
  readOpening,
  shelve,
  shortId,
  standBooks,
  totals,
  turnY,
  whereBooks,
  whereOf,
  withOpening,
  type BookSpot,
  type PlanText,
} from "./library";

const text: PlanText = {
  wingSub: (p, b) => `${p} shelves · ${b} books`,
  stacks: "General stacks",
  stacksSub: "memories that name no project",
  annex: "Annex",
  reference: "Reference",
  docs: "documents",
};
const names = { stacks: "General stacks", annex: "Annex", reference: "Reference" };

const books = (prefix: string, n: number, terms = 3): LibraryBook[] =>
  Array.from({ length: n }, (_, i) => ({ id: `memory:${prefix}-${i}`, label: `about ${prefix} ${i}`, terms: (i * 7 + terms) % 15 }));
const shelf = (project: string, n: number): LibraryShelf & { books: LibraryBook[] } => ({ id: `entity:${project}`, project, books: books(project, n) });

function library(over: Partial<Library> = {}): Library {
  return {
    version: 3,
    source: "test.db",
    wings: [
      { name: "CortexDB", books: 70, shelves: [shelf("CortexDB", 50), shelf("alchemy", 20)] },
      { name: "haify", books: 30, shelves: [shelf("haify", 18), shelf("DRBD", 12)] },
      { name: "superai", books: 8, shelves: [shelf("superai", 8)] },
    ],
    annex: [shelf("qilang", 2), shelf("dsh", 3)],
    stacks: books("loose", 100),
    cards: 40,
    docs: [
      { id: "doc:a", title: "README", chunks: 3 },
      { id: "doc:b", title: "SPARQL subset", chunks: 30 },
    ],
    docs_known: true,
    records: true,
    ...over,
  };
}

/** A brain big enough to need several cases per room and several rows per side. */
function bigLibrary(): Library {
  const wings = Array.from({ length: 9 }, (_, w) => {
    const shelves = Array.from({ length: 4 }, (_, s) => shelf(`p${w}-${s}`, 10 + ((w * 13 + s * 29) % 90)));
    return { name: `p${w}-0`, books: shelves.reduce((k, x) => k + x.books.length, 0), shelves };
  }).sort((a, b) => b.books - a.books);
  return library({
    wings,
    annex: Array.from({ length: 12 }, (_, i) => shelf(`small-${i}`, 1 + (i % 4))),
    stacks: books("loose", 1400, 1),
    docs: Array.from({ length: 95 }, (_, i) => ({ id: `doc:${i}`, title: `doc ${i}`, chunks: i % 40 })),
  });
}

describe("shelving the server's answer", () => {
  it("reads a null list as an empty one", () => {
    const s = shelve(library({ wings: null, annex: null, stacks: null, docs: null }));
    expect(s.wings).toEqual([]);
    expect(s.annex).toEqual([]);
    expect(s.stacks).toEqual([]);
    expect(s.docs).toEqual([]);
    const lay = layoutLibrary(s, text);
    expect(lay.books).toEqual([]);
    expect(lay.plan.cases).toEqual([]);
  });

  it("counts every book once, wherever it is filed", () => {
    expect(totals(shelve(library()))).toEqual({ books: 70 + 30 + 8 + 5 + 100, shelves: 2 + 2 + 1 + 2 });
  });
});

describe("packing shelves into cases", () => {
  it("runs a long shelf on into the next case and keeps its name on every book", () => {
    const cases = packCases([shelf("a", 5), shelf("b", 7), shelf("c", 2)], 6);
    expect(cases.map((c) => c.books.length)).toEqual([6, 6, 2]);
    expect(cases[0]?.starts).toEqual([
      { id: "entity:a", name: "a", at: 0, n: 5 },
      { id: "entity:b", name: "b", at: 5, n: 7 },
    ]);
    // b carries on into the second case without a second label…
    expect(cases[1]?.starts).toEqual([]);
    // …and its books there are still b's.
    expect(cases[1]?.books.map((b) => b.shelf)).toEqual(["b", "b", "b", "b", "b", "b"]);
    expect(cases[2]?.starts).toEqual([{ id: "entity:c", name: "c", at: 0, n: 2 }]);
  });
});

describe("the room's layout", () => {
  it("gives every book exactly one place, even in a big brain", () => {
    const lib = shelve(bigLibrary());
    const lay = layoutLibrary(lib, text);
    const want = totals(lib).books + lib.docs.length;
    expect(lay.books).toHaveLength(want);
    expect(lay.byId.size).toBe(want);
    expect(lay.books.map((b) => b.index)).toEqual(lay.books.map((_, i) => i));
  });

  it("keeps each book inside its case, on one of its levels", () => {
    const lay = layoutLibrary(shelve(bigLibrary()), text);
    for (const c of lay.plan.cases) {
      for (const b of standBooks(c)) {
        // Undo the case's turn and offset to read the book in the case's own frame.
        const local = turnY({ x: b.p.x - c.x, y: b.p.y, z: b.p.z - c.z }, -c.rot);
        expect(Math.abs(local.x) + b.w / 2).toBeLessThanOrEqual(c.width / 2 + 1e-9);
        expect(local.y + b.h / 2).toBeLessThan(c.levels * 0.58 + 0.25 + 1e-9);
      }
    }
  });

  /** Brains shaped to push the hall's walls: one wing far wider than a row, and a reference room of a thousand documents. */
  const extremes: Array<[string, Library]> = [
    ["a big brain", bigLibrary()],
    ["one enormous side wing", library({ wings: [{ name: "a", books: 60, shelves: [shelf("a", 60)] }, { name: "b", books: 2000, shelves: [shelf("b", 2000)] }] })],
    ["a thousand documents", library({ wings: [{ name: "a", books: 8, shelves: [shelf("a", 8)] }], annex: [], stacks: [], docs: Array.from({ length: 1000 }, (_, i) => ({ id: `doc:${i}`, title: `d${i}`, chunks: i % 9 })) })],
  ];

  for (const [name, raw] of extremes) {
    it(`stands no two cases in each other and every case inside the hall, for ${name}`, () => {
      const { plan, books: spots } = layoutLibrary(shelve(raw), text);
      expect(spots.length).toBe(totals(shelve(raw)).books + (raw.docs ?? []).length);
      const box = (c: (typeof plan.cases)[number]) => {
        // The sides and cornice stand 0.2 past the shelves; the case is 0.62 deep.
        const hw = c.width / 2 + 0.2;
        const hd = 0.35;
        return c.rot ? { x0: c.x - hd, x1: c.x + hd, z0: c.z - hw, z1: c.z + hw } : { x0: c.x - hw, x1: c.x + hw, z0: c.z - hd, z1: c.z + hd };
      };
      // Cases that only touch are fine; the tolerance keeps rounding from calling that an overlap.
      const eps = 1e-6;
      const boxes = plan.cases.map(box);
      for (let i = 0; i < boxes.length; i++) {
        const a = boxes[i]!;
        expect(a.x0).toBeGreaterThanOrEqual(plan.hall.x0);
        expect(a.x1).toBeLessThanOrEqual(plan.hall.x1);
        expect(a.z0).toBeGreaterThanOrEqual(plan.hall.z0);
        expect(a.z1).toBeLessThanOrEqual(plan.hall.z1);
        for (let j = i + 1; j < boxes.length; j++) {
          const b = boxes[j]!;
          const overlap = a.x0 < b.x1 - eps && b.x0 < a.x1 - eps && a.z0 < b.z1 - eps && b.z0 < a.z1 - eps;
          expect(overlap, `cases ${i} and ${j} overlap`).toBe(false);
        }
      }
      // The reading table, the lectern and the catalogue stand clear of every case.
      for (const p of [plan.desk, plan.lectern, plan.catalog]) {
        for (const a of boxes) expect(p.x > a.x0 - 1 && p.x < a.x1 + 1 && p.z > a.z0 - 1 && p.z < a.z1 + 1).toBe(false);
      }
    });
  }

  it("puts a plaque on every wing, the annex, the stacks and the reference room, and knows where each wing is", () => {
    const lib = shelve(library());
    const { plan } = layoutLibrary(lib, text);
    expect(plan.plaques.map((p) => p.title).sort()).toEqual(["Annex", "CortexDB", "General stacks", "Reference", "haify", "superai"]);
    expect(plan.plaques.find((p) => p.title === "Reference")?.wing).toBeNull();
    for (const w of [0, 1, 2, ANNEX, STACKS]) expect(plan.wings.has(w), `wing ${w}`).toBe(true);
  });

  it("labels each shelf where it starts, over its own wing's cases", () => {
    const { plan } = layoutLibrary(shelve(library()), text);
    expect(plan.shelfLabels.map((l) => `${l.name}:${l.n}:${l.wing}`).sort()).toEqual([
      "CortexDB:50:0",
      "DRBD:12:1",
      "alchemy:20:0",
      "dsh:3:-2",
      "haify:18:1",
      "qilang:2:-2",
      "superai:8:2",
    ]);
  });

  it("files every book under the room it stands in", () => {
    const lay = layoutLibrary(shelve(library()), text);
    expect(lay.byId.get("memory:alchemy-3")).toMatchObject({ wing: 0, shelf: "alchemy", doc: false });
    expect(lay.byId.get("memory:qilang-1")).toMatchObject({ wing: ANNEX, shelf: "qilang" });
    expect(lay.byId.get("memory:loose-7")).toMatchObject({ wing: STACKS, shelf: "" });
    expect(lay.byId.get("doc:b")).toMatchObject({ wing: REFERENCE, doc: true, label: "SPARQL subset" });
  });

  it("comes out the same when it is built again", () => {
    const a = layoutLibrary(shelve(bigLibrary()), text);
    const b = layoutLibrary(shelve(bigLibrary()), text);
    expect(b.books).toEqual(a.books);
  });
});

describe("a book's look", () => {
  it("is as thick as its index, up to a cap", () => {
    expect(bookWidth(0)).toBeCloseTo(0.07);
    expect(bookWidth(6)).toBeGreaterThan(bookWidth(1));
    expect(bookWidth(12)).toBeCloseTo(0.2);
    expect(bookWidth(400)).toBeCloseTo(0.2);
  });

  it("hashes ids to a stable fraction", () => {
    expect(hash("memory:a")).toBe(hash("memory:a"));
    expect(hash("memory:a")).not.toBe(hash("memory:b"));
    for (const id of ["", "x", "memory:cortexdb", "记忆"]) {
      expect(hash(id)).toBeGreaterThanOrEqual(0);
      expect(hash(id)).toBeLessThan(1);
    }
  });

  it("turns about the vertical the way three does", () => {
    const p = turnY({ x: 1, y: 2, z: 0 }, Math.PI / 2);
    expect(p.x).toBeCloseTo(0);
    expect(p.y).toBe(2);
    expect(p.z).toBeCloseTo(-1);
  });

  it("drops a namespace from an id", () => {
    expect(shortId("memory:cortexdb-release")).toBe("cortexdb-release");
    expect(shortId("plain")).toBe("plain");
  });
});

describe("reading the room back", () => {
  const lib = shelve(library());
  const lay = layoutLibrary(lib, text);

  it("says where a book stands", () => {
    expect(whereOf(lay.byId.get("memory:alchemy-0"), names)).toBe("alchemy");
    expect(whereOf(lay.byId.get("memory:loose-0"), names)).toBe("General stacks");
    expect(whereOf(lay.byId.get("doc:a"), names)).toBe("Reference");
    expect(whereOf(undefined, names)).toBe("");
  });

  it("counts a card's books by wing, most first, and leaves out books not on the floor", () => {
    const ids = ["memory:CortexDB-1", "memory:alchemy-2", "memory:haify-0", "memory:loose-1", "memory:loose-2", "memory:loose-3", "memory:qilang-0", "memory:gone"];
    expect(whereBooks(lib, lay.byId, ids, names)).toEqual([
      ["General stacks", 3],
      ["CortexDB", 2],
      ["Annex", 1],
      ["haify", 1],
    ]);
  });

  it("finds books by title or id, ignoring case, and not the reference volumes", () => {
    expect(findBooks(lay.books, "ABOUT ALCHEMY 1").map((b) => b.id)).toEqual(["memory:alchemy-1", "memory:alchemy-10", "memory:alchemy-11", "memory:alchemy-12", "memory:alchemy-13", "memory:alchemy-14", "memory:alchemy-15", "memory:alchemy-16"]);
    expect(findBooks(lay.books, "readme")).toEqual([]);
    expect(findBooks(lay.books, "  ")).toEqual([]);
    expect(findBooks(lay.books, "qilang", 1)).toHaveLength(1);
  });

  it("lights the books a call names, by terms of three letters or more", () => {
    const ids = (bs: BookSpot[]) => bs.map((b) => b.id);
    expect(ids(booksRead(lay.books, { terms: ["DSH"] }))).toEqual(["memory:dsh-0", "memory:dsh-1", "memory:dsh-2"]);
    // "qi" is too short to mean anything on its own.
    expect(booksRead(lay.books, { terms: ["qi"] })).toEqual([]);
    expect(booksRead(lay.books, {})).toEqual([]);
    expect(booksRead(lay.books, { terms: ["loose"] })).toHaveLength(12);
  });
});

describe("the page's URL", () => {
  it("opens a book or a card, and a book when a link names both", () => {
    expect(readOpening("?book=memory:a")).toEqual({ kind: "book", id: "memory:a" });
    expect(readOpening("?card=entity:x&lang=zh")).toEqual({ kind: "card", id: "entity:x" });
    expect(readOpening("?card=entity:x&book=memory:a")).toEqual({ kind: "book", id: "memory:a" });
    expect(readOpening("?book=%20&lang=en")).toBeNull();
    expect(readOpening(new URLSearchParams("book=memory%3Ab"))).toEqual({ kind: "book", id: "memory:b" });
  });

  it("keeps the language, the mode and an embedder's options when something else is opened", () => {
    expect(withOpening("?lang=zh&mode=dark&book=memory:a", { kind: "card", id: "entity:x y" })).toBe("?lang=zh&mode=dark&card=entity%3Ax+y");
    expect(withOpening("?lang=zh&card=entity:x", null)).toBe("?lang=zh");
    expect(withOpening("", null)).toBe("");
    expect(readOpening(withOpening("?theme=embed", { kind: "book", id: "memory:a&b" }))).toEqual({ kind: "book", id: "memory:a&b" });
  });
});
