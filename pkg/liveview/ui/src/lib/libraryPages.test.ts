import { describe, expect, it } from "vitest";
import type { RecordDetail } from "@/api";
import { FIRST_PAGE_LINES, PAGE_LINES, bookText, clip, metaLine, paginate, provenance, wrapText } from "./libraryPages";

// One unit per character, so widths in a test are counts.
const len = (s: string) => Array.from(s).length;

const record = (over: Partial<RecordDetail> = {}): RecordDetail => ({ available: true, found: true, id: "memory:a", ...over });
const words = { source: "source", producer: "producer", written: "written", grade: "grade", scope: "scope" };

describe("wrapping a book's text", () => {
  it("breaks between words and drops the space it broke at", () => {
    expect(wrapText("the quick brown fox jumps", 10, len)).toEqual(["the quick", "brown fox", "jumps"]);
  });

  it("keeps paragraphs and blank lines", () => {
    expect(wrapText("one\n\ntwo three", 20, len)).toEqual(["one", "", "two three"]);
  });

  it("breaks Chinese between any two characters", () => {
    expect(wrapText("家里的虚拟机都加进了网络", 5, len)).toEqual(["家里的虚拟", "机都加进了", "网络"]);
  });

  it("cuts a word wider than a whole line", () => {
    const lines = wrapText("see example.invalid/a/very/long/path/x now", 12, len);
    expect(lines).toEqual(["see", "example.inva", "lid/a/very/l", "ong/path/x", "now"]);
    for (const l of lines) expect(len(l)).toBeLessThanOrEqual(12);
  });

  it("gives an empty text one empty line", () => {
    expect(wrapText("", 10, len)).toEqual([""]);
  });
});

describe("paging", () => {
  it("puts fewer lines on the first page, under the title", () => {
    const lines = Array.from({ length: FIRST_PAGE_LINES + PAGE_LINES + 3 }, (_, i) => `line ${i}`);
    const pages = paginate(lines);
    expect(pages.map((p) => p.length)).toEqual([FIRST_PAGE_LINES, PAGE_LINES, 3]);
    expect(pages.flat()).toEqual(lines);
  });

  it("always has a page to open to", () => {
    expect(paginate([])).toEqual([[]]);
  });
});

describe("what a book opens to", () => {
  it("is the record's text, or the memory's", () => {
    expect(bookText({ label: "t", record: record({ content: "body" }) }, "none")).toBe("body");
    expect(bookText({ label: "t", record: record({ memory: { id: "a", content: "saved" } }) }, "none")).toBe("saved");
  });

  it("says so when the source cannot open books, and falls back on the title otherwise", () => {
    expect(bookText({ label: "t", record: { available: false, found: false, id: "x" } }, "cannot")).toBe("cannot");
    expect(bookText({ label: "t", record: record() }, "cannot")).toBe("t");
  });
});

describe("a book's provenance", () => {
  it("lists what the record carries, with the memory's creation time when there is no _at", () => {
    const r = record({ source: "chat", producer: "claude", grade: "verified", memory: { id: "a", content: "", scope: "user", user_id: "leo", created_at: "2026-11-06T09:07:57Z" } });
    expect(provenance(r, words)).toEqual([
      ["source", "chat"],
      ["producer", "claude"],
      ["written", "2026-11-06T09:07:57Z"],
      ["grade", "verified"],
      ["scope", "user · leo"],
    ]);
    expect(provenance(undefined, words)).toEqual([]);
  });

  it("puts the date, grade and kind under the title", () => {
    expect(metaLine(record({ at: "2026-01-02T03:04:05Z", grade: "held", type: "memory" }), words)).toBe("written 2026-01-02 · grade held · memory");
    expect(metaLine(record(), words)).toBe("");
  });
});

describe("clipping", () => {
  it("counts characters, not UTF-16 units", () => {
    expect(clip("abcdef", 4)).toBe("abc…");
    expect(clip("abc", 4)).toBe("abc");
    expect(clip("记忆图书馆", 3)).toBe("记忆…");
  });
});
