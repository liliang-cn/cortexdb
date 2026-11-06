import { describe, expect, it } from "vitest";
import { BOX_W, COMPACT_BELOW, buildLanes, cardinalityGlyph, countLabel, drawsDiagram, layout, litBy, resolveSelection, type Rect } from "./ontologyLayout";
import { absent, draft, noUsage, transit } from "./ontologyFixtures";

const overlaps = (a: Rect, b: Rect) => Math.min(a.x + a.w, b.x + b.w) > Math.max(a.x, b.x) && Math.min(a.y + a.h, b.y + b.h) > Math.max(a.y, b.y);

describe("the lanes", () => {
  it("are one per interface, in the order the schema declares them, then the types implementing nothing", () => {
    const lanes = buildLanes(transit());
    expect(lanes.map((l) => l.iface?.api_name ?? null)).toEqual(["Locatable", "Scheduled", null]);
    expect(lanes.map((l) => l.types.map((t) => t.api_name))).toEqual([["Airport", "Station", "Gate"], ["Flight", "Train"], ["Airline", "Passenger"]]);
  });

  it("put every type in exactly one lane: its first declared interface the schema has", () => {
    // Flight implements Scheduled first, then Locatable.
    const rep = transit();
    const placed = buildLanes(rep).flatMap((l) => l.types.map((t) => t.api_name));
    expect(placed.sort()).toEqual(rep.object_types.map((t) => t.api_name).sort());
    const ghost = transit({ object_types: [{ ...transit().object_types[3]!, implements: ["Nowhere", "Locatable"] }] });
    expect(buildLanes(ghost)[0]?.types.map((t) => t.api_name)).toEqual(["Flight"]);
  });

  it("have no lane for the loose types when every type implements something", () => {
    const rep = transit({ object_types: transit().object_types.slice(0, 5) });
    expect(buildLanes(rep).map((l) => l.iface?.api_name)).toEqual(["Locatable", "Scheduled"]);
  });

  it("mark the other interfaces a type implements on its box, in their lanes' colours", () => {
    const L = layout(transit(), 1200);
    const flight = L.boxes.get("flight");
    expect(flight?.lane).toBe(1);
    expect(flight?.dots.map((d) => [d.iface, d.colorIndex])).toEqual([
      ["scheduled", 1],
      ["locatable", 0],
    ]);
  });
});

describe("the drawing", () => {
  it("is the same picture every time for the same report and width", () => {
    // Comparing a count with the one seen a minute ago needs the box in the
    // same place both times.
    expect(layout(transit(), 1200)).toEqual(layout(transit(), 1200));
  });

  it("does not move a box when only the counts change", () => {
    const before = layout(transit(), 1200);
    const after = layout(transit({ object_types: transit().object_types.map((t) => ({ ...t, instances: t.instances * 10 + 1 })) }), 1200);
    for (const [k, box] of before.boxes) expect([after.boxes.get(k)?.x, after.boxes.get(k)?.y]).toEqual([box.x, box.y]);
  });

  it("is drawn for a draft, which has boxes and nothing saved", () => {
    expect(drawsDiagram(draft())).toBe(true);
    expect(draft().saved).toBe(false);
    expect(drawsDiagram(absent())).toBe(false);
  });

  it("puts the header above a lane on a phone, and every box inside the canvas", () => {
    const narrow = layout(transit(), 340);
    expect(narrow.compact).toBe(true);
    expect(layout(transit(), COMPACT_BELOW).compact).toBe(false);
    for (const box of narrow.boxes.values()) expect(box.x + BOX_W).toBeLessThanOrEqual(340);
  });

  it("gives the arcs the room they need, and the lane bands the same width", () => {
    const L = layout(transit(), 600);
    for (const r of L.links) {
      expect(r.label.x + r.label.w).toBeLessThanOrEqual(L.width);
      expect(r.label.y).toBeGreaterThanOrEqual(L.top);
      expect(r.label.y + r.label.h).toBeLessThanOrEqual(L.top + L.height);
    }
    for (const lane of L.lanes) expect(lane.band.w).toBe(L.width - 20);
  });

  it("keeps link names off each other", () => {
    for (const width of [340, 800, 1200]) {
      const labels = layout(transit(), width).links.map((r) => r.label);
      for (let i = 0; i < labels.length; i++) for (let j = i + 1; j < labels.length; j++) expect(overlaps(labels[i]!, labels[j]!), `${width}: ${labels[i]?.text} on ${labels[j]?.text}`).toBe(false);
    }
  });

  it("says which link types name a type the schema does not declare, rather than drawing them nowhere", () => {
    const rep = transit();
    rep.link_types.push({ ...rep.link_types[0]!, api_name: "toNowhere", b: { ...rep.link_types[0]!.b, object_type: "Ghost" } });
    expect(layout(rep, 1200).dangling.map((l) => l.api_name)).toEqual(["toNowhere"]);
  });
});

