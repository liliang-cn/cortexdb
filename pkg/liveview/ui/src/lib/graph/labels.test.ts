import { describe, expect, it } from "vitest";
import { planLabels, type LabelHost, type LabelLink, type LabelNode } from "./labels";
import { NO_FOCUS, type Focus } from "./model";

// Nodes live on a plane at z = 0 and project to the screen one to one.
function node(id: string, x: number, y: number, degree = 1, label = id): LabelNode {
  return { id, label, x, y, z: 0, degree, radius: 4, hidden: false, dying: null };
}

function host(nodes: LabelNode[], opts: Partial<LabelHost> = {}, links: LabelLink[] = []): LabelHost {
  return {
    focus: NO_FOCUS,
    nodes: new Map(nodes.map((n) => [n.id, n])),
    links: new Map(links.map((l) => [l.key, l])),
    project: (x, y) => ({ x, y }),
    size: () => ({ width: 1000, height: 800 }),
    nearby: () => [],
    hubs: () => nodes.map((n) => n.id),
    ambient: true,
    compact: false,
    ...opts,
  };
}

const keys = (h: LabelHost, shown?: Set<string>) => planLabels(h, shown).map((p) => p.key);

describe("planLabels", () => {
  it("places in priority order and skips a name that would land on one placed", () => {
    // b's label would sit on a's.
    const h = host([node("a", 100, 100, 9), node("b", 110, 100, 5), node("c", 400, 400, 1)]);
    expect(keys(h)).toEqual(["n:a", "n:c"]);
  });

  it("keeps a name already on screen when a higher-ranked one drifts into it", () => {
    const h = host([node("a", 100, 100, 9), node("b", 110, 100, 5)]);
    // b was named last frame (a had been elsewhere); a has just moved onto it.
    expect(keys(h, new Set(["n:b"]))).toEqual(["n:b"]);
  });

  it("still lets a higher tier take precedence over an incumbent in a lower one", () => {
    const centre = node("a", 100, 100, 9);
    const other = node("b", 110, 100, 5);
    const focus: Focus = { kind: "selection", nodes: new Set(["a", "b"]), edges: new Set(), labelled: new Set(), centre: "a" };
    const h = host([centre, other], { focus });
    expect(keys(h, new Set(["n:b"]))).toEqual(["n:a"]);
  });

  it("gives parallel edge labels their lines in the focus's order, whoever is placed first", () => {
    const a = node("a", 100, 100);
    const b = node("b", 300, 100);
    const links: LabelLink[] = [
      { key: "k1", label: "knows", source: "a", target: "b", hidden: false, dying: null },
      { key: "k2", label: "likes", source: "a", target: "b", hidden: false, dying: null },
    ];
    const focus: Focus = { kind: "path", nodes: new Set(["a", "b"]), edges: new Set(["k1", "k2"]), labelled: new Set(["k1", "k2"]), centre: null };
    const h = host([a, b], { focus, ambient: false }, links);
    const fresh = planLabels(h).filter((p) => p.key.startsWith("e:"));
    const held = planLabels(h, new Set(["e:k2"])).filter((p) => p.key.startsWith("e:"));
    const yOf = (ps: typeof fresh, k: string) => ps.find((p) => p.key === k)?.y;
    expect(held.map((p) => p.key)).toEqual(["e:k2", "e:k1"]);
    expect(yOf(held, "e:k1")).toBe(yOf(fresh, "e:k1"));
    expect(yOf(held, "e:k2")).toBe(yOf(fresh, "e:k2"));
  });

  it("names nothing off screen", () => {
    const h = host([node("far", 5000, 100)]);
    expect(keys(h)).toEqual([]);
  });
});
