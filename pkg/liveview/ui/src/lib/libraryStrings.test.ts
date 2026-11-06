import { describe, expect, it } from "vitest";
import { LIBRARY_TEXT } from "./libraryStrings";

describe("the library's two languages", () => {
  const { en, zh } = LIBRARY_TEXT;

  it("say the same things", () => {
    expect(Object.keys(zh).sort()).toEqual(Object.keys(en).sort());
    for (const k of Object.keys(en) as Array<keyof typeof en>) expect(typeof zh[k], k).toBe(typeof en[k]);
  });

  it("leave nothing blank", () => {
    for (const lang of [en, zh]) {
      for (const [k, v] of Object.entries(lang)) {
        const s = typeof v === "function" ? (v as (...a: unknown[]) => string)(3, 7) : v;
        expect(typeof s, k).toBe("string");
        expect((s as string).trim().length, k).toBeGreaterThan(0);
      }
    }
  });

  it("put the numbers they are given into the sentence", () => {
    for (const t of [en, zh]) {
      expect(t.wingSub(4, 51)).toMatch(/4.*51/);
      expect(t.page(2, 9)).toMatch(/2.*9/);
      expect(t.shared(5)).toContain("5");
      expect(t.cardBooks(12)).toContain("12");
      expect(t.more(30)).toContain("30");
      expect(t.failed("timeout")).toContain("timeout");
      expect(t.document(3)).toContain("3");
    }
    expect(en.cardBooks(1)).toBe("1 book mentions it");
  });

  it("speak Chinese in the Chinese copy", () => {
    expect(zh.title).toBe("记忆图书馆");
    expect(zh.close).toBe("合上，放回书架");
  });
});
