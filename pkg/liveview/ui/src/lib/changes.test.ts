import { describe, expect, it } from "vitest";
import type { ChangeItem } from "@/api";
import {
  dayLabel,
  filtersFromParams,
  filtersKey,
  filtersToParams,
  filtersToQuery,
  groupByDay,
  matchesFilters,
  mergeFeed,
  noFilters,
  reasonView,
  reasonWords,
  schemaLabel,
  valueOfSummary,
  type ChangeFilters,
} from "./changes";

const ref = (id: string, label = id) => ({ id, label });
function item(seq: number, over: Partial<ChangeItem> = {}): ChangeItem {
  return { seq, at: 1_700_000_000_000 + seq * 1000, kind: "edge", op: "added", id: `e${seq}`, summary: `row ${seq}`, entities: [], ...over };
}
const lives = (predicate = "lives_in", to = "Chengdu", over: Partial<ChangeItem> = {}) =>
  item(2, { edge: { source: ref("leo", "Leo"), predicate, target: ref("cd", to) }, summary: `Leo ${predicate} ${to}`, ...over });

describe("reason wording", () => {
  it("says what a replacement replaced, with the schema it was decided under", () => {
    const c = lives("lives_in", "Chengdu", {
      replaces: { id: "e1", summary: "Leo lives_in Beijing" },
      schema: { id: "people", version: 2 },
    });
    expect(reasonView(c)).toEqual({ text: "replaced Beijing", schema: "people v2" });
  });

  it("says what took a superseded row's place", () => {
    const c = lives("lives_in", "Beijing", { op: "superseded", reason: "superseded", replaced_by: { id: "e2", summary: "Leo lives_in Chengdu" } });
    expect(reasonView(c)?.text).toBe("replaced by Chengdu");
  });

  it("says where a merged row went", () => {
    const c = item(3, { kind: "node", op: "merged", replaced_by: { id: "n2", summary: "entity Leo Li" } });
    expect(reasonView(c)?.text).toBe("merged into entity Leo Li");
  });

  it("has nothing to add to a plain addition", () => {
    expect(reasonView(item(1))).toBeNull();
    expect(reasonView(item(1, { reason: "insert_whatever" }))).toBeNull();
  });

  it("spells out raw reasons it knows and tidies the ones it does not", () => {
    expect(reasonWords("document_deleted")).toBe("its source document was deleted");
    expect(reasonWords("some_new_reason")).toBe("some new reason");
    expect(reasonView(item(4, { op: "deleted", reason: "orphaned" }))?.text).toBe("nothing refers to it any more");
    expect(reasonView(item(5, { op: "retracted" }))?.text).toBe("taken back");
  });

  it("names a schema with and without a version", () => {
    expect(schemaLabel({ id: "people", version: 2 })).toBe("people v2");
    expect(schemaLabel({ id: "people", version: 0 })).toBe("people");
    expect(schemaLabel(undefined)).toBe("");
  });

  it("keeps a summary whole when it does not start with the edge's own words", () => {
    expect(valueOfSummary("something else", lives())).toBe("something else");
  });
});

describe("day grouping", () => {
  const noon = (y: number, m: number, d: number) => new Date(y, m - 1, d, 12).getTime();
  const now = noon(2026, 11, 6);

  it("labels today, yesterday and older days", () => {
    expect(dayLabel(noon(2026, 11, 6), now)).toBe("Today");
    expect(dayLabel(noon(2026, 11, 5), now)).toBe("Yesterday");
    expect(dayLabel(noon(2026, 9, 1), now)).toContain("September");
    expect(dayLabel(noon(2025, 9, 1), now)).toContain("2025");
  });

  it("groups consecutive rows of one local day and keeps order", () => {
    const rows = [
      { at: noon(2026, 11, 6) + 5000, n: 1 },
      { at: noon(2026, 11, 6), n: 2 },
      { at: noon(2026, 11, 5), n: 3 },
    ];
    const g = groupByDay(rows, now);
    expect(g.map((x) => [x.label, x.items.map((i) => i.n)])).toEqual([
      ["Today", [1, 2]],
      ["Yesterday", [3]],
    ]);
  });
});

describe("filters", () => {
  const f: ChangeFilters = { kind: ["edge", "node"], op: ["superseded"], producer: ["sparql.update"], entity: ["leo"] };

  it("survives a round trip through the URL and keeps other parameters", () => {
    const base = new URLSearchParams("mode=dark");
    const params = filtersToParams(f, base);
    expect(params.get("mode")).toBe("dark");
    expect(filtersFromParams(new URLSearchParams(params.toString()))).toEqual(f);
    expect(filtersToParams(noFilters, params).toString()).toBe("mode=dark");
  });

  it("ignores empty and repeated values when reading", () => {
    const g = filtersFromParams(new URLSearchParams("op=added&op=added&op=&kind=node"));
    expect(g).toEqual({ ...noFilters, op: ["added"], kind: ["node"] });
  });

  it("asks the server with the same filters, leaving unset ones out", () => {
    expect(filtersToQuery(f)).toEqual({ kind: ["edge", "node"], op: ["superseded"], producer: ["sparql.update"], entity: ["leo"] });
    expect(filtersToQuery(noFilters)).toEqual({});
  });

  it("has one key whatever the order of values", () => {
    expect(filtersKey({ ...noFilters, op: ["a", "b"] })).toBe(filtersKey({ ...noFilters, op: ["b", "a"] }));
  });

  it("ORs within a filter and ANDs across filters, like the server", () => {
    const c = item(1, { op: "superseded", producer: "sparql.update", entities: ["leo", "cd"] });
    expect(matchesFilters(c, f)).toBe(true);
    expect(matchesFilters({ ...c, op: "added" }, f)).toBe(false);
    expect(matchesFilters({ ...c, producer: undefined }, f)).toBe(false);
    expect(matchesFilters({ ...c, entities: [], id: "other" }, f)).toBe(false);
    expect(matchesFilters({ ...c, entities: [], id: "leo" }, f)).toBe(true);
    expect(matchesFilters(c, { ...noFilters, op: ["added", "superseded"] })).toBe(true);
    expect(matchesFilters(c, noFilters)).toBe(true);
  });
});

describe("joining live and fetched rows", () => {
  const paged = [item(10), item(9), item(8)];

  it("puts live rows on top, newest first, without repeating a seq", () => {
    const live = [item(12), item(11), item(10)];
    expect(mergeFeed(paged, live, noFilters, true).map((c) => c.seq)).toEqual([12, 11, 10, 9, 8]);
  });

  it("applies the filters to the live rows too", () => {
    const live = [item(12, { op: "superseded" }), item(11, { op: "added" })];
    const only: ChangeFilters = { ...noFilters, op: ["superseded"] };
    expect(mergeFeed(paged, live, only, true).map((c) => c.seq)).toEqual([12, 10, 9, 8]);
  });

  it("drops live rows older than the fetched pages while older pages remain", () => {
    const live = [item(11), item(3)];
    expect(mergeFeed(paged, live, noFilters, true).map((c) => c.seq)).toEqual([11, 10, 9, 8]);
    expect(mergeFeed(paged, live, noFilters, false).map((c) => c.seq)).toEqual([11, 10, 9, 8, 3]);
  });

  it("shows the live rows alone before anything was fetched", () => {
    expect(mergeFeed([], [item(2), item(1)], noFilters, true).map((c) => c.seq)).toEqual([2, 1]);
  });
});
