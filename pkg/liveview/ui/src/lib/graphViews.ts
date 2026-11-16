// The graph page's two views: the graph itself, and the same brain as a
// library. Both live at / and share one URL, so the view is a query parameter
// (?view=library; the graph is the bare URL). The two views read different
// parameters (the graph ?focus=, ?q=, ?type= and the rest; the library ?book=
// and ?card=), so neither needs a prefix, and each view's parameters stay in
// the URL while the other is open. The old /library path redirects here with
// every parameter as it came, so a link to a book still lands on it.

import { pageShown } from "@/lib/pages";

export type GraphViewMode = "graph" | "library";

/** The query parameter that names the view. */
export const VIEW_PARAM = "view";

/** The views this page draws, in order. The graph cannot be left out; the library can (see lib/pages). */
export function graphViews(hidden?: ReadonlySet<string>): GraphViewMode[] {
  return pageShown("/library", hidden) ? ["graph", "library"] : ["graph"];
}

/** The view the URL names, among the ones drawn; the graph when it names none of them. */
export function readView(params: URLSearchParams, views: readonly GraphViewMode[]): GraphViewMode {
  return params.get(VIEW_PARAM) === "library" && views.includes("library") ? "library" : "graph";
}

/** The URL's parameters with `view` open. The graph is the bare URL. */
export function switchView(params: URLSearchParams, view: GraphViewMode): URLSearchParams {
  const next = new URLSearchParams(params);
  if (view === "library") next.set(VIEW_PARAM, "library");
  else next.delete(VIEW_PARAM);
  return next;
}

/** Where /library?<search> goes now: the library view, every parameter as it came. */
export function legacyLibrarySearch(search: string): string {
  return `?${switchView(new URLSearchParams(search), "library").toString()}`;
}
