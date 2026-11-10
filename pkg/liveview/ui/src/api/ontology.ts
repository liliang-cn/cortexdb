// The ontology page's wire shapes: pkg/liveview/ontology.go (OntologyReport and
// what it carries) and ontology_draft.go (OntologyDraftView, which embeds the
// report and adds the half a draft has and a saved schema does not). Field
// names are the Go json tags; optional here means omitempty there.

import { getJSON, type Params } from "./client";

/** The four things a saved-schema answer can say, decided in Go (ontology.go). */
export const ONTOLOGY_STATES = {
  /** A source with no ontology hook, or a read that failed: nobody asked. */
  unreadable: "unreadable",
  /** Asked and answered: this store holds no schema. */
  absent: "absent",
  /** A schema exists and every declared type is at zero. */
  unused: "unused",
  /** A schema exists and has not been shown to be unused. */
  live: "live",
} as const;

/**
 * The three a draft can say (ontology_draft.go). Deliberately disjoint from the
 * four above, so a switch on the state can never hand a draft a saved schema's
 * sentence.
 */
export const DRAFT_STATES = {
  undraftable: "undraftable",
  nothingToDraft: "nothing-to-draft",
  drafted: "drafted",
} as const;

export type OntologyState = (typeof ONTOLOGY_STATES)[keyof typeof ONTOLOGY_STATES];
export type DraftState = (typeof DRAFT_STATES)[keyof typeof DRAFT_STATES];

export interface OntologyProp {
  api_name: string;
  display_name?: string;
  kind?: string;
  required?: boolean;
  searchable?: boolean;
  vectorized?: boolean;
}

export interface OntologyObjectNode {
  api_name: string;
  display_name?: string;
  description?: string;
  primary_key?: string;
  title_property?: string;
  status?: string;
  visibility?: string;
  implements: string[];
  aliases?: string[];
  properties: OntologyProp[];
  /** Nodes typed as this; meaningful only when usage.available. */
  instances: number;
}

export interface OntologyLinkEnd {
  api_name: string;
  display_name?: string;
  object_type: string;
  /** "ONE", "MANY", or whatever the schema left blank. */
  cardinality: string;
  /** The property on this side that holds the key, when one is declared. */
  foreign_key?: string;
}

export interface OntologyLinkEdge {
  api_name: string;
  description?: string;
  status?: string;
  a: OntologyLinkEnd;
  b: OntologyLinkEnd;
  /** "one-to-many" and so on; empty when a side is undeclared. */
  multiplicity: string;
  instances: number;
}

export interface OntologyInterfaceNode {
  api_name: string;
  display_name?: string;
  description?: string;
  extends?: string[];
  properties: OntologyProp[];
  /** Object types implementing it, Extends followed transitively. */
  implementors: string[];
}

/** A type the store's records carry that nothing declares. Name is empty for a write that named no type. */
export interface OntologyStrayType {
  name: string;
  count: number;
}

export interface OntologyUsage {
  /** Whether the store's own vocabulary was read; every count means nothing without it. */
  available: boolean;
  reason?: string;
  /** What was counted, when that is less than the whole store. */
  scope?: string;
  /** What was counted, as a value the page words: "store" or "graph". */
  counted?: "store" | "graph";
  /** Of a truncated graph, how many nodes came back out of total. */
  returned?: number;
  total?: number;
  nodes: number;
  edges: number;
  node_types: number;
  edge_types: number;
  undeclared_nodes: OntologyStrayType[];
  undeclared_edges: OntologyStrayType[];
  undeclared_node_types: number;
  undeclared_edge_types: number;
  undeclared_node_count: number;
  undeclared_edge_count: number;
  node_list_truncated?: boolean;
  edge_list_truncated?: boolean;
}

export interface OntologySchemaRef {
  schema_id: string;
  name?: string;
  version: number;
  active: boolean;
}

/** liveview.OntologyReport: the declarations, the store's own vocabulary, and the state Go read off the two. */
export interface OntologyReport {
  available: boolean;
  reason?: string;
  state: OntologyState | DraftState | (string & {});
  saved: boolean;
  schemas: OntologySchemaRef[];
  schema_id?: string;
  name?: string;
  description?: string;
  version?: number;
  active?: boolean;
  enforcement?: string;
  strict_actions?: boolean;
  action_types: number;
  object_sets: number;
  shared_properties: number;
  object_types: OntologyObjectNode[];
  link_types: OntologyLinkEdge[];
  interfaces: OntologyInterfaceNode[];
  declared_unused_types: number;
  declared_unused_links: number;
  usage: OntologyUsage;
  /** Unix ms. */
  at: number;
}

export interface OntologyDecision {
  target: string;
  detail: string;
  evidence?: string;
}

export interface OntologyDecisionGroup {
  kind: string;
  title: string;
  question: string;
  /** How many there are; decisions may be shorter. */
  count: number;
  decisions: OntologyDecision[];
  truncated?: boolean;
}

/** cortexdb.OntologyChange: one difference between two schemas. */
export interface OntologyChange {
  kind: string;
  target: string;
  detail: string;
  breaking: boolean;
}

/** liveview.OntologyDraftView: a derived schema drawn through the saved one's renderer. */
export interface OntologyDraftView extends OntologyReport {
  /** Always true on this payload; the page reads it before anything else. */
  draft: true;
  min_nodes: number;
  min_edges: number;
  pruned_node_types: number;
  pruned_nodes: number;
  pruned_edge_types: number;
  pruned_edges: number;
  source_nodes: number;
  source_edges: number;
  source_node_types: number;
  source_edge_types: number;
  buckets?: Record<string, number>;
  derived_at?: number;
  decisions: OntologyDecisionGroup[];
  decisions_total: number;
  notes: string[];
  against?: string;
  against_version?: number;
  changes?: OntologyChange[];
  changes_total?: number;
  breaking?: boolean;
}

/**
 * What api/ontology answers. A saved-schema report carries no draft field at
 * all, so `draft === true` is the one discriminator, and it is never inferred
 * from the state word.
 */
export type OntologyAnswer = (OntologyReport & { draft?: undefined | false }) | OntologyDraftView;

export function isDraft(rep: OntologyAnswer): rep is OntologyDraftView {
  return rep.draft === true;
}

/** GET api/ontology with the query lib/ontology builds from the page's options. */
export function fetchOntology(params: Params, signal?: AbortSignal): Promise<OntologyAnswer> {
  return getJSON<OntologyAnswer>("api/ontology", params, signal);
}