describe("the link types", () => {
  it("carry a glyph per side, with the ONE side drawn as 1", () => {
    expect(cardinalityGlyph("ONE")).toBe("1");
    expect(cardinalityGlyph("MANY")).toBe("∗");
    // An undeclared side is a schema that has not decided, not a MANY.
    expect(cardinalityGlyph("")).toBe("?");
    const dep = layout(transit(), 1200).links.find((r) => r.lt.api_name === "flightDeparture");
    expect(dep?.ends.map((e) => [e.side, e.glyph, e.fk])).toEqual([
      ["a", "∗", false],
      ["b", "1", true],
    ]);
  });

  it("mark the foreign key on the side that declares it and nowhere else", () => {
    for (const r of layout(transit(), 1200).links) {
      for (const e of r.ends) expect(e.fk).toBe(!!(e.side === "a" ? r.lt.a.foreign_key : r.lt.b.foreign_key));
    }
  });

  it("draw a link a type has to itself out to the right of its box", () => {
    const L = layout(transit(), 1200);
    const loop = L.links.find((r) => r.lt.api_name === "connectsTo");
    const airport = L.boxes.get("airport");
    expect(loop?.self).toBe(true);
    expect(loop?.control.x).toBeGreaterThan((airport?.x ?? 0) + BOX_W);
  });

  it("bow a link between lanes to the right, away from the interface headers", () => {
    const L = layout(transit(), 1200);
    const dep = L.links.find((r) => r.lt.api_name === "flightDeparture");
    const airport = L.boxes.get("airport");
    expect(dep?.control.x).toBeGreaterThan((airport?.x ?? 0) + BOX_W);
  });

  it("are solid only when the store carries them", () => {
    const L = layout(transit(), 1200);
    expect(L.links.filter((r) => r.live).map((r) => r.lt.api_name)).toEqual(["flightDeparture", "operatedBy", "trainOrigin", "connectsTo"]);
    expect(layout(transit({ usage: noUsage() }), 1200).links.some((r) => r.live)).toBe(false);
  });
});

describe("the count in a box", () => {
  it("tells a declaration nothing uses from one whose count was not taken", () => {
    expect(countLabel(0, true)).toEqual({ text: "unused", unused: true, uncounted: false });
    expect(countLabel(0, false)).toEqual({ text: "not counted", unused: false, uncounted: true });
    expect(countLabel(4321, true)).toEqual({ text: "4,321", unused: false, uncounted: false });
  });

  it("draws the unused type dashed only when the store was counted", () => {
    expect(layout(transit(), 1200).boxes.get("passenger")?.count.unused).toBe(true);
    const bare = layout(transit({ usage: noUsage() }), 1200);
    for (const box of bare.boxes.values()) {
      expect(box.count.unused).toBe(false);
      expect(box.count.text).toBe("not counted");
      expect(box.bar).toBeNull();
    }
  });
});

describe("what a reader picks", () => {
  it("lights an interface's implementors wherever they sit", () => {
    expect(litBy(transit(), null)).toBeNull();
    expect(Array.from(litBy(transit(), "locatable") ?? []).sort()).toEqual(["airport", "flight", "gate", "station"]);
  });

  it("survives a refresh by name, and resolves to nothing once the name is gone", () => {
    const sel = { kind: "object" as const, name: "Flight" };
    const hit = resolveSelection(transit(), sel);
    expect(hit?.kind === "object" && hit.ot.instances).toBe(4);
    const later = transit({ object_types: transit().object_types.map((t) => (t.api_name === "Flight" ? { ...t, instances: 9 } : t)) });
    const again = resolveSelection(later, sel);
    expect(again?.kind === "object" && again.ot.instances).toBe(9);
    expect(resolveSelection(absent(), sel)).toBeNull();
    expect(resolveSelection(transit(), { kind: "link", name: "BOOKS" })?.kind).toBe("link");
    expect(resolveSelection(transit(), { kind: "interface", name: "Scheduled" })?.kind).toBe("interface");
  });
});
