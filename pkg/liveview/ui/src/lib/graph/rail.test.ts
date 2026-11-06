import { describe, expect, it } from "vitest";
import type { ActivityEvent, ChangeItem, Edge, Node } from "@/api";
import { buildAdjacency, edgeKey } from "./model";
import { buildRailItems, railSelection, railTargets } from "./rail";

const change = (seq: number, at: number, extra: Partial<ChangeItem> = {}): ChangeItem => ({
  seq, at, kind: "node", op: "added", id: `n${seq}`, summary: `entity ${seq}`, entities: [], ...extra,
});
const call = (seq: number, at: number, extra: Partial<ActivityEvent> = {}): ActivityEvent => ({ seq, at, kind: "query", tool: "recall", text: `call ${seq}`, ...extra });

describe("rail items", () => {
  it("interleave changes and calls newest first", () => {
    const items = buildRailItems([change(2, 200), change(1, 100)], [call(1, 150)], "all", 10);
    expect(items.map((i) => i.key)).toEqual(["c:2", `a:1:150`, "c:1"]);
  });
  it("filter to one kind", () => {
    expect(buildRailItems([change(1, 1)], [call(1, 2)], "changes", 10).every((i) => i.type === "change")).toBe(true);
    expect(buildRailItems([change(1, 1)], [call(1, 2)], "calls", 10).every((i) => i.type === "activity")).toBe(true);
  });
  it("stop at the limit", () => {
    const many = Array.from({ length: 50 }, (_, i) => change(i, i));
    expect(buildRailItems(many, [], "all", 5)).toHaveLength(5);
  });
  it("mark a failed call with the failure semantic", () => {
    const [item] = buildRailItems([], [call(1, 1, { failed: true })], "all", 5);
    expect(item?.semantic.key).toBe("failed");
  });
});

describe("what a row touched", () => {
  const nodes = new Map<string, Node>([["a", { id: "a", label: "Leo", type: "p" }], ["b", { id: "b", label: "Chengdu", type: "c" }]]);
  const ab: Edge = { source: "a", target: "b", label: "lives_in", id: "edge:1" };
  const edges = new Map([[edgeKey(ab), ab]]);
  const adj = buildAdjacency(edges.values());

  it("takes a change's entities that are on screen", () => {
    const [item] = buildRailItems([change(1, 1, { entities: ["a", "gone"] })], [], "all", 5);
    const t = railTargets(item!, nodes, edges, adj);
    expect([...t.nodes]).toEqual(["a"]);
  });
  it("finds an edge change by its ends and predicate", () => {
    const c = change(1, 1, { kind: "edge", id: "other", edge: { source: { id: "a", label: "Leo" }, predicate: "lives_in", target: { id: "b", label: "Chengdu" } } });
    const [item] = buildRailItems([c], [], "all", 5);
    const t = railTargets(item!, nodes, edges, adj);
    expect([...t.edges]).toEqual([edgeKey(ab)]);
    expect([...t.nodes].sort()).toEqual(["a", "b"]);
  });
  it("finds an edge change by its row id", () => {
    const [item] = buildRailItems([change(1, 1, { kind: "edge", id: "edge:1" })], [], "all", 5);
    expect(railTargets(item!, nodes, edges, adj).edges.size).toBe(1);
  });
  it("names the nodes and the linked edge of a tool call", () => {
    const [item] = buildRailItems([], [call(1, 1, { terms: ["leo"], links: [["Leo", "Chengdu"]] })], "all", 5);
    const t = railTargets(item!, nodes, edges, adj);
    expect([...t.nodes]).toEqual(["a"]);
    expect(t.edges.size).toBe(1);
  });
  it("selects the first node a row names, else the source of an edge", () => {
    expect(railSelection({ nodes: new Set(["b"]), edges: new Set() }, edges)).toBe("b");
    expect(railSelection({ nodes: new Set(), edges: new Set([edgeKey(ab)]) }, edges)).toBe("a");
    expect(railSelection({ nodes: new Set(), edges: new Set() }, edges)).toBeNull();
  });
});
