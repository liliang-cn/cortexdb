// Two pieces of layout the Versions page needs from the DOM: whether the
// comparison has room beside the list, and keeping the reader's place when a
// new version is put in above it.

import { useSyncExternalStore } from "react";

/** Tailwind's lg: from here the comparison sits beside the list; below it, it opens as a sheet. */
export const WIDE = "(min-width: 1024px)";

export function isWide(): boolean {
  return typeof window !== "undefined" && window.matchMedia(WIDE).matches;
}

export function useWide(): boolean {
  return useSyncExternalStore(
    (cb) => {
      const m = window.matchMedia(WIDE);
      m.addEventListener("change", cb);
      return () => m.removeEventListener("change", cb);
    },
    isWide,
    () => true,
  );
}

/** Where the reader was: a row, and how far its top sat below the top of the scroller. */
export interface ScrollAnchor {
  scroller: HTMLElement;
  key: string;
  offset: number;
}

function scrollerOf(el: HTMLElement): HTMLElement | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const y = getComputedStyle(p).overflowY;
    if (y === "auto" || y === "scroll") return p;
  }
  return (document.scrollingElement as HTMLElement | null) ?? null;
}

function topOf(scroller: HTMLElement): number {
  return scroller === document.scrollingElement ? 0 : scroller.getBoundingClientRect().top;
}

/**
 * The first row of the list still in view, taken just before a re-read is
 * applied. Null while the top of the list is itself in view: then a new
 * version should push in where the reader can see it arrive.
 *
 * Done by hand rather than left to the browser's scroll anchoring, which not
 * every engine has, and which would otherwise act on top of this; the page
 * turns it off with overflow-anchor: none.
 */
export function captureAnchor(list: HTMLElement | null): ScrollAnchor | null {
  if (!list) return null;
  const scroller = scrollerOf(list);
  if (!scroller) return null;
  const top = topOf(scroller);
  if (list.getBoundingClientRect().top >= top) return null;
  for (const el of list.querySelectorAll<HTMLElement>("[data-version]")) {
    const r = el.getBoundingClientRect();
    if (r.bottom > top) return { scroller, key: el.dataset.version ?? "", offset: r.top - top };
  }
  return null;
}

/** Scroll so the anchored row sits where it was, whatever was put in above it. */
export function restoreAnchor(list: HTMLElement | null, anchor: ScrollAnchor) {
  const el = list?.querySelector<HTMLElement>(`[data-version="${CSS.escape(anchor.key)}"]`);
  if (!el) return;
  const delta = el.getBoundingClientRect().top - topOf(anchor.scroller) - anchor.offset;
  if (Math.abs(delta) >= 1) anchor.scroller.scrollTop += delta;
}
