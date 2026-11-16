import { describe, expect, it } from "vitest";
import { FINDING_TEXT, ONTOLOGY_TEXT } from "./ontologyStrings";

describe("the ontology page's strings", () => {
  it("are in both languages, and none of them is a sentence", () => {
    for (const table of [ONTOLOGY_TEXT, FINDING_TEXT]) {
      expect(Object.keys(table.zh).sort()).toEqual(Object.keys(table.en).sort());
      for (const lang of ["en", "zh"] as const) {
        for (const [k, v] of Object.entries(table[lang])) {
          const text = typeof v === "function" ? String((v as (...a: unknown[]) => string)("2", "3", "4", "5")) : String(v);
          expect(text, `${lang}.${k}`).not.toBe("");
          expect(text, `${lang}.${k}`).not.toMatch(/[.。]$/);
        }
      }
    }
  });
});
