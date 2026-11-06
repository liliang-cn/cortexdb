import { describe, expect, it } from "vitest";
import {
  CO_OCCURS,
  CO_OCCURS_COLOR,
  PREDICATE_PALETTE,
  hash32,
  heatColor,
  mixHex,
  oklchToRgb,
  predicateColor,
  predicateSlot,
  rankTypes,
  typeColor,
} from "./colors";

describe("predicate colours", () => {
  it("give the same predicate the same colour every time", () => {
    for (const p of ["uses", "owns", "lives_in", "depends_on", "part_of"]) {
      expect(predicateColor(p)).toBe(predicateColor(p));
      expect(predicateColor(p)).toBe(PREDICATE_PALETTE[predicateSlot(p)]);
    }
  });

  it("do not depend on the order predicates were first seen in", () => {
    const forward = ["uses", "owns", "runs"].map(predicateColor);
    const backward = ["runs", "owns", "uses"].map(predicateColor).reverse();
    expect(backward).toEqual(forward);
  });

  it("are pinned, so a change to the hash or the palette is noticed", () => {
    // A stored link or a screenshot in the docs relies on these staying put.
    expect(hash32("")).toBe(0x811c9dc5);
    expect(hash32("a")).toBe(0xe40c292c);
    expect(PREDICATE_PALETTE).toHaveLength(12);
    expect(predicateColor("uses")).toMatch(/^#[0-9a-f]{6}$/);
    expect(predicateSlot("uses")).toBe(hash32("uses") % 12);
    expect(PREDICATE_PALETTE[0]).toBe("#d86069");
  });

  it("use twelve distinct colours", () => {
    expect(new Set(PREDICATE_PALETTE).size).toBe(12);
  });

  it("keep neighbouring hues apart in the table", () => {
    // Slots are interleaved: two entries next to each other are never adjacent hues.
    const hue = (hex: string) => {
      const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255) as [number, number, number];
      const max = Math.max(r, g, b);
      const d = max - Math.min(r, g, b);
      if (d === 0) return 0;
      const h = max === r ? ((g - b) / d) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4;
      return (h * 60 + 360) % 360;
    };
    for (let i = 1; i < PREDICATE_PALETTE.length; i++) {
      const a = hue(PREDICATE_PALETTE[i - 1] as string);
      const b = hue(PREDICATE_PALETTE[i] as string);
      const gap = Math.min(Math.abs(a - b), 360 - Math.abs(a - b));
      expect(gap).toBeGreaterThan(50);
    }
  });

  it("draw co_occurs and an empty predicate in the neutral", () => {
    expect(predicateColor(CO_OCCURS)).toBe(CO_OCCURS_COLOR);
    expect(predicateColor("")).toBe(CO_OCCURS_COLOR);
    expect(PREDICATE_PALETTE).not.toContain(CO_OCCURS_COLOR);
  });

  it("read on white and on a dark ground", () => {
    const lum = (hex: string) => {
      const c = [1, 3, 5].map((i) => {
        const v = parseInt(hex.slice(i, i + 2), 16) / 255;
        return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
      }) as [number, number, number];
      return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
    };
    const contrast = (a: string, b: string) => {
      const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x) as [number, number];
      return (hi + 0.05) / (lo + 0.05);
    };
    for (const c of PREDICATE_PALETTE) {
      expect(contrast(c, "#ffffff")).toBeGreaterThan(2.4);
      expect(contrast(c, "#0d1016")).toBeGreaterThan(4);
    }
  });
});

describe("type colours", () => {
  it("are lighter on a dark ground than on a light one", () => {
    const lum = (hex: string) => parseInt(hex.slice(1, 3), 16) + parseInt(hex.slice(3, 5), 16) + parseInt(hex.slice(5, 7), 16);
    expect(lum(typeColor("person", true))).toBeGreaterThan(lum(typeColor("person", false)));
  });
  it("are stable per type name", () => {
    expect(typeColor("project", false)).toBe(typeColor("project", false));
    expect(typeColor("project", false)).not.toBe(typeColor("memory", false));
  });
  it("give the commonest types hues far apart, so they never share a colour", () => {
    // The demo brain's seven commonest types, four of which a hash put on one green.
    const rows = ["entity", "project", "feature", "software", "workspace", "Node", "tool"].map((key, i) => ({ key, count: 100 - i }));
    rankTypes(rows);
    const hexes = rows.map((r) => typeColor(r.key, false));
    expect(new Set(hexes).size).toBe(rows.length);
    const rgb = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
    for (let i = 0; i < hexes.length; i++) {
      for (let j = i + 1; j < hexes.length; j++) {
        const a = rgb(hexes[i]!);
        const b = rgb(hexes[j]!);
        const d = Math.hypot(a[0]! - b[0]!, a[1]! - b[1]!, a[2]! - b[2]!);
        expect(d, `${rows[i]!.key} vs ${rows[j]!.key}`).toBeGreaterThan(50);
      }
    }
  });
  it("say whether the ranking changed, so drawn colours can be repainted", () => {
    const rows = [{ key: "a", count: 2 }, { key: "b", count: 1 }];
    rankTypes(rows);
    expect(rankTypes(rows)).toBe(false);
    expect(rankTypes([{ key: "b", count: 3 }, { key: "a", count: 1 }])).toBe(true);
  });
  it("keep a rare type muted, so it cannot pass for a common one", () => {
    rankTypes([{ key: "entity", count: 10 }]);
    const chroma = (hex: string) => {
      const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
      return Math.max(r!, g!, b!) - Math.min(r!, g!, b!);
    };
    expect(chroma(typeColor("rare_type", false))).toBeLessThan(chroma(typeColor("entity", false)));
  });
  it("have a grey for no type", () => {
    expect(typeColor("", false)).toBe("#94a3b8");
  });
});

describe("colour helpers", () => {
  it("converts OKLCH white and black", () => {
    expect(oklchToRgb(1, 0, 0)).toEqual([255, 255, 255]);
    expect(oklchToRgb(0, 0, 0)).toEqual([0, 0, 0]);
  });
  it("mixes hex colours", () => {
    expect(mixHex("#000000", "#ffffff", 0.5)).toBe("#808080");
    expect(mixHex("#102030", "#ffffff", 0)).toBe("#102030");
  });
  it("ramps the recall heat from the ground grey to hot", () => {
    expect(heatColor(0, 10, false)).toBe("#cbd5e1");
    expect(heatColor(1, 10, false)).not.toBe(heatColor(10, 10, false));
  });
});
