import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { hoverIntent } from "./hover";

const OPTS = { enterMs: 100, leaveMs: 200 };

function setup() {
  let current: string | null = null;
  const applied: (string | null)[] = [];
  const intent = hoverIntent<string>(
    (v) => {
      current = v;
      applied.push(v);
    },
    () => current,
    OPTS,
  );
  return { intent, applied, current: () => current };
}

describe("hoverIntent", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("takes a value only once the pointer has rested on it", () => {
    const { intent, applied } = setup();
    intent.point("a");
    vi.advanceTimersByTime(99);
    expect(applied).toEqual([]);
    vi.advanceTimersByTime(1);
    expect(applied).toEqual(["a"]);
  });

  it("takes nothing from a pointer sweeping across many values", () => {
    const { intent, applied } = setup();
    for (const id of ["a", null, "b", null, "c", "d", null, "e"]) {
      intent.point(id);
      vi.advanceTimersByTime(40);
    }
    expect(applied).toEqual([]);
    vi.advanceTimersByTime(100);
    expect(applied).toEqual(["e"]);
  });

  it("passes from one value to the next without letting go in between", () => {
    const { intent, applied } = setup();
    intent.point("a");
    vi.advanceTimersByTime(100);
    intent.point(null);
    vi.advanceTimersByTime(150);
    intent.point("b");
    vi.advanceTimersByTime(300);
    expect(applied).toEqual(["a", "b"]);
  });

  it("keeps a value the pointer leaves and comes back to", () => {
    const { intent, applied } = setup();
    intent.point("a");
    vi.advanceTimersByTime(100);
    intent.point(null);
    vi.advanceTimersByTime(150);
    intent.point("a");
    vi.advanceTimersByTime(500);
    expect(applied).toEqual(["a"]);
  });

  it("lets go once the pointer has been off for longer", () => {
    const { intent, applied, current } = setup();
    intent.point("a");
    vi.advanceTimersByTime(100);
    intent.point(null);
    vi.advanceTimersByTime(199);
    expect(current()).toBe("a");
    vi.advanceTimersByTime(1);
    expect(applied).toEqual(["a", null]);
  });

  it("commits at once and cancels what was pending", () => {
    const { intent, applied } = setup();
    intent.point("a");
    intent.commit("b");
    vi.advanceTimersByTime(500);
    expect(applied).toEqual(["b"]);
    intent.point("c");
    intent.cancel();
    vi.advanceTimersByTime(500);
    expect(applied).toEqual(["b"]);
  });
});
