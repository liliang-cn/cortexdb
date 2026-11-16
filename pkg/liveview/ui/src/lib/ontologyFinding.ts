// What the ontology page shows above the drawing: a status word, the
// report's numbers beside their labels, and a few label → value facts.
//
// Chosen by the report's state, which Go decided, and never inferred from an
// empty list: an empty list of object types is produced by a source that
// cannot be asked, by a store with no schema and by a schema drawn with
// ?gap=0, and telling a reader the wrong one of those is worse than telling
// them nothing. Each state has its own status word.
//
// Draftness is read off `draft`, before the state, so a state this build has
// never heard of still arrives wearing a draft's clothes. Inferring it from
// the state word would make the first unrecognised draft state render as a
// saved schema, which is the one mistake the draft surface is arranged to
// prevent.

import { DRAFT_STATES, ONTOLOGY_STATES, isDraft, type OntologyAnswer, type OntologyDraftView, type OntologySchemaRef, type OntologyStrayType } from "@/api/ontology";
import type { Lang } from "@/lib/i18n";
import { FINDING_TEXT, numIn, type FindingText } from "@/lib/ontologyStrings";

/** The finding's weight: neutral reads as an answer, warn as an absence, bad as nobody being able to answer. */
export type Tone = "neutral" | "warn" | "bad";

/** A label and its value, both already worded for the page. */
export interface Field {
  label: string;
  value: string;
  /** Worth a second look: a declaration at zero, a type nothing declares, a breaking change. */
  warn?: boolean;
}

export interface Finding {
  tone: Tone;
  /** The state as a status word, or null when the numbers are the whole answer. */
  status: string | null;
  /** The headline counts. */
  stats: Field[];
  /** Smaller label → value lines: what was counted, a threshold. */
  facts: Field[];
  /** Why a reading failed, as the server worded it. */
  error: string | null;
}

/** A count in the language's digits (English when none is given, as the tests read it). */
export const num = (n: number | undefined | null, lang: Lang = "en") => numIn(lang, n);

/**
 * The finding for a report. `gap` is whether the reader asked for the
 * declared-vs-actual count, so an uncounted schema says "off" when it was
 * switched off and shows the server's reason when counting failed.
 */
export function finding(rep: OntologyAnswer, lang: Lang = "en", gap = true): Finding {
  return isDraft(rep) ? draftFinding(rep, FINDING_TEXT[lang], lang) : savedFinding(rep, FINDING_TEXT[lang], lang, gap);
}

const field = (label: string, n: number | undefined | null, lang: Lang, warn = false): Field => ({ label, value: num(n, lang), ...(warn && Number(n || 0) > 0 ? { warn } : {}) });

function stateError(state: string | undefined, reason: string | undefined, t: FindingText): string {
  return `${state || t.missing} · ${reason || t.missing}`;
}

