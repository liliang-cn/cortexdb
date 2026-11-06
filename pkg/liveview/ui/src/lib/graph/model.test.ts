import { describe, expect, it } from "vitest";
import type { Edge, Node } from "@/api";
import {
  NO_FILTERS,
  activityTargets,
  buildAdjacency,
  computeFocus,
  diffGraph,
  edgeKey,
  edgeShown,
  findNodeByName,
  matchTerms,
  mergeExplored,
  neighbourhood,
  nodeShown,
  predicateCounts,
  predicateShown,
  tracePath,
  typeCounts,
  type Filters,
} from "./model";

const n = (id: string, type = "entity", label = id): Node => ({ id, label, type });
const e = (source: string, label: string, target: string): Edge => ({ source, target, label, id: `edge:${source}:${label}:${target}` });
const nodeMap = (...list: Node[]) => new Map(list.map((x) => [x.id, x]));
const edgeMap = (...list: Edge[]) => new Map(list.map((x) => [edgeKey(x), x]));

const people = [n("leo", "person", "Leo"), n("ana", "person", "Ana"), n("cd", "city", "Chengdu"), n("bj", "city", "Beijing"), n("far")];
const rels = [e("leo", "lives_in", "cd"), e("ana", "lives_in", "bj"), e("leo", "knows", "ana"), e("leo", "co_occurs", "bj"), e("far", "knows", "bj")];

describe("neighbourhood selection", () => {
  const adj = buildAdjacency(rels);
  it("is the node, its edges both ways, and the other end of each", () => {
    const r = neighbourhood(adj, "leo");
    expect([...r.nodes].sort()).toEqual(["ana", "bj", "cd", "leo"]);
    expect(r.edges.size).toBe(3);
    expect(r.edges.has(edgeKey(e("far", "knows", "bj")))).toBe(false);
  });
  it("includes incoming edges", () => {
    expect([...neighbourhood(adj, "cd").nodes].sort()).toEqual(["cd", "leo"]);
  });
  it("leaves out the edges a filter removed, and the nodes only they reached", () => {
    const r = neighbourhood(adj, "leo", (x) => x.label !== "co_occurs");
    expect(r.nodes.has("bj")).toBe(false);
    expect(r.edges.size).toBe(2);
  });
  it("is just the node when it has no edges", () => {
    const r = neighbourhood(adj, "nobody");
    expect([...r.nodes]).toEqual(["nobody"]);
    expect(r.edges.size).toBe(0);
  });
  it("counts degree once per edge, and a self loop once", () => {
    const a = buildAdjacency([e("x", "is", "x"), e("x", "r", "y")]);
    expect(a.degree.get("x")).toBe(2);
    expect(a.degree.get("y")).toBe(1);
  });
});

describe("focus", () => {
  const nodes = nodeMap(...people);
  const adj = buildAdjacency(rels);
  const base = { nodes, adj, path: null, preview: null, selected: null, hovered: null, highlight: new Set<string>(), query: "", edgeShown: () => true };

  it("is none when nothing is selected", () => {
    expect(computeFocus(base).kind).toBe("none");
  });
  it("selecting a node focuses its neighbourhood and labels its edges", () => {
    const f = computeFocus({ ...base, selected: "leo" });
    expect(f.kind).toBe("selection");
    expect(f.centre).toBe("leo");
    expect(f.nodes.has("far")).toBe(false);
    expect(f.labelled.size).toBe(f.edges.size);
  });
  it("prefers the selection to a hover", () => {
    expect(computeFocus({ ...base, selected: "leo", hovered: "ana" }).centre).toBe("leo");
    expect(computeFocus({ ...base, hovered: "ana" }).kind).toBe("hover");
  });
  it("ignores a selected id that is no longer in the graph", () => {
    expect(computeFocus({ ...base, selected: "gone" }).kind).toBe("none");
  });
  it("lets a previewed rail row and a traced path outrank a selection", () => {
    const preview = { nodes: new Set(["ana"]), edges: new Set<string>() };
    expect(computeFocus({ ...base, selected: "leo", preview }).kind).toBe("preview");
    const path = { nodes: new Set(["leo", "cd"]), edges: new Set([edgeKey(rels[0] as Edge)]) };
    expect(computeFocus({ ...base, selected: "leo", preview, path }).kind).toBe("path");
  });
  it("lights an answer's ids that are on screen and the edges between them", () => {
    const f = computeFocus({ ...base, highlight: new Set(["leo", "cd", "elsewhere"]) });
    expect(f.kind).toBe("highlight");
    expect([...f.nodes].sort()).toEqual(["cd", "leo"]);
    expect(f.edges.size).toBe(1);
  });
  it("matches the name filter on label and id, ignoring case", () => {
    const f = computeFocus({ ...base, query: "CHENG" });
    expect(f.kind).toBe("query");
    expect([...f.nodes]).toEqual(["cd"]);
    expect(computeFocus({ ...base, query: "bj" }).nodes.has("bj")).toBe(true);
  });
});

