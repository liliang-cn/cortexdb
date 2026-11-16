// The ontology page's two tabs: the declared model drawn, and its saved
// versions. Both live at /ontology and share one URL, so the tab is a query
// parameter (?tab=versions; the ontology tab is the bare URL) and the versions
// tab keeps its own parameters under a prefix (v_schema, v_from, v_to): both
// tabs name a schema, and they do not always name the same one. Each tab's
// parameters stay in the URL while the other is open, so switching back finds
// it as it was. The old /ontology-history path redirects here with its
// parameters moved under the prefix, so a link to a diff still lands on it.

import { pageShown } from "@/lib/pages";

export type OntologyTab = "ontology" | "versions";

/** The query parameter that names the tab. */
export const TAB_PARAM = "tab";

/** The prefix of the versions tab's own query parameters. */
export const VERSIONS_PREFIX = "v_";

/** The tabs this view draws, in order: a host can leave either page out (see lib/pages). */
export function ontologyTabs(hidden?: ReadonlySet<string>): OntologyTab[] {
  const tabs: OntologyTab[] = [];
  if (pageShown("/ontology", hidden)) tabs.push("ontology");
  if (pageShown("/ontology-history", hidden)) tabs.push("versions");
  return tabs;
}

/** The tab the URL names, among the ones drawn; the first one drawn when it names none of them. */
export function readTab(params: URLSearchParams, tabs: readonly OntologyTab[]): OntologyTab {
  const named = params.get(TAB_PARAM);
  const want: OntologyTab = named === "versions" ? "versions" : "ontology";
  return tabs.includes(want) ? want : (tabs[0] ?? "ontology");
}

/** The parameters under `prefix`, with the prefix taken off; every other parameter is left out. */
export function scopeParams(params: URLSearchParams, prefix: string): URLSearchParams {
  const out = new URLSearchParams();
  for (const [k, v] of params) if (k.startsWith(prefix)) out.append(k.slice(prefix.length), v);
  return out;
}

/** `base` with its parameters under `prefix` replaced by `scoped`, each put back under the prefix. */
export function unscopeParams(scoped: URLSearchParams, prefix: string, base: URLSearchParams): URLSearchParams {
  const out = new URLSearchParams();
  for (const [k, v] of base) if (!k.startsWith(prefix)) out.append(k, v);
  for (const [k, v] of scoped) out.append(prefix + k, v);
  return out;
}

/**
 * The URL's parameters with `tab` open. The ontology tab is the bare URL.
 * Opening the versions tab on `schema` (the schema the ontology tab draws, or
 * the saved schema a draft was held against) moves the versions tab to that
 * schema, and the picks of another schema go with the move.
 */
export function switchTab(params: URLSearchParams, tab: OntologyTab, schema = ""): URLSearchParams {
  const next = new URLSearchParams(params);
  if (tab === "ontology") {
    next.delete(TAB_PARAM);
    return next;
  }
  next.set(TAB_PARAM, "versions");
  const key = VERSIONS_PREFIX + "schema";
  if (schema && next.get(key) !== schema) {
    next.set(key, schema);
    next.delete(VERSIONS_PREFIX + "from");
    next.delete(VERSIONS_PREFIX + "to");
  }
  return next;
}

/** The versions page's own parameters, as /ontology-history spelled them. */
const VERSIONS_KEYS = new Set(["schema", "from", "to"]);

/**
 * Where /ontology-history?<search> goes now: the versions tab, the old page's
 * own parameters under the prefix, and every other one (?lang=, ?mode=,
 * ?as_of=, which the whole app reads) as it came.
 */
export function legacyVersionsSearch(search: string): string {
  const next = new URLSearchParams();
  next.set(TAB_PARAM, "versions");
  for (const [k, v] of new URLSearchParams(search)) next.append(VERSIONS_KEYS.has(k) ? VERSIONS_PREFIX + k : k, v);
  return `?${next.toString()}`;
}
