/**
 * How often the ontology page reads the ontology again: liveview.OntologyInterval,
 * which a Go test holds this to. A schema moves when somebody redesigns the
 * model, so half a minute is already far faster than the thing being watched.
 */
export const ONTOLOGY_MS = 30000;

// The page's options, read from the URL and written back to it.
//
// Every switch the page offers is also a query parameter, because an embedder
// cannot press a button — the rule the graph page follows, and the one this
// page kept when it was a page of its own:
//
//   ?gap=0       the declarations alone. Nothing is read for the overlay: this
//                switch changes the server's work, not just the paint, so an
//                embedder that only wants the model costs the store nothing
//                for the half it is not showing
//   ?strays=0    no band of undeclared types. On a store with no schema that
//                band is the entire answer, which is the point; an embedder
//                showing the model alone turns it off
//   ?panels=0    the diagram alone: no controls, no key, no detail panel. The
//                finding above the diagram and the band below it stay, because
//                they are the answer rather than chrome
//   ?schema=ID   which saved schema to draw, when more than one is saved
//   ?bg=RRGGBB   the page's background, for a page shown through a frame
//
// And the draft's own, which change the verb rather than the paint:
//
//   ?draft=1     derive an ontology from what the store holds and draw that,
//                through the same lanes and the same overlay. Nothing is saved
//                and nothing here can save: the page reviews, a person saves,
//                through ontology_save. A flag rather than ?schema=draft,
//                because "draft" is a legal schema id (the deriver's own
//                default) and a store that had saved its draft could then
//                never be shown it again
//   ?min=N       keep node and edge types with fewer records than N out of the
//                drawing. The server defaults to 3, because a real brain's
//                unthresholded draft is more than a person reviews; what the
//                threshold kept out is counted in the finding
//   ?min_nodes=N
//   ?min_edges=N one side of that threshold on its own

import type { Params } from "@/api/client";
import type { ChangeItem } from "@/api";
import { DRAFT_STATES, ONTOLOGY_STATES, type OntologyAnswer } from "@/api/ontology";

export interface OntologyOptions {
  gap: boolean;
  strays: boolean;
  panels: boolean;
  schema: string;
  draft: boolean;
  /**
   * The thresholds exactly as given, "" when absent. Zero has to be
   * expressible, since it is how a reader asks for the whole vocabulary, so
   * "absent" and "0" are not the same value and these stay strings.
   */
  min: string;
  minNodes: string;
  minEdges: string;
  /** "#rrggbb", or null for the theme's own background. */
  bg: string | null;
}

export const DEFAULT_OPTIONS: OntologyOptions = {
  gap: true,
  strays: true,
  panels: true,
  schema: "",
  draft: false,
  min: "",
  minNodes: "",
  minEdges: "",
  bg: null,
};

const threshold = (raw: string | null) => (raw === null ? "" : raw.trim());

export function readOntologyOptions(params: URLSearchParams): OntologyOptions {
  const bg = params.get("bg") ?? "";
  return {
    gap: params.get("gap") !== "0",
    strays: params.get("strays") !== "0",
    panels: params.get("panels") !== "0",
    schema: (params.get("schema") ?? "").trim(),
    draft: params.get("draft") === "1",
    min: threshold(params.get("min")),
    minNodes: threshold(params.get("min_nodes")),
    minEdges: threshold(params.get("min_edges")),
    bg: /^[0-9a-f]{6}$/i.test(bg) ? `#${bg.toLowerCase()}` : null,
  };
}

/**
 * The options written back into the URL, keeping whatever else is there (the
 * theme override, a language). Only what differs from the default is written,
 * so a page with every switch at rest has a bare URL.
 */
export function writeOntologyOptions(opts: OntologyOptions, base?: URLSearchParams): URLSearchParams {
  const p = new URLSearchParams(base);
  const set = (key: string, value: string | null) => (value ? p.set(key, value) : p.delete(key));
  set("gap", opts.gap ? null : "0");
  set("strays", opts.strays ? null : "0");
  set("panels", opts.panels ? null : "0");
  set("schema", opts.schema || null);
  set("draft", opts.draft ? "1" : null);
  set("min", opts.min || null);
  set("min_nodes", opts.minNodes || null);
  set("min_edges", opts.minEdges || null);
  set("bg", opts.bg ? opts.bg.slice(1) : null);
  return p;
}

