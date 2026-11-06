import { describe, expect, it } from "vitest";
import { pickLang } from "./i18n";

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