describe("tracing a path", () => {
  const adj = buildAdjacency(rels);
  it("finds the shortest route and ignores direction", () => {
    const p = tracePath(adj, "cd", "ana");
    expect(p?.nodes).toEqual(["cd", "leo", "ana"]);
    expect(p?.edges).toHaveLength(2);
  });
  it("is null when the two are not connected", () => {
    expect(tracePath(buildAdjacency([e("a", "r", "b")]), "a", "z")).toBeNull();
  });
  it("is a single node from a node to itself", () => {
    expect(tracePath(adj, "leo", "leo")).toEqual({ nodes: ["leo"], edges: [] });
  });
});

describe("filters", () => {
  const nodes = nodeMap(...people);
  it("hides co_occurs until asked", () => {
    expect(predicateShown(NO_FILTERS, "co_occurs")).toBe(false);
    expect(predicateShown({ ...NO_FILTERS, showCoOccurs: true }, "co_occurs")).toBe(true);
    expect(predicateShown(NO_FILTERS, "knows")).toBe(true);
  });
  it("hides a predicate the legend switched off, even co_occurs after it is shown", () => {
    const f: Filters = { ...NO_FILTERS, showCoOccurs: true, hiddenPredicates: new Set(["co_occurs", "knows"]) };
    expect(predicateShown(f, "co_occurs")).toBe(false);
    expect(predicateShown(f, "knows")).toBe(false);
    expect(predicateShown(f, "lives_in")).toBe(true);
  });
  it("hides an edge when either end's type is hidden", () => {
    const f: Filters = { ...NO_FILTERS, hiddenTypes: new Set(["city"]) };
    expect(nodeShown(f, n("cd", "city"))).toBe(false);
    expect(edgeShown(f, e("leo", "lives_in", "cd"), nodes)).toBe(false);
    expect(edgeShown(f, e("leo", "knows", "ana"), nodes)).toBe(true);
  });
  it("treats an untyped node as its own type", () => {
    const f: Filters = { ...NO_FILTERS, hiddenTypes: new Set(["(untyped)"]) };
    expect(nodeShown(f, n("x", ""))).toBe(false);
  });
  it("counts types and predicates, largest first and then by name", () => {
    expect(typeCounts(people)).toEqual([
      { key: "city", count: 2 },
      { key: "person", count: 2 },
      { key: "entity", count: 1 },
    ]);
    expect(predicateCounts(rels)[0]).toEqual({ key: "knows", count: 2 });
    expect(predicateCounts([e("a", "", "b")])[0]?.key).toBe("related");
  });
});

