import { describe, expect, it } from "vitest";
import { initialAsOf } from "./store";

describe("initialAsOf", () => {
  it("reads RFC 3339, with a plus the URL turned into a space", () => {
    expect(initialAsOf("?as_of=2026-01-02T03:04:05+08:00")).toBe(Date.parse("2026-01-02T03:04:05+08:00"));
    expect(initialAsOf("?as_of=2026-01-02T03:04:05 08:00")).toBe(Date.parse("2026-01-02T03:04:05+08:00"));
  });
  it("reads unix milliseconds", () => {
    expect(initialAsOf("?as_of=1767323045000")).toBe(1767323045000);
  });
  it("is null for nothing or nonsense, so the page stays live", () => {
    expect(initialAsOf("")).toBeNull();
    expect(initialAsOf("?as_of=yesterday-ish")).toBeNull();
  });
});