/** Which parts of the page are drawn. */
export interface PageParts {
  /** The title, the schema's name and pills, and the link to its versions. */
  header: boolean;
  /** The switches, the threshold, the schema picker and Refresh. */
  controls: boolean;
  key: boolean;
  /** The panel that opens on a click. */
  detail: boolean;
  /** The sentence above the diagram. */
  finding: boolean;
  diagram: boolean;
  /** The band of types nothing declares. */
  outside: boolean;
  /** A draft's decisions and its diff against a saved schema. */
  review: boolean;
}

/**
 * ?panels=0 means "the diagram alone" here, the way it means "the graph
 * alone" on the graph page. It takes away chrome an embedder can do without:
 * the page's own controls, the key and the detail panel. The finding above
 * the diagram, the diagram, the band below it and a draft's review half are
 * not chrome; they are the answer, and a bare page that dropped them would be
 * a blank page.
 */
export function pageParts(opts: OntologyOptions): PageParts {
  return {
    header: opts.panels,
    controls: opts.panels,
    key: opts.panels,
    detail: opts.panels,
    finding: true,
    diagram: true,
    outside: opts.strays,
    review: true,
  };
}

/**
 * What api/ontology is asked, from the options. Only the options that change
 * the server's work go to it; the rest are paint.
 *
 * gap has nothing to switch off on a draft: deriving one reads the type counts
 * as part of deriving it, so the overlay is already paid for and ?gap=0 would
 * buy nothing. The thresholds mean nothing without a draft, so they are only
 * sent with one.
 */
export function ontologyQuery(opts: OntologyOptions): Params {
  const q: Params = {};
  if (opts.draft) {
    q.draft = "1";
    if (opts.min !== "") q.min = opts.min;
    if (opts.minNodes !== "") q.min_nodes = opts.minNodes;
    if (opts.minEdges !== "") q.min_edges = opts.minEdges;
  } else if (!opts.gap) {
    q.gap = "0";
  }
  if (opts.schema) q.schema = opts.schema;
  return q;
}

/** One string per distinct question, so an answer can be matched to the one it answers. */
export function queryKey(q: Params): string {
  return Object.entries(q)
    .filter(([, v]) => v !== undefined && v !== null && v !== "")
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${String(v)}`)
    .join("&");
}

/**
 * The answer for a read that never arrived, in the shape of one that did.
 *
 * Not an error banner over whatever was drawn last: after a source change that
 * would be the previous store's schema presented as this one's. The same
 * bargain the server makes by answering 200 with a report saying why. And a
 * draft that failed stays a draft, so a saved schema's sentence never lands
 * over a failure to derive one.
 */
export function failedAnswer(draft: boolean, reason: string): OntologyAnswer {
  const usage = {
    available: false,
    nodes: 0,
    edges: 0,
    node_types: 0,
    edge_types: 0,
    undeclared_nodes: [],
    undeclared_edges: [],
    undeclared_node_types: 0,
    undeclared_edge_types: 0,
    undeclared_node_count: 0,
    undeclared_edge_count: 0,
  };
  const base = {
    available: false,
    reason,
    saved: false,
    schemas: [],
    action_types: 0,
    object_sets: 0,
    shared_properties: 0,
    object_types: [],
    link_types: [],
    interfaces: [],
    declared_unused_types: 0,
    declared_unused_links: 0,
    usage,
    at: Date.now(),
  };
  if (!draft) return { ...base, state: ONTOLOGY_STATES.unreadable };
  return {
    ...base,
    state: DRAFT_STATES.undraftable,
    draft: true,
    min_nodes: 0,
    min_edges: 0,
    pruned_node_types: 0,
    pruned_nodes: 0,
    pruned_edge_types: 0,
    pruned_edges: 0,
    source_nodes: 0,
    source_edges: 0,
    source_node_types: 0,
    source_edge_types: 0,
    decisions: [],
    decisions_total: 0,
    notes: [],
  };
}

/**
 * The newest change to a saved schema the live stream has carried, or 0. The
 * page reads again when this moves, so a save shows up at once rather than at
 * the next poll.
 */
export function latestSchemaChange(changes: ReadonlyArray<Pick<ChangeItem, "seq" | "kind">>): number {
  let seq = 0;
  for (const c of changes) if (c.kind === "ontology_schema" && c.seq > seq) seq = c.seq;
  return seq;
}

/**
 * The versions page for the schema on screen. A draft has no versions of its
 * own, so it links to the saved schema it was held against, when there is one.
 */
export function versionsPath(rep: OntologyAnswer | null): string {
  if (!rep) return "/ontology-history";
  const id = rep.draft === true ? rep.against : rep.saved ? rep.schema_id : undefined;
  return id ? `/ontology-history?schema=${encodeURIComponent(id)}` : "/ontology-history";
}