describe("the store to scene diff", () => {
  const A = n("a");
  const B = n("b");
  const ab = e("a", "r", "b");
  it("is empty when nothing moved", () => {
    expect(diffGraph(nodeMap(A, B), edgeMap(ab), nodeMap(A, B), edgeMap(ab)).empty).toBe(true);
  });
  it("sees an added node, a removed node and a changed node by identity", () => {
    const B2 = { ...B, grade: "verified" };
    const C = n("c");
    const d = diffGraph(nodeMap(A, B), edgeMap(), nodeMap(B2, C), edgeMap());
    expect(d.addedNodes).toEqual([C]);
    expect(d.changedNodes).toEqual([B2]);
    expect(d.removedNodes).toEqual(["a"]);
  });
  it("touches only the rows that differ when thousands are unchanged", () => {
    const many = Array.from({ length: 2000 }, (_, i) => n(`n${i}`));
    const prev = nodeMap(...many);
    const next = new Map(prev);
    next.set("n7", { ...many[7] as Node, label: "renamed" });
    const d = diffGraph(prev, edgeMap(), next, edgeMap());
    expect(d.changedNodes).toHaveLength(1);
    expect(d.addedNodes).toHaveLength(0);
    expect(d.removedNodes).toHaveLength(0);
  });
  it("sees added, changed and removed edges", () => {
    const bc = e("b", "r", "c");
    const abGraded = { ...ab, grade: "verified" };
    const nodes = nodeMap(A, B, n("c"));
    const d = diffGraph(nodes, edgeMap(ab, bc), nodes, edgeMap(abGraded, e("c", "r", "a")));
    expect(d.changedEdges).toEqual([abGraded]);
    expect(d.addedEdges).toHaveLength(1);
    expect(d.removedEdges).toEqual([edgeKey(bc)]);
  });
  it("counts an edge to a missing node as absent", () => {
    const d = diffGraph(nodeMap(A), edgeMap(), nodeMap(A), edgeMap(ab));
    expect(d.addedEdges).toHaveLength(0);
    // and an edge that loses its end is removed
    const gone = diffGraph(nodeMap(A, B), edgeMap(ab), nodeMap(A), edgeMap(ab));
    expect(gone.removedEdges).toEqual([edgeKey(ab)]);
    expect(gone.removedNodes).toEqual(["b"]);
  });
  it("merges explored rows beside the core without overriding it", () => {
    const core = nodeMap(A);
    const extra = nodeMap({ ...A, label: "other" }, B);
    const merged = mergeExplored(core, extra);
    expect(merged.get("a")).toBe(A);
    expect(merged.get("b")).toBe(B);
    expect(mergeExplored(core, new Map())).toBe(core);
  });
});

describe("matching tool-call terms", () => {
  const nodes = nodeMap(n("1", "x", "Leo"), n("2", "x", "Leonardo da Vinci"), n("3", "x", "Chengdu"), n("4", "x", "Le"));
  it("prefers an exact name over a substring", () => {
    expect(matchTerms(nodes, ["leo"])).toEqual(["1"]);
  });
  it("falls back to substrings only for a term long enough, and caps them", () => {
    expect(matchTerms(nodes, ["chengd"])).toEqual(["3"]);
    expect(matchTerms(nodes, ["che"])).toEqual([]);
    const crowd = nodeMap(...Array.from({ length: 30 }, (_, i) => n(`id${i}`, "x", `thing number ${i}`)));
    expect(matchTerms(crowd, ["thing number"]).length).toBe(6);
  });
  it("ignores one-letter terms and no terms", () => {
    expect(matchTerms(nodes, ["l"])).toEqual([]);
    expect(matchTerms(nodes, undefined)).toEqual([]);
  });
  it("finds a node by name, then by id", () => {
    expect(findNodeByName(nodes, "chengdu")).toBe("3");
    expect(findNodeByName(nodes, "nothing")).toBeNull();
  });
});

describe("what an activity event lights", () => {
  const nodes = nodeMap(n("machine:1", "machine", "辊锻机"), n("machine:10", "machine", "压力机"), n("card:7", "card", "工艺卡 GY-0312"));
  const adj = buildAdjacency([]);
  it("lights nodes named by id exactly, never their namesakes by substring", () => {
    expect(activityTargets(nodes, adj, { nodes: ["machine:1"] }).nodes).toEqual(["machine:1"]);
  });
  it("puts ids before terms, without lighting a node twice, and skips ids it does not hold", () => {
    const got = activityTargets(nodes, adj, { nodes: ["card:7", "gone:1", "card:7"], terms: ["工艺卡 GY-0312", "辊锻机"] });
    expect(got.nodes).toEqual(["card:7", "machine:1"]);
  });
  it("lights a short name only by its whole label when it comes as a term", () => {
    expect(activityTargets(nodes, adj, { terms: ["辊锻"] }).nodes).toEqual([]);
    expect(activityTargets(nodes, adj, { terms: ["辊锻机"] }).nodes).toEqual(["machine:1"]);
  });
});
