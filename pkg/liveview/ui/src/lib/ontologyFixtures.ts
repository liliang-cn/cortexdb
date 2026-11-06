// Reports in the shapes pkg/liveview answers with, for the ontology tests. The
// transit schema is the one the page is checked against by hand: two
// interfaces, a type implementing both, a type implementing nothing, one
// nobody uses, a self-loop and a link nothing carries.

import type { OntologyDraftView, OntologyLinkEdge, OntologyObjectNode, OntologyReport, OntologyUsage } from "@/api/ontology";

export function usage(over: Partial<OntologyUsage> = {}): OntologyUsage {
  return {
    available: true,
    scope: "counted over every row in the store, chunk nodes included",
    nodes: 22,
    edges: 15,
    node_types: 9,
    edge_types: 6,
    undeclared_nodes: [
      { name: "concept", count: 5 },
      { name: "memory", count: 3 },
      { name: "", count: 1 },
    ],
    undeclared_edges: [
      { name: "mentions", count: 4 },
      { name: "about", count: 1 },
    ],
    undeclared_node_types: 3,
    undeclared_edge_types: 2,
    undeclared_node_count: 9,
    undeclared_edge_count: 5,
    ...over,
  };
}

export const noUsage = (reason = "the store's own vocabulary was not read"): OntologyUsage => ({
  available: false,
  reason,
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
});

const ot = (api_name: string, instances: number, implementsList: string[] = [], over: Partial<OntologyObjectNode> = {}): OntologyObjectNode => ({
  api_name,
  primary_key: `${api_name.toLowerCase()}Id`,
  implements: implementsList,
  properties: [{ api_name: `${api_name.toLowerCase()}Id`, kind: "string", required: true }],
  instances,
  ...over,
});

const lt = (api_name: string, a: string, ac: string, b: string, bc: string, instances: number, fk?: { side: "a" | "b"; prop: string }): OntologyLinkEdge => ({
  api_name,
  a: { api_name: `${api_name}_a`, object_type: a, cardinality: ac, ...(fk?.side === "a" ? { foreign_key: fk.prop } : {}) },
  b: { api_name: `${api_name}_b`, object_type: b, cardinality: bc, ...(fk?.side === "b" ? { foreign_key: fk.prop } : {}) },
  multiplicity: ac && bc ? `${ac.toLowerCase()}-to-${bc.toLowerCase()}` : "",
  instances,
});

export function transit(over: Partial<OntologyReport> = {}): OntologyReport {
  return {
    available: true,
    state: "live",
    saved: true,
    schemas: [
      { schema_id: "transit", name: "Transit", version: 1, active: true },
      { schema_id: "transit-lite", name: "Transit (lite)", version: 1, active: false },
    ],
    schema_id: "transit",
    name: "Transit",
    version: 1,
    active: true,
    enforcement: "vocabulary",
    action_types: 0,
    object_sets: 0,
    shared_properties: 0,
    object_types: [
      ot("Airport", 3, ["Locatable"]),
      ot("Station", 2, ["Locatable"]),
      ot("Gate", 1, ["Locatable"], { status: "experimental" }),
      ot("Flight", 4, ["Scheduled", "Locatable"]),
      ot("Train", 1, ["Scheduled"]),
      ot("Airline", 2),
      ot("Passenger", 0, [], { status: "deprecated" }),
    ],
    link_types: [
      lt("flightDeparture", "Airport", "MANY", "Flight", "ONE", 4, { side: "b", prop: "originIata" }),
      lt("operatedBy", "Airline", "MANY", "Flight", "ONE", 3, { side: "b", prop: "operatorCode" }),
      lt("trainOrigin", "Station", "MANY", "Train", "ONE", 1, { side: "b", prop: "originStation" }),
      lt("gateAt", "Airport", "MANY", "Gate", "ONE", 0, { side: "b", prop: "airportCode" }),
      lt("connectsTo", "Airport", "MANY", "Airport", "MANY", 2),
      lt("books", "Passenger", "MANY", "Flight", "MANY", 0),
    ],
    interfaces: [
      { api_name: "Locatable", properties: [{ api_name: "latitude", kind: "double" }], implementors: ["Airport", "Flight", "Gate", "Station"] },
      { api_name: "Scheduled", properties: [{ api_name: "departsAt", kind: "timestamp" }], implementors: ["Flight", "Train"] },
    ],
    declared_unused_types: 1,
    declared_unused_links: 2,
    usage: usage(),
    at: 1_793_956_027_540,
    ...over,
  };
}

/** A store asked and answering that it holds no schema. */
export function absent(over: Partial<OntologyReport> = {}): OntologyReport {
  return transit({
    state: "absent",
    saved: false,
    schemas: [],
    schema_id: undefined,
    name: undefined,
    version: undefined,
    active: undefined,
    enforcement: undefined,
    object_types: [],
    link_types: [],
    interfaces: [],
    declared_unused_types: 0,
    declared_unused_links: 0,
    ...over,
  });
}

export function draft(over: Partial<OntologyDraftView> = {}): OntologyDraftView {
  const base = transit({
    state: "drafted",
    saved: false,
    schemas: [],
    schema_id: "draft",
    name: "Draft derived from the graph",
    active: undefined,
    declared_unused_types: 0,
    declared_unused_links: 0,
    usage: usage({ undeclared_nodes: [{ name: "memory", count: 3 }], undeclared_edges: [], undeclared_node_types: 1, undeclared_edge_types: 0, undeclared_node_count: 3, undeclared_edge_count: 0 }),
  });
  return {
    ...base,
    draft: true,
    min_nodes: 3,
    min_edges: 3,
    pruned_node_types: 4,
    pruned_nodes: 6,
    pruned_edge_types: 3,
    pruned_edges: 4,
    source_nodes: 22,
    source_edges: 15,
    source_node_types: 9,
    source_edge_types: 6,
    decisions: [
      {
        kind: "primary_key_guess",
        title: "A key the machine guessed",
        question: "A primary key is what a person promises will stay unique.",
        count: 2,
        decisions: [{ target: "Airport", detail: "the draft guesses \"name\"", evidence: "name is on 3 of 3 \"Airport\" nodes" }],
        truncated: true,
      },
    ],
    decisions_total: 2,
    notes: ["Nothing here was saved. Read it, correct it, then save it with ontology_save."],
    ...over,
  };
}
