import { describe, expect, it } from "vitest";
import { ANALYSIS_TEXT, type AnalysisText } from "./analysisStrings";

// The page speaks both languages in full: the same entries, nothing blank.

const sample = (v: unknown): string[] => {
  if (typeof v === "string") return [v];
  if (typeof v === "function") {
    const f = v as (...a: unknown[]) => unknown;
    // Text stands in for every argument: a count is written into the sentence either way.
    return [String(f("1", "a", "b", "c", "d")), String(f("3", "a", "b", "c", "d"))];
  }
  if (v && typeof v === "object") return Object.values(v).flatMap(sample);
  return [];
};

const shape = (v: unknown): unknown => (v && typeof v === "object" ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, shape(x)]).sort()) : typeof v);

describe("the analysis page's strings", () => {
  it("have the same entries in both languages, nested ones included", () => {
    expect(shape(ANALYSIS_TEXT.zh)).toEqual(shape(ANALYSIS_TEXT.en));
  });

  it("leave nothing blank", () => {
    for (const lang of [ANALYSIS_TEXT.en, ANALYSIS_TEXT.zh]) {
      for (const [key, value] of Object.entries(lang) as Array<[keyof AnalysisText, unknown]>) {
        for (const s of sample(value)) expect(s.trim(), key).not.toBe("");
      }
    }
  });

  it("say that memories are outside the builder, in both", () => {
    expect(ANALYSIS_TEXT.en.exploreScope).toContain("Memories");
    expect(ANALYSIS_TEXT.zh.exploreScope).toContain("记忆");
  });
});
