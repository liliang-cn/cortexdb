import { describe, expect, it } from "vitest";
import type { EntityRelation, GraphHistory, HistoryVersion, RelationGroup } from "@/api";
import {
  describeInEffect,
  groupsWithHistory,
  historyByRelation,
  pastEntries,
  successorOf,
  relationKey,
  splitCoOccurs,
  versionFor,
  versionOther,
} from "./entity";

const rel = (id: string, predicate: string, other: string): EntityRelation => ({ edge_id: id, predicate, other: { id: other, label: other } });
const group = (predicate: string, ...edges: EntityRelation[]): RelationGroup => ({ predicate, edges });
const ver = (id: string, status: string, object: string, over: Partial<HistoryVersion> = {}): HistoryVersion => ({ id, subject: "leo", object, status, ...over });

describe("co_occurs", () => {
  it("is folded out of the relations and listed once per other end", () => {
    const s = splitCoOccurs([
      group("lives_in", rel("e1", "lives_in", "cd")),
      group("co_occurs", rel("c1", "co_occurs", "ann"), rel("c2", "co_occurs", "bob")),
      group("co_occurs", rel("c3", "co_occurs", "ann")),
    ]);
    expect(s.groups.map((g) => g.predicate)).toEqual(["lives_in"]);
    expect(s.together.map((e) => e.other.id)).toEqual(["ann", "bob"]);
  });
});

describe("in effect, in words", () => {
  it("explains a single-valued decision with the schema version", () => {
    const v = describeInEffect({ kind: "replaced", summary: "Replaced Beijing: lives_in is single-valued in schema people v2.", rule: "single_valued", schema_id: "people", schema_version: 2 }, "en");
    expect(v?.title).toBe("Replaced an earlier value");
    expect(v?.summary).toContain("schema people v2");
    // The server's sentence carries the rule and the schema; nothing is repeated.
    expect(v?.notes).toEqual([]);
    expect(v?.several).toBe(false);
  });

  it("says several values are in effect, in the server's words, without repeating the opinion", () => {
    const v = describeInEffect({ kind: "several_in_effect", summary: "Leo knows Ann is one of 2 values in effect (also: Bob); the schema has no opinion.", schema_opinion: "no_opinion" }, "en");
    expect(v?.several).toBe(true);
    expect(v?.notes).toEqual([]);
  });

  it("writes the schema's opinion out when the server sent no sentence", () => {
    expect(describeInEffect({ kind: "several_in_effect", summary: "", schema_opinion: "no_opinion" }, "en")?.notes[0]).toContain("no opinion");
  });

  it("counts other records that state the same thing", () => {
    expect(describeInEffect({ kind: "only_version", summary: "", also_asserted_by: ["a"] }, "en")?.notes).toEqual(["Also stated by 1 other record."]);
  });

  it("has nothing to say without an explanation", () => {
    expect(describeInEffect(undefined, "en")).toBeNull();
    expect(describeInEffect({ kind: "only_version", summary: "", also_asserted_by: ["a", "b"] }, "zh")?.notes).toEqual(["另有 2 条记录也这么说。"]);
  });
});

describe("history under relations", () => {
  const h: GraphHistory = {
    entity: "leo",
    lines: [
      { subject: "leo", predicate: "lives_in", direction: "out", versions: [ver("e1", "superseded", "bj", { object_name: "Beijing", superseded_by: "e2", valid_to: "2026-11-02T10:00:00Z" }), ver("e2", "current", "cd", { object_name: "Chengdu" })] },
      { subject: "leo", predicate: "works_at", direction: "out", versions: [ver("w1", "retracted", "acme", { object_name: "Acme" })] },
      { subject: "ann", subject_name: "Ann", predicate: "knows", direction: "in", versions: [ver("k1", "current", "leo")] },
      { subject: "bob", subject_name: "Bob", predicate: "knows", direction: "in", versions: [ver("k2", "retracted", "leo", { retracted_at: "2026-11-03T10:00:00Z" })] },
      { subject: "leo", predicate: "co_occurs", direction: "out", versions: [ver("c1", "current", "ann")] },
    ],
  };
  const lines = historyByRelation(h);

  it("keys lines by direction and predicate, one list per key, and skips co_occurs", () => {
    expect([...lines.keys()].sort()).toEqual([relationKey("in", "knows"), relationKey("out", "lives_in"), relationKey("out", "works_at")]);
    expect(lines.get("in|knows")).toHaveLength(2);
  });

  it("finds the version behind a current relation by its edge id, then by the far end", () => {
    const out = lines.get("out|lives_in") ?? [];
    expect(versionFor(out, rel("e2", "lives_in", "cd"))?.object).toBe("cd");
    expect(versionFor(out, rel("other-id", "lives_in", "cd"))?.id).toBe("e2");
    expect(versionFor([], rel("e2", "lives_in", "cd"))).toBeUndefined();
    expect(versionFor(lines.get("in|knows") ?? [], rel("nope", "knows", "ann"))?.id).toBe("k1");
  });

  it("lists past versions across lines, most recently ended first", () => {
    expect(pastEntries(lines.get("in|knows") ?? []).map((p) => p.version.id)).toEqual(["k2"]);
    expect(pastEntries(lines.get("out|lives_in") ?? []).map((p) => p.version.id)).toEqual(["e1"]);
  });

  it("names what replaced a past version", () => {
    const out = lines.get("out|lives_in") ?? [];
    const old = out[0]!.versions[0]!;
    expect(successorOf(out, old)).toEqual({ id: "cd", name: "Chengdu", status: "current" });
    expect(successorOf(out, out[0]!.versions[1]!)).toBeUndefined();
  });

  it("keeps a predicate whose every version is past as an empty group", () => {
    const g = groupsWithHistory("out", [group("lives_in", rel("e2", "lives_in", "cd"))], lines);
    expect(g.map((x) => [x.predicate, x.edges.length])).toEqual([["lives_in", 1], ["works_at", 0]]);
    expect(groupsWithHistory("in", [group("knows", rel("k1", "knows", "ann"))], lines).map((x) => x.predicate)).toEqual(["knows"]);
  });

  it("names the far end from the entity's side", () => {
    expect(versionOther(lines.get("out|lives_in")![0]!, ver("e1", "superseded", "bj", { object_name: "Beijing" }))).toEqual({ id: "bj", name: "Beijing" });
    expect(versionOther(lines.get("in|knows")![0]!, ver("k1", "current", "leo"))).toEqual({ id: "ann", name: "Ann" });
  });
});
