// What the ontology page says, before it draws anything.
//
// Chosen by the report's state, which Go decided, and never inferred from an
// empty list: an empty list of object types is produced by a source that
// cannot be asked, by a store with no schema and by a schema drawn with
// ?gap=0, and telling a reader the wrong one of those is worse than telling
// them nothing. Each state has its own sentence, worded as the page Go used to
// serve worded it.
//
// Draftness is read off `draft`, before the state, so a state this build has
// never heard of still arrives wearing a draft's clothes. Inferring it from
// the state word would make the first unrecognised draft state render as a
// saved schema, which is the one mistake the draft surface is arranged to
// prevent.
//
// Everything here returns text as runs, plain or set off, so the sentences are
// testable as strings and the page decides how a set-off run looks.

import { DRAFT_STATES, ONTOLOGY_STATES, isDraft, type OntologyAnswer, type OntologyDraftView, type OntologySchemaRef, type OntologyStrayType } from "@/api/ontology";

/** A piece of a sentence: plain, or set off (a number, a name the reader acts on). */
export type Run = string | { b: string } | { em: string };

/** The finding's weight: neutral reads as an answer, warn as an absence, bad as nobody being able to answer. */
export type Tone = "neutral" | "warn" | "bad";

export interface Finding {
  tone: Tone;
  runs: Run[];
  /** Small print under the sentence: a reason, a scope, a threshold. One array per line. */
  foot: Run[][];
  /** The deriver's own caveats, in its words. */
  notes: string[];
}

const NUM = new Intl.NumberFormat("en");
export const num = (n: number | undefined | null) => NUM.format(Number(n || 0));
export const plural = (n: number | undefined | null, one: string, many: string) => (Number(n || 0) === 1 ? one : many);
const b = (s: string | number): Run => ({ b: typeof s === "number" ? num(s) : s });

/** The runs as one string, for tests and for an aria-label. */
export function textOf(runs: ReadonlyArray<Run>): string {
  return runs.map((r) => (typeof r === "string" ? r : "b" in r ? r.b : r.em)).join("");
}

export function finding(rep: OntologyAnswer): Finding {
  return isDraft(rep) ? draftFinding(rep) : savedFinding(rep);
}

function savedFinding(rep: OntologyAnswer): Finding {
  const u = rep.usage;
  const objects = rep.object_types.length;
  const links = rep.link_types.length;
  const foot: Run[][] = [];
  let tone: Tone = "neutral";
  let runs: Run[];

  switch (rep.state) {
    case ONTOLOGY_STATES.unreadable:
      tone = "bad";
      runs = [b("This view cannot be asked for an ontology."), " That is not the same as this store having none — nobody asked it."];
      foot.push([rep.reason || "no reason given"]);
      break;

    case ONTOLOGY_STATES.absent:
      tone = "warn";
      runs = [b("No ontology is saved in this store."), " Nothing here declares what may be talked about, so nothing written to it was ever checked against a model."];
      if (u.available) {
        runs.push(
          " Its ", b(u.nodes), ` ${plural(u.nodes, "node", "nodes")} carry `, b(u.node_types), ` distinct ${plural(u.node_types, "type", "types")} and its `,
          b(u.edges), ` ${plural(u.edges, "edge", "edges")} carry `, b(u.edge_types), " — a vocabulary that exists and was never written down.",
        );
      }
      break;

    case ONTOLOGY_STATES.unused:
      tone = "warn";
      runs = [
        b("An ontology is saved, and nothing in the store uses it."), " Every one of its ", b(objects), ` object ${plural(objects, "type", "types")} and `,
        b(links), ` link ${plural(links, "type", "types")} is at zero. The model below describes intent; nothing here is evidence of it.`,
      ];
      break;

    case ONTOLOGY_STATES.live:
      if (u.available) {
        runs = [b(objects), " object types and ", b(links), " link types declared, against ", b(u.nodes), " nodes and ", b(u.edges), " edges in the store."];
        const unused = rep.declared_unused_types + rep.declared_unused_links;
        if (unused) runs.push(" ", b(unused), ` ${plural(unused, "declaration is", "declarations are")} at zero — declared, never used.`);
        const strays = u.undeclared_node_types + u.undeclared_edge_types;
        if (strays) runs.push(" ", b(strays), ` ${plural(strays, "type is", "types are")} in the data that the schema does not describe.`);
        if (!unused && !strays) runs.push(" Everything declared is in use and nothing in the store is outside the model.");
      } else {
        // Saved, and the second reading was not taken. Every count on the
        // diagram is zero for a reason that has nothing to do with the data,
        // so this says so instead of letting the boxes imply otherwise.
        runs = [b(objects), " object types and ", b(links), " link types declared. ", b("The gap was not measured"), ", so no count below means anything."];
        foot.push([u.reason || "declared vs actual is switched off"]);
      }
      break;

    default:
      // A state this build has never heard of. Saying so is the only honest
      // thing left; falling through to a sentence would describe a reading
      // that may not have happened.
      tone = "bad";
      runs = [b("This report arrived in a state this page does not know how to read.")];
      foot.push([`${rep.state || "no state given"} · ${rep.reason || "no reason given"}`]);
  }

  if (u.available && u.scope) foot.push([u.scope]);
  return { tone, runs, foot, notes: [] };
}

