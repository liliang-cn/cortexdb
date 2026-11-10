import { describe, expect, it } from "vitest";
import { LANG_KEY, adoptedLang, pickLang, readSavedLang, resolveLang, saveLang } from "./i18n";
import { SHELL_TEXT } from "./shellStrings";
import { NAV_GROUPS } from "@/components/shell/nav";

describe("pickLang", () => {
  it("takes ?lang= over the browser", () => {
    expect(pickLang("zh", "en-US")).toBe("zh");
    expect(pickLang("en", "zh-CN")).toBe("en");
  });
  it("falls back to the browser, and reads any Chinese as zh", () => {
    expect(pickLang(null, "zh-TW")).toBe("zh");
    expect(pickLang("", "ZH-hans")).toBe("zh");
    expect(pickLang(undefined, "de-DE")).toBe("en");
  });
  it("is English when nothing says otherwise", () => {
    expect(pickLang(null, undefined)).toBe("en");
    expect(pickLang("fr", null)).toBe("en");
  });
});

describe("resolveLang", () => {
  it("lets ?lang= beat the saved choice and the browser", () => {
    expect(resolveLang("zh", "en", "en-US")).toBe("zh");
    expect(resolveLang("en", "zh", "zh-CN")).toBe("en");
    expect(resolveLang("zh-TW", null, "en")).toBe("zh");
  });
  it("lets the saved choice beat the browser", () => {
    expect(resolveLang(null, "zh", "en-US")).toBe("zh");
    expect(resolveLang("", "en", "zh-CN")).toBe("en");
  });
  it("falls back to the browser when nothing was chosen", () => {
    expect(resolveLang(null, null, "zh-Hans-CN")).toBe("zh");
    expect(resolveLang(undefined, undefined, "de")).toBe("en");
    expect(resolveLang(null, null, undefined)).toBe("en");
  });
});

function memoryStore(init: Record<string, string> = {}) {
  const m = new Map(Object.entries(init));
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    map: m,
  };
}

const throwing = {
  getItem(): string | null {
    throw new Error("SecurityError");
  },
  setItem(): void {
    throw new Error("QuotaExceededError");
  },
};

describe("a ?lang= link", () => {
  // The nav's links carry no ?lang=, so a link's language has to outlive the
  // page it opened, or the next click falls back to the browser's.
  it("is remembered as the viewer's choice", () => {
    expect(adoptedLang("zh", null)).toBe("zh");
    expect(adoptedLang("zh-TW", "en")).toBe("zh");
    expect(adoptedLang("en", "zh")).toBe("en");
  });

  it("asks nothing when it says what is already chosen, or nothing", () => {
    expect(adoptedLang("zh", "zh")).toBeNull();
    expect(adoptedLang(null, "zh")).toBeNull();
    expect(adoptedLang("", null)).toBeNull();
  });
});

describe("the saved choice", () => {
  it("round-trips through storage", () => {
    const s = memoryStore();
    expect(readSavedLang(s)).toBeNull();
    saveLang("zh", s);
    expect(s.map.get(LANG_KEY)).toBe("zh");
    expect(readSavedLang(s)).toBe("zh");
  });
  it("ignores a value that is not a language", () => {
    expect(readSavedLang(memoryStore({ [LANG_KEY]: "fr" }))).toBeNull();
  });
  it("falls back to the browser when storage throws", () => {
    expect(() => saveLang("zh", throwing)).not.toThrow();
    const saved = readSavedLang(throwing);
    expect(saved).toBeNull();
    expect(resolveLang(null, saved, "zh-CN")).toBe("zh");
    expect(resolveLang(null, saved, "en-GB")).toBe("en");
  });
});

describe("the shell's words", () => {
  it("names every page in the nav in both languages", () => {
    for (const item of NAV_GROUPS.flat()) {
      expect(SHELL_TEXT.en.nav[item.to]).toBeTruthy();
      expect(SHELL_TEXT.zh.nav[item.to]).toBeTruthy();
      expect(SHELL_TEXT.zh.nav[item.to]).not.toBe(SHELL_TEXT.en.nav[item.to]);
    }
  });
});
