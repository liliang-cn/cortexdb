import { describe, expect, it } from "vitest";
import { GRAPH_TEXT, type PathNote } from "./graphStrings";

describe("GRAPH_TEXT", () => {
  it("has every string in both languages, none of them empty", () => {
    expect(Object.keys(GRAPH_TEXT.zh).sort()).toEqual(Object.keys(GRAPH_TEXT.en).sort());
    for (const lang of ["en", "zh"] as const) {
      for (const [k, v] of Object.entries(GRAPH_TEXT[lang])) {
        if (typeof v === "string") expect(v, `${lang}.${k}`).not.toBe("");
      }
    }
  });

  it("speaks a traced path in the page's language", () => {
    const note: PathNote = { kind: "path", hops: 2, names: ["a", "b", "c"] };
    expect(GRAPH_TEXT.en.path(note)).toBe("2 hops: a → b → c");
    expect(GRAPH_TEXT.zh.path(note)).toBe("2 跳：a → b → c");
    expect(GRAPH_TEXT.en.path({ kind: "none" })).toBe("");
  });
});
