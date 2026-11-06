import { describe, expect, it } from "vitest";
import { RECALLS_STRINGS, splitSlots, type RecallsText } from "./recalls-strings";

// The page speaks both languages in full: the same keys, nothing left blank,
// and the same set-apart pieces in each sentence that has them.

const sample = (v: unknown): string[] => {
  if (typeof v === "string") return [v];
  if (typeof v === "function") {
    const f = v as (...a: unknown[]) => unknown;
    return [String(f(1, "memory")), String(f(3, "knowledge")), String(f(0, "chunk"))];
  }
  if (v && typeof v === "object") return Object.values(v).flatMap(sample);
  return [];
};

describe("the recalls page's strings", () => {
  const en = RECALLS_STRINGS.en;
  const zh = RECALLS_STRINGS.zh;

  it("have the same entries in both languages", () => {
    expect(Object.keys(zh).sort()).toEqual(Object.keys(en).sort());
    expect(Object.keys(zh.degradedWhy).sort()).toEqual(Object.keys(en.degradedWhy).sort());
  });

  it("leave nothing blank", () => {
    for (const lang of [en, zh]) {
      for (const [key, value] of Object.entries(lang) as Array<[keyof RecallsText, unknown]>) {
        // marks(0, 0) is empty on purpose: a recall with no marks says nothing.
        if (key === "marks") continue;
        for (const s of sample(value)) expect(s.trim(), `${key}`).not.toBe("");
      }
    }
  });

  it("say how to turn the log on, with the variables set apart, in both", () => {
    for (const lang of [en, zh]) {
      const slots = splitSlots(lang.offHow).filter((p) => typeof p !== "string");
      expect(slots).toEqual([{ slot: "on" }, { slot: "days" }]);
    }
  });

  it("name the degraded reasons the server reports", () => {
    for (const reason of ["embedder_unreachable", "vector_search_failed", "keyword_search_failed"]) {
      expect(en.degradedWhy[reason]).toBeTruthy();
      expect(zh.degradedWhy[reason]).toBeTruthy();
    }
  });

  it("count in each language's way", () => {
    expect(en.hits(1)).toBe("1 hit");
    expect(en.hits(2)).toBe("2 hits");
    expect(zh.hits(2)).toBe("2 条命中");
    expect(en.newRecalls(1)).toBe("1 new recall");
    expect(en.marks(1, 1)).toBe("1 useful, 1 noise");
    expect(en.marks(0, 0)).toBe("");
  });
});

describe("splitSlots", () => {
  it("cuts a sentence at its slots", () => {
    expect(splitSlots("Start with {on}. Kept {days} days.")).toEqual(["Start with ", { slot: "on" }, ". Kept ", { slot: "days" }, " days."]);
  });
  it("leaves a sentence without slots whole", () => {
    expect(splitSlots("plain")).toEqual(["plain"]);
    expect(splitSlots("{on}")).toEqual([{ slot: "on" }]);
  });
});
