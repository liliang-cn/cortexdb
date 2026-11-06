// An open book's pages: its text wrapped and paged for the lectern, and the
// lines of its provenance. Pure, with the measuring passed in, so the lectern
// can measure with a canvas and a test can measure with string length.

import type { RecordDetail } from "@/api";
import type { LibraryBookPage } from "@/api/library";

/** Lines on the first page, under the title, and on every page after it. */
export const FIRST_PAGE_LINES = 21;
export const PAGE_LINES = 26;

// A CJK character is a word of its own: Chinese has no spaces to break at, and
// a line of it may break between any two characters.
const CJK = "⺀-鿿豈-﫿＀-￯　-〿";
const TOKENS = new RegExp(`[${CJK}]|[^\\s${CJK}]+|\\s+`, "g");

/**
 * Wraps text to lines no wider than maxWidth by measure, keeping paragraphs:
 * every newline starts a new line, and a blank line stays blank. A word wider
 * than a whole line is cut where it has to be, so a long URL cannot run off
 * the page.
 */
export function wrapText(text: string, maxWidth: number, measure: (s: string) => number): string[] {
  const out: string[] = [];
  const push = (line: string) => out.push(line.replace(/\s+$/, ""));
  for (const para of String(text ?? "").split("\n")) {
    let line = "";
    for (const tok of para.match(TOKENS) ?? [""]) {
      if (line.trim() && measure(line + tok) > maxWidth) {
        push(line);
        // A space that would have ended the line is dropped with the break.
        line = /^\s+$/.test(tok) ? "" : tok;
      } else {
        line += tok;
      }
      // A word wider than the line is cut into pieces that do fit.
      let chars = Array.from(line);
      while (chars.length > 1 && measure(line) > maxWidth) {
        let cut = chars.length - 1;
        while (cut > 1 && measure(chars.slice(0, cut).join("")) > maxWidth) cut--;
        push(chars.slice(0, cut).join(""));
        chars = chars.slice(cut);
        line = chars.join("");
      }
    }
    push(line);
  }
  return out;
}

/** Cuts lines into pages: fewer on the first, under the title. Always at least one page. */
export function paginate(lines: readonly string[], first = FIRST_PAGE_LINES, rest = PAGE_LINES): string[][] {
  const pages: string[][] = [lines.slice(0, first)];
  for (let i = first; i < lines.length; i += rest) pages.push(lines.slice(i, i + rest));
  return pages;
}

/** The text a book opens to: what the source holds, or why it holds nothing, or at least its title. */
export function bookText(book: Pick<LibraryBookPage, "label" | "record">, noRecords: string): string {
  const r: Partial<RecordDetail> = book.record ?? {};
  const text = r.content || r.memory?.content;
  if (text) return text;
  if (r.available === false) return noRecords;
  return book.label;
}

/** A string at most n characters long, ending in an ellipsis when it was cut. */
export function clip(s: string, n: number): string {
  const chars = Array.from(s ?? "");
  return chars.length > n ? `${chars.slice(0, n - 1).join("")}…` : chars.join("");
}

/** When a record was written: its contract _at, or the memory's own creation time. */
export function writtenAt(r: Partial<RecordDetail> | undefined): string {
  return r?.at || r?.memory?.created_at || "";
}

export interface ProvenanceText {
  source: string;
  producer: string;
  written: string;
  grade: string;
  scope: string;
}

/** The provenance lines a book's back page and panel list, those the record carries. */
export function provenance(r: Partial<RecordDetail> | undefined, text: ProvenanceText): Array<[string, string]> {
  const out: Array<[string, string]> = [];
  if (!r) return out;
  if (r.source) out.push([text.source, r.source]);
  if (r.producer) out.push([text.producer, r.producer]);
  const at = writtenAt(r);
  if (at) out.push([text.written, at]);
  if (r.grade) out.push([text.grade, r.grade]);
  const scope = r.memory?.scope;
  if (scope) out.push([text.scope, r.memory?.user_id ? `${scope} · ${r.memory.user_id}` : scope]);
  return out;
}

/** The line under an open book's title: when it was written, its grade and its kind. */
export function metaLine(r: Partial<RecordDetail> | undefined, text: Pick<ProvenanceText, "written" | "grade">): string {
  if (!r) return "";
  const at = writtenAt(r);
  return [at ? `${text.written} ${at.slice(0, 10)}` : "", r.grade ? `${text.grade} ${r.grade}` : "", r.type ?? ""].filter(Boolean).join(" · ");
}
