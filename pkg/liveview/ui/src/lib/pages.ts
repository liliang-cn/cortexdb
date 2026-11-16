// The pages a host left out of this view.
//
// A host that mounts the view as one page of its own product can leave pages
// out (liveview.WithoutPages). The server writes them into the index beside
// the base tag, as <meta name="cortexdb-hidden-pages" content="recalls
// ontology">, so they are known before the first paint and fixed for the life
// of the page. A page left out is absent rather than empty: not in the
// navigation or the palette, not in the router, and no page that stays draws a
// link to it. The graph and the entity page cannot be left out.

/** The pages a host can leave out, by their path under the view's root. */
export type OptionalPage = "changes" | "activity" | "recalls" | "library" | "ontology" | "ontology-history" | "analysis";

const META = 'meta[name="cortexdb-hidden-pages"]';

/** The page names in the meta tag's content: space separated, in any order. */
export function parseHiddenPages(content: string | null | undefined): ReadonlySet<string> {
  return new Set((content ?? "").split(/\s+/).filter(Boolean));
}

let cached: ReadonlySet<string> | null = null;

/** The pages this view leaves out. Empty outside a browser, and under the dev server, which writes no tag. */
export function hiddenPages(): ReadonlySet<string> {
  if (cached) return cached;
  const content = typeof document === "undefined" ? null : document.querySelector(META)?.getAttribute("content");
  cached = parseHiddenPages(content);
  return cached;
}

/** The page an app path names: "/recalls?trace=x" is "recalls", "/" is "". */
export function pageOf(path: string): string {
  return path.replace(/^\/+/, "").split(/[/?#]/, 1)[0] ?? "";
}

/** Whether the page at this app path is part of this view. */
export function pageShown(path: string, hidden: ReadonlySet<string> = hiddenPages()): boolean {
  return !hidden.has(pageOf(path));
}

/**
 * Pages drawn as tabs of another page's route: the versions page is a tab of
 * /ontology, and its own path only redirects there. A route is shown while
 * any page drawn on it is. (The library is a view of the graph at /, which
 * cannot be left out; /library only redirects there, and goes with the library.)
 */
const ROUTE_PAGES: Readonly<Record<string, readonly string[]>> = {
  ontology: ["ontology", "ontology-history"],
};

/** Whether the app route at this path is part of this view: the router and the navigation ask this, the pages' links ask pageShown. */
export function routeShown(path: string, hidden: ReadonlySet<string> = hiddenPages()): boolean {
  const pages = ROUTE_PAGES[pageOf(path)];
  return pages ? pages.some((p) => !hidden.has(p)) : pageShown(path, hidden);
}
