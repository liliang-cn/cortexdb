import { describe, expect, it } from "vitest";
import { RULES_TEXT } from "./rulesStrings";

describe("the standing rules' strings", () => {
  it("are in both languages, and none of them is a sentence", () => {
    expect(Object.keys(RULES_TEXT.zh).sort()).toEqual(Object.keys(RULES_TEXT.en).sort());
    for (const lang of ["en", "zh"] as const) {
      for (const [k, v] of Object.entries(RULES_TEXT[lang])) {
        const text = typeof v === "function" ? String((v as (...a: unknown[]) => string)("2", "3")) : String(v);
        expect(text, `${lang}.${k}`).not.toBe("");
        expect(text, `${lang}.${k}`).not.toMatch(/[.。!！?？]$/);
        // A label, not prose: no clause punctuation and only a few words.
        expect(text, `${lang}.${k}`).not.toMatch(/[,;:，；：]/);
        expect(text.split(/\s+/).length, `${lang}.${k}`).toBeLessThanOrEqual(4);
      }
    }
  });
});