function savedFinding(rep: OntologyAnswer, t: FindingText, lang: Lang, gap: boolean): Finding {
  const u = rep.usage;
  const declared = [field(t.objectTypes, rep.object_types.length, lang), field(t.linkTypes, rep.link_types.length, lang)];
  const store = u.available ? [field(t.nodes, u.nodes, lang), field(t.edges, u.edges, lang)] : [];
  const facts: Field[] = [];
  const f: Finding = { tone: "neutral", status: null, stats: [], facts, error: null };

  switch (rep.state) {
    case ONTOLOGY_STATES.unreadable:
      f.tone = "bad";
      f.status = t.unreadable;
      f.error = rep.reason || null;
      break;

    case ONTOLOGY_STATES.absent:
      f.tone = "warn";
      f.status = t.absent;
      if (u.available) f.stats = [field(t.nodes, u.nodes, lang), field(t.nodeTypes, u.node_types, lang), field(t.edges, u.edges, lang), field(t.edgeTypes, u.edge_types, lang)];
      break;

    case ONTOLOGY_STATES.unused:
      f.tone = "warn";
      f.status = t.unused;
      f.stats = [...declared, ...store];
      break;

    case ONTOLOGY_STATES.live:
      if (u.available) {
        f.stats = [
          ...declared,
          ...store,
          field(t.unusedDeclarations, rep.declared_unused_types + rep.declared_unused_links, lang, true),
          field(t.undeclaredTypes, u.undeclared_node_types + u.undeclared_edge_types, lang, true),
        ];
      } else {
        // Saved, and the second reading was not taken. Every count on the
        // diagram is zero for a reason that has nothing to do with the data,
        // so the status says so instead of letting the boxes imply otherwise.
        f.status = t.uncounted;
        f.stats = declared;
        if (!gap) facts.push({ label: t.gapLabel, value: t.off });
        else f.error = u.reason || null;
      }
      break;

    default:
      // A state this build has never heard of. Naming it is the only honest
      // thing left; falling through would describe a reading that may not
      // have happened.
      f.tone = "bad";
      f.status = t.unknownState;
      f.error = stateError(rep.state, rep.reason, t);
  }

  if (u.available && u.counted) {
    facts.push({ label: t.countedLabel, value: t.counted(u.counted) });
    if (u.counted === "graph" && u.total) facts.push({ label: t.returnedLabel, value: `${num(u.returned, lang)} / ${num(u.total, lang)}` });
  }
  const own = u.available ? [...(u.store_nodes ?? []), ...(u.store_edges ?? [])] : [];
  if (own.length) facts.push({ label: t.storeOwnLabel, value: own.map((k) => `${k.name} ${num(k.count, lang)}`).join(" · ") });
  return f;
}

function draftFinding(rep: OntologyDraftView, t: FindingText, lang: Lang): Finding {
  switch (rep.state) {
    case DRAFT_STATES.undraftable:
      return { tone: "bad", status: t.undraftable, stats: [], facts: [], error: rep.reason || null };
    case DRAFT_STATES.nothingToDraft:
      return { tone: "warn", status: t.nothingToDraft, stats: [], facts: [], error: null };
    case DRAFT_STATES.drafted:
      break;
    default:
      return { tone: "bad", status: t.unknownState, stats: [], facts: [], error: stateError(rep.state, rep.reason, t) };
  }

  // The threshold is stated whether or not it bit. A drawing of thirty boxes
  // with a hundred types behind it reads as a small brain unless the page
  // shows otherwise, and "0 not drawn" is the other half of the same fact.
  const pruned = rep.pruned_node_types || rep.pruned_edge_types;
  return {
    tone: "warn",
    status: t.drafted,
    stats: [
      field(t.objectTypes, rep.object_types.length, lang),
      field(t.linkTypes, rep.link_types.length, lang),
      field(t.nodes, rep.source_nodes, lang),
      field(t.edges, rep.source_edges, lang),
      field(t.nodeTypes, rep.source_node_types, lang),
      field(t.edgeTypes, rep.source_edge_types, lang),
      field(t.toDecide, rep.decisions_total, lang, true),
    ],
    facts: [
      { label: t.thresholdLabel, value: `min_nodes=${num(rep.min_nodes, lang)} · min_edges=${num(rep.min_edges, lang)}` },
      { label: t.prunedLabel, value: pruned ? t.pruned(rep.pruned_node_types, rep.pruned_nodes, rep.pruned_edge_types, rep.pruned_edges) : num(0, lang) },
    ],
    error: null,
  };
}

// ---- the header ---------------------------------------------------------

export type PillTone = "on" | "cold" | "warn" | "draft";

export interface Pill {
  label: string;
  tone: PillTone;
}

export interface Header {
  /** The schema's name, or null when there is none to name. */
  title: string | null;
  /** What follows the name, or stands in for it. */
  detail: string | null;
  pills: Pill[];
}

/**
 * The line under the page title. A draft never wears a saved schema's pills:
 * no "active", no "inactive", no strict-actions badge, because those describe
 * a schema that is deciding what writes are allowed and this one decides
 * nothing.
 */
