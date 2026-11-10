import { describe, expect, it } from "vitest";
import { endWords } from "./ontologyHistory";
import { partIn, VERSIONS_TEXT } from "./versionsStrings";

describe("the versions page's words", () => {
  it("has every end reason in both languages", () => {
    expect(Object.keys(VERSIONS_TEXT.zh.endWords).sort()).toEqual(Object.keys(VERSIONS_TEXT.en.endWords).sort());
    expect(endWords("switched", "zh")).toBe("启用了另一个模式");
    expect(endWords("some_new_reason", "zh")).toBe("some new reason");
  });

  it("names a diff's part in Chinese and leaves one it does not know as it came", () => {
    expect(partIn("zh", "required property")).toBe("必填属性");
    expect(partIn("en", "required property")).toBe("required property");
    expect(partIn("zh", "action type")).toBe("action type");
  });
});
