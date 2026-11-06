import { describe, expect, it } from "vitest";
import { aimOffset, insetsOf, NO_INSETS, type Insets } from "./insets";

// A phone's stage: 390 wide, 796 tall, under a 48px top bar.
const stage = { top: 48, left: 0, right: 390, bottom: 844 };

describe("insetsOf", () => {
  it("is nothing when nothing covers the stage", () => {
    expect(insetsOf(stage, [])).toEqual(NO_INSETS);
  });

  it("measures a bottom sheet from its top edge to the stage's bottom", () => {
    const sheet = { top: 478, left: 0, right: 390, bottom: 844 };
    expect(insetsOf(stage, [{ side: "bottom", box: sheet }])).toEqual({ top: 0, right: 0, bottom: 366, left: 0 });
  });

  it("measures a top card from the stage's top to its bottom edge, and takes the larger of two on one side", () => {
    const search = { top: 60, left: 12, right: 378, bottom: 150 };
    const results = { top: 60, left: 12, right: 378, bottom: 300 };
    expect(insetsOf(stage, [{ side: "top", box: search }, { side: "top", box: results }]).top).toBe(252);
  });

  it("measures side panels on a desktop stage", () => {
    const desk = { top: 48, left: 0, right: 1440, bottom: 900 };
    const left = { top: 60, left: 12, right: 364, bottom: 888 };
    const right = { top: 60, left: 1108, right: 1428, bottom: 888 };
    const got = insetsOf(desk, [{ side: "left", box: left }, { side: "right", box: right }]);
    expect(got.left).toBe(364);
    expect(got.right).toBe(332);
  });

  it("ignores a panel that is hidden or outside the stage", () => {
    const hidden = { top: 0, left: 0, right: 0, bottom: 0 };
    const below = { top: 900, left: 0, right: 390, bottom: 1200 };
    expect(insetsOf(stage, [{ side: "bottom", box: hidden }, { side: "bottom", box: below }])).toEqual(NO_INSETS);
  });

  it("always leaves a quarter of an axis uncovered, shrinking both sides in proportion", () => {
    const tall = { top: 100, left: 0, right: 390, bottom: 844 }; // covers 744 of 796
    const search = { top: 60, left: 12, right: 378, bottom: 150 }; // covers 102
    const got = insetsOf(stage, [{ side: "bottom", box: tall }, { side: "top", box: search }]);
    const height = stage.bottom - stage.top;
    expect(got.top + got.bottom).toBeCloseTo(height * 0.75, 6);
    expect(got.bottom / got.top).toBeCloseTo(744 / 102, 6);
  });
});

describe("aimOffset", () => {
  // A camera on +z looking at the origin, y up, 40° tall, a 800px view, 240 away.
  const forward = { x: 0, y: 0, z: -1 };
  const up = { x: 0, y: 1, z: 0 };
  const perPx = (2 * 240 * Math.tan((40 * Math.PI) / 360)) / 800;

  // Projects a world point through that camera, aimed at aim, to pixels from the view's centre (right, down).
  function screenOf(p: { x: number; y: number; z: number }, aim: { x: number; y: number; z: number }) {
    return { right: (p.x - aim.x) / perPx, down: -(p.y - aim.y) / perPx };
  }

  const at = (insets: Insets) => aimOffset(forward, up, 40, 800, 240, insets);

  it("is no offset when nothing covers the view", () => {
    const o = at(NO_INSETS);
    expect(Math.hypot(o.x, o.y, o.z)).toBe(0);
  });

  it("lifts a point above a bottom sheet into the middle of what is left", () => {
    const o = at({ top: 0, right: 0, bottom: 360, left: 0 });
    const s = screenOf({ x: 0, y: 0, z: 0 }, o);
    // The uncovered band is 0..440 from the top; its middle is 220, which is 180 above centre.
    expect(s.down).toBeCloseTo(-180, 6);
    expect(s.right).toBeCloseTo(0, 6);
  });

  it("moves a point right of a left panel and left of a right one by half the difference", () => {
    const o = at({ top: 0, right: 100, bottom: 0, left: 300 });
    const s = screenOf({ x: 0, y: 0, z: 0 }, o);
    expect(s.right).toBeCloseTo(100, 6);
    expect(s.down).toBeCloseTo(0, 6);
  });

  it("gives up rather than guess when the camera looks straight along up", () => {
    const o = aimOffset({ x: 0, y: -1, z: 0 }, up, 40, 800, 240, { top: 0, right: 0, bottom: 300, left: 0 });
    expect(Math.hypot(o.x, o.y, o.z)).toBe(0);
  });
});
