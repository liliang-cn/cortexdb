import { describe, expect, it } from "vitest";
import { ANNEX_EARTH, CANDLE_LIGHT, DAY, LAMP_LIGHT, LEATHERS, NIGHT, PAGE_INK, REFERENCE_BUCKRAM, STACK_CLOTH, WOOD, hsl, isViolet } from "./libraryPalette";

const hexes = (o: unknown): string[] => {
  if (typeof o === "string") return /^#[0-9a-f]{6}$/i.test(o) ? [o] : [];
  if (Array.isArray(o)) return o.flatMap(hexes);
  if (o && typeof o === "object") return Object.values(o).flatMap(hexes);
  return [];
};

describe("the reading room's colours", () => {
  it("knows a violet when it sees one", () => {
    // The ones the old page's test refused by name.
    for (const v of ["#8b5cf6", "#a855f7", "#7c3aed", "#c084fc"]) expect(isViolet(v), v).toBe(true);
    // Blues, greys and the night window are not.
    for (const c of ["#2c4a6b", "#1d2c45", "#9fb3d6", "#808080", "#ffffff"]) expect(isViolet(c), c).toBe(false);
  });

  it("reads hue and saturation the usual way", () => {
    expect(hsl("#ff0000")).toEqual({ h: 0, s: 1, l: 0.5 });
    expect(hsl("#00ff00").h).toBeCloseTo(120);
    expect(hsl("#0000ff").h).toBeCloseTo(240);
    expect(() => hsl("violet")).toThrow();
  });

  it("has no violet in the leathers, the cloth, the wood or either light", () => {
    const all = hexes([LEATHERS, STACK_CLOTH, REFERENCE_BUCKRAM, ANNEX_EARTH, WOOD, DAY, NIGHT, PAGE_INK, LAMP_LIGHT, CANDLE_LIGHT]);
    expect(all.length).toBeGreaterThan(60);
    for (const c of all) {
      const { h, s } = hsl(c);
      expect(h >= 250 && h <= 335 && s > 0.25, `${c} is violet (hue ${h.toFixed(0)}°, saturation ${s.toFixed(2)})`).toBe(false);
    }
  });

  it("gives every wing's leathers more than one colour, so a shelf does not read as a block", () => {
    for (const set of LEATHERS) expect(new Set(set).size).toBeGreaterThan(2);
  });
});