export function headerOf(rep: OntologyAnswer, lang: Lang = "en"): Header {
  const t = FINDING_TEXT[lang];
  const n = (x: number | undefined | null) => num(x, lang);
  if (isDraft(rep)) {
    if (rep.state === DRAFT_STATES.undraftable) return { title: null, detail: t.noDraft, pills: [] };
    if (rep.state === DRAFT_STATES.nothingToDraft) return { title: null, detail: t.nothingToDraftFrom, pills: [] };
    const pills: Pill[] = [
      { label: t.draftPill, tone: "draft" },
      { label: rep.enforcement || "vocabulary", tone: "cold" },
      { label: t.thresholdPill(rep.min_nodes, rep.min_edges), tone: "cold" },
    ];
    if (rep.against) pills.push({ label: t.vs(rep.against), tone: "warn" });
    return { title: rep.schema_id || t.draftTitle, detail: null, pills };
  }
  if (!rep.saved) {
    return { title: null, detail: rep.state === ONTOLOGY_STATES.unreadable ? t.noAnswer : t.noSchema, pills: [] };
  }
  const pills: Pill[] = [
    rep.active ? { label: t.active, tone: "on" } : { label: t.inactive, tone: "cold" },
  ];
  // CortexDB's own sources always say which enforcement a stored schema has,
  // the strict default included. An empty one comes from a host's own source
  // that declared none, and a badge would claim a rule nobody stated.
  if (rep.enforcement) {
    pills.push({ label: rep.enforcement, tone: rep.enforcement === "strict" ? "on" : "warn" });
  }
  if (rep.strict_actions) pills.push({ label: t.strictActions, tone: "warn" });
  if (rep.action_types) pills.push({ label: t.actions(rep.action_types), tone: "cold" });
  if (rep.object_sets) pills.push({ label: t.objectSets(rep.object_sets), tone: "cold" });
  return { title: rep.name || rep.schema_id || null, detail: `v${n(rep.version)}`, pills };
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
  /** "+N", when the server capped the list and not the count. */
  more: string | null;
}

export interface Outside {
  stats: Field[];
  groups: StrayGroup[];
}

function strayGroup(kind: string, list: OntologyStrayType[], total: number, truncated: boolean | undefined, lang: Lang): StrayGroup {
  return {
    label: `${kind} · ${num(total, lang)}`,
    chips: list.map((s) => ({ name: s.name || null, count: num(s.count, lang) })),
    more: truncated ? FINDING_TEXT[lang].more(total - list.length) : null,
  };
}

/**
 * Types the store's records carry that no object or link type describes. On a
 * brain with no schema this is the whole answer: a vocabulary that exists and
 * was never declared. Null when there is nothing to put in the band, or the
 * reader turned it off.
 */
export function outsideTheModel(rep: OntologyAnswer, show: boolean, lang: Lang = "en"): Outside | null {
  const u = rep.usage;
  if (!show || !u.available || (u.undeclared_nodes.length === 0 && u.undeclared_edges.length === 0)) return null;
  const t = FINDING_TEXT[lang];
  const stats: Field[] = [
    { label: t.undeclaredNodes, value: `${num(u.undeclared_node_count, lang)} / ${num(u.nodes, lang)}` },
    { label: t.undeclaredEdges, value: `${num(u.undeclared_edge_count, lang)} / ${num(u.edges, lang)}` },
  ];
  const groups: StrayGroup[] = [];
  if (u.undeclared_nodes.length) groups.push(strayGroup(t.nodeTypesGroup, u.undeclared_nodes, u.undeclared_node_types, u.node_list_truncated, lang));
  if (u.undeclared_edges.length) groups.push(strayGroup(t.edgeTypesGroup, u.undeclared_edges, u.undeclared_edge_types, u.edge_list_truncated, lang));
  return { stats, groups };
}

// ---- the draft's review half ---------------------------------------------

/**
 * The counts above the diff against a schema somebody did save. Only when
 * there is one: a diff against nothing renders as "every type added", which
 * reads as a change somebody made rather than as a store nobody has modelled.
 * Breaking counts the listed changes that would stop something already stored
 * from validating.
 */
export function againstStats(rep: OntologyDraftView, lang: Lang = "en"): Field[] | null {
  if (!rep.against) return null;
  const t = FINDING_TEXT[lang];
  const breaking = rep.breaking ? (rep.changes ?? []).filter((c) => c.breaking).length : 0;
  return [field(t.changes, rep.changes_total ?? 0, lang), field(t.breaking, breaking, lang, true)];
}