function draftFinding(rep: OntologyDraftView): Finding {
  switch (rep.state) {
    case DRAFT_STATES.undraftable:
      return {
        tone: "bad",
        runs: [b("This source cannot draft an ontology."), " That is not the same as this store having nothing to draft — nobody could ask it."],
        foot: [[rep.reason || "no reason given"]],
        notes: [],
      };
    case DRAFT_STATES.nothingToDraft:
      return {
        tone: "warn",
        runs: [
          b("There is nothing here to draft from."),
          " The store was read and holds no nodes, so there is no vocabulary to write down. That is an answer about this store, not about the deriver.",
        ],
        foot: [],
        notes: [],
      };
    case DRAFT_STATES.drafted:
      break;
    default:
      return {
        tone: "bad",
        runs: [b("This draft arrived in a state this page does not know how to read.")],
        foot: [[`${rep.state || "no state given"} · ${rep.reason || "no reason given"}`]],
        notes: [],
      };
  }

  const objects = rep.object_types.length;
  const links = rep.link_types.length;
  const runs: Run[] = [
    b("Nobody has saved this."), " It is a first schema derived from what this store already contains: ",
    b(objects), ` object ${plural(objects, "type", "types")} and `, b(links), ` link ${plural(links, "type", "types")} proposed over the `,
    b(rep.source_nodes), ` ${plural(rep.source_nodes, "node", "nodes")} and `, b(rep.source_edges), ` ${plural(rep.source_edges, "edge", "edges")} that were read. `,
    "Every type is experimental, every primary key is a guess, every cardinality is MANY, and enforcement is vocabulary. ",
    "Saving it — or a corrected version of it — is a person's act and happens elsewhere, through ", b("ontology_save"), ".",
  ];
  if (rep.decisions_total) {
    runs.push(" ", b(rep.decisions_total), ` ${plural(rep.decisions_total, "question is", "questions are")} waiting for a decision below.`);
  }

  // The threshold, stated whether or not it bit. A drawing of thirty boxes
  // with a hundred types behind it reads as a small brain unless the page
  // says otherwise, and "nothing was pruned" is the other half of the same
  // sentence: without it, silence would mean either.
  const threshold: Run[] = ["Drawn at ", b(`min_nodes=${num(rep.min_nodes)}`), ", ", b(`min_edges=${num(rep.min_edges)}`), ". "];
  if (rep.pruned_node_types || rep.pruned_edge_types) {
    threshold.push(
      "That kept ", b(rep.pruned_node_types), ` node ${plural(rep.pruned_node_types, "type", "types")} (${num(rep.pruned_nodes)} nodes) and `,
      b(rep.pruned_edge_types), ` edge ${plural(rep.pruned_edge_types, "type", "types")} (${num(rep.pruned_edges)} edges) out of the `, { em: "drawing" },
      " — not out of the counts. They are in the band below, and a lower threshold draws them.",
    );
  } else {
    threshold.push("Nothing was kept out of the drawing by the threshold; everything absent below was excluded by the deriver's own rules.");
  }
  threshold.push(" The store carries ", b(rep.source_node_types), " node types and ", b(rep.source_edge_types), " edge types in all.");

  // The deriver's own caveats, in its words. It knows what it did not do, and
  // a view paraphrasing that would be a second opinion pretending to be the
  // first.
  return { tone: "warn", runs, foot: [threshold], notes: [...(rep.notes ?? [])] };
}

// ---- the header ---------------------------------------------------------

export type PillTone = "on" | "cold" | "warn" | "draft";

export interface Pill {
  label: string;
  tone: PillTone;
  title?: string;
}

export interface Header {
  /** The schema's name, or null when there is none to name. */
  title: string | null;
  /** What follows the name, or stands in for it. */
  detail: string;
  pills: Pill[];
}

/**
 * The line under the page title. A draft never wears a saved schema's pills:
 * no "active", no "inactive", no strict-actions badge, because those describe
 * a schema that is deciding what writes are allowed and this one decides
 * nothing.
 */
