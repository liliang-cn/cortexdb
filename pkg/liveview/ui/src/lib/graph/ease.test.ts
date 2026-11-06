import { describe, expect, it } from "vitest";
import { EMPHASIS_TAU_MS, approach } from "./ease";

describe("approach", () => {
  it("moves part of the way in one frame, not all of it", () => {
    const v = approach(0, 1, 16);
    expect(v).toBeGreaterThan(0.15);
    expect(v).toBeLessThan(0.35);
  });

  it("settles exactly on the target within a quarter of a second at 60 fps", () => {
    let v = 0;
    let frames = 0;
    while (v !== 1 && frames < 100) {
      v = approach(v, 1, 1000 / 60);
      frames++;
    }
    expect(v).toBe(1);
    expect(frames * (1000 / 60)).toBeLessThanOrEqual(400);
    expect(frames * (1000 / 60)).toBeGreaterThan(3 * EMPHASIS_TAU_MS);
  });

  it("does not depend on the frame rate", () => {
    let fast = 0;
    for (let i = 0; i < 12; i++) fast = approach(fast, 1, 1000 / 120);
    const slow = approach(approach(approach(0, 1, 1000 / 30), 1, 1000 / 30), 1, 1000 / 30);
    expect(Math.abs(fast - slow)).toBeLessThan(0.01);
  });

  it("goes back the same way", () => {
    expect(approach(1, 0, 16)).toBeCloseTo(1 - approach(0, 1, 16), 10);
  });

  it("jumps when motion is reduced", () => {
    expect(approach(0, 1, 16, 0)).toBe(1);
  });
});
