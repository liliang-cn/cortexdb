import { describe, expect, it } from "vitest";
import { FEED_TEXT } from "./feedStrings";

describe("feed strings", () => {
  it("knows the same reason codes, in-effect kinds and opinions in both languages", () => {
    for (const k of ["reasons", "ieTitles", "opinions"] as const) {
      expect(Object.keys(FEED_TEXT.zh[k]).sort()).toEqual(Object.keys(FEED_TEXT.en[k]).sort());
    }
  });
  it("counts in both languages", () => {
    expect(FEED_TEXT.en.newChanges(1)).toBe("1 new change");
    expect(FEED_TEXT.en.newChanges(3)).toBe("3 new changes");
    expect(FEED_TEXT.zh.newChanges(3)).toBe("3 条新变更");
  });
  it("keeps the slots the Activity page cuts its sentences at", () => {
    for (const t of [FEED_TEXT.en, FEED_TEXT.zh]) {
      expect(t.noCallsViewBody.split("{tool}")).toHaveLength(2);
      expect(t.changesStillLists.split("{changes}")).toHaveLength(2);
    }
  });
});