export function headerOf(rep: OntologyAnswer): Header {
  if (isDraft(rep)) {
    if (rep.state === DRAFT_STATES.undraftable) return { title: null, detail: "no draft from this source", pills: [] };
    if (rep.state === DRAFT_STATES.nothingToDraft) return { title: null, detail: "nothing to draft from", pills: [] };
    const pills: Pill[] = [
      { label: "draft · nobody signed this", tone: "draft", title: "Derived from the store; nothing is saved until a person saves it with ontology_save." },
      { label: rep.enforcement || "vocabulary", tone: "cold", title: "Enforcement the draft would be saved with" },
      { label: `min ${num(rep.min_nodes)} / ${num(rep.min_edges)}`, tone: "cold", title: "The threshold the draft ran with: min_nodes / min_edges" },
    ];
    if (rep.against) pills.push({ label: `vs ${rep.against}`, tone: "warn", title: "Held against the schema this store already saved" });
    return { title: rep.schema_id || "draft", detail: "derived, not saved", pills };
  }
  if (!rep.saved) {
    return { title: null, detail: rep.state === ONTOLOGY_STATES.unreadable ? "no answer from this source" : "no schema saved", pills: [] };
  }
  const pills: Pill[] = [
    rep.active ? { label: "active", tone: "on", title: "This schema decides what writes are allowed" } : { label: "inactive", tone: "cold", title: "Saved, and not the one in effect" },
  ];
  // CortexDB's own sources always say which enforcement a stored schema has,
  // the strict default included. An empty one comes from a host's own source
  // that declared none, and a badge would claim a rule nobody stated.
  if (rep.enforcement) {
    pills.push({ label: rep.enforcement, tone: rep.enforcement === "strict" ? "on" : "warn", title: "What an active schema does to a write that does not conform" });
  }
  if (rep.strict_actions) pills.push({ label: "strict actions", tone: "warn" });
  if (rep.action_types) pills.push({ label: `${num(rep.action_types)} ${plural(rep.action_types, "action", "actions")}`, tone: "cold" });
  if (rep.object_sets) pills.push({ label: `${num(rep.object_sets)} object ${plural(rep.object_sets, "set", "sets")}`, tone: "cold" });
  return { title: rep.name || rep.schema_id || null, detail: `v${num(rep.version)}`, pills };
}

/**
 * The saved schemas a reader can switch between, or none. Only when there is a
 * choice to make: one saved schema needs no picker, and drawing one would
 * suggest the page is showing a slice of something. A draft offers none,
 * because it is not one of them.
 */
export function schemaChoices(rep: OntologyAnswer): Array<OntologySchemaRef & { current: boolean }> {
  if (isDraft(rep) || rep.schemas.length < 2) return [];
  const here = (rep.schema_id ?? "").trim().toLowerCase();
  return rep.schemas.map((s) => ({ ...s, current: s.schema_id.trim().toLowerCase() === here }));
}

// ---- the band outside the model ----------------------------------------

export interface StrayGroup {
  label: string;
  chips: Array<{ name: string | null; count: string }>;
  /** "and N more", when the server capped the list and not the count. */
  more: string | null;
}

export interface Outside {
  runs: Run[];
  groups: StrayGroup[];
}

function strayGroup(kind: string, list: OntologyStrayType[], total: number, truncated: boolean | undefined): StrayGroup {
  return {
    label: `${kind} · ${num(total)}`,
    chips: list.map((s) => ({ name: s.name || null, count: num(s.count) })),
    more: truncated ? `and ${num(total - list.length)} more` : null,
  };
}

/**
 * Types the store's records carry that no object or link type describes. On a
 * brain with no schema this is the whole answer: a vocabulary that exists and
 * was never declared. Null when there is nothing to put in the band, or the
 * reader turned it off.
 */
export function outsideTheModel(rep: OntologyAnswer, show: boolean): Outside | null {
  const u = rep.usage;
  if (!show || !u.available || (u.undeclared_nodes.length === 0 && u.undeclared_edges.length === 0)) return null;
  const lead = isDraft(rep)
    ? // On a draft this band is the deriver's own exclusions, drawn where a
      // reader already knows to look for what a model leaves out. The reasons
      // are named because only the last of them is the reader's own doing.
      "The draft is silent about these. They are what the deriver bucketed out — record kinds and the writes that named no type — or withheld: a minority spelling it would not merge, a name that cannot be an API name, or a type under the threshold."
    : rep.saved
      ? "These are the types the store's own records carry, which this schema never declares."
      : "There is no schema, so every type the store uses is here. This is the vocabulary the brain already has and has never written down.";
  const runs: Run[] = [
    lead, " ", b(u.undeclared_node_count), " of ", b(u.nodes), " nodes and ", b(u.undeclared_edge_count), " of ", b(u.edges), " edges are under one of them.",
  ];
  const groups: StrayGroup[] = [];
  if (u.undeclared_nodes.length) groups.push(strayGroup("node types", u.undeclared_nodes, u.undeclared_node_types, u.node_list_truncated));
  if (u.undeclared_edges.length) groups.push(strayGroup("edge types", u.undeclared_edges, u.undeclared_edge_types, u.edge_list_truncated));
  return { runs, groups };
}

// ---- the draft's review half ---------------------------------------------

/**
 * The sentence above the diff against a schema somebody did save. Only when
 * there is one: a diff against nothing renders as "every type added", which
 * reads as a change somebody made rather than as a store nobody has modelled.
 */
export function againstSummary(rep: OntologyDraftView): Run[] | null {
  if (!rep.against) return null;
  const total = rep.changes_total ?? 0;
  const breaking = (rep.changes ?? []).filter((c) => c.breaking).length;
  return [
    "This store already has a schema, and nothing here is applied to it. This is what would differ if the draft replaced it, taken through the same comparison a version bump goes through: ",
    b(total), ` ${plural(total, "change", "changes")}`,
    ...(rep.breaking
      ? [", of which ", b(breaking), " listed here would stop something already stored from validating."]
      : [", none of which would invalidate anything already stored."]),
  ];
}
