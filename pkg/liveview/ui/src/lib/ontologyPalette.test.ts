import { describe, expect, it } from "vitest";
import { DARK_PALETTE, LIGHT_PALETTE, hexToHsl, isVioletOrMagenta, laneColor, paletteColors, paletteFor } from "./ontologyPalette";

describe("the ontology palette", () => {
  it("has no violet or magenta in either theme", () => {
    for (const p of [LIGHT_PALETTE, DARK_PALETTE]) {
      for (const hex of paletteColors(p)) {
        expect(hex, hex).toMatch(/^#[0-9a-f]{6}$/i);
        const { h, s } = hexToHsl(hex);
        expect(s > 0.25 && h >= 250 && h <= 335, `${hex} is hue ${h.toFixed(0)}, saturation ${s.toFixed(2)}`).toBe(false);
      }
    }
  });

  it("recognises violet and magenta when it sees them", () => {
    expect(isVioletOrMagenta("#7c3aed")).toBe(true);
    expect(isVioletOrMagenta("#c026d3")).toBe(true);
    expect(isVioletOrMagenta("#a855f7")).toBe(true);
    // Indigo just short of violet, grey with a violet cast, and the lanes' blues.
    expect(isVioletOrMagenta("#2563eb")).toBe(false);
    expect(isVioletOrMagenta("#8b8a90")).toBe(false);
  });

  it("reads hue and saturation the way the rule means them", () => {
    expect(hexToHsl("#ff0000")).toEqual({ h: 0, s: 1, l: 0.5 });
    expect(hexToHsl("#808080").s).toBe(0);
    expect(Math.round(hexToHsl("#0000ff").h)).toBe(240);
  });

  it("keeps the draft's amber out of the lanes, so a lane never reads as a draft", () => {
    for (const p of [LIGHT_PALETTE, DARK_PALETTE]) {
      expect(p.lanes).not.toContain(p.draft);
      for (const lane of p.lanes) {
        const h = hexToHsl(lane).h;
        expect(h < 30 || h > 60, `${lane} is amber`).toBe(true);
      }
    }
  });

  it("gives the loose lane its grey and cycles the accents", () => {
    expect(laneColor(LIGHT_PALETTE, null)).toBe(LIGHT_PALETTE.loose);
    expect(laneColor(LIGHT_PALETTE, LIGHT_PALETTE.lanes.length)).toBe(LIGHT_PALETTE.lanes[0]);
    expect(paletteFor(true)).toBe(DARK_PALETTE);
    expect(paletteFor(false)).toBe(LIGHT_PALETTE);
  });

  it("has as many accents in each theme", () => {
    expect(DARK_PALETTE.lanes.length).toBe(LIGHT_PALETTE.lanes.length);
    expect(new Set(LIGHT_PALETTE.lanes).size).toBe(LIGHT_PALETTE.lanes.length);
  });
});
