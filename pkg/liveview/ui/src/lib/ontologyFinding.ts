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
import type { Lang } from "@/lib/i18n";
import { FINDING_TEXT, numIn, type FindingText } from "@/lib/ontologyStrings";

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

/** A count in the language's digits (English when none is given, as the tests read it). */
export const num = (n: number | undefined | null, lang: Lang = "en") => numIn(lang, n);
export const plural = (n: number | undefined | null, one: string, many: string) => (Number(n || 0) === 1 ? one : many);

/** The runs as one string, for tests and for an aria-label. */
export function textOf(runs: ReadonlyArray<Run>): string {
  return runs.map((r) => (typeof r === "string" ? r : "b" in r ? r.b : r.em)).join("");
}

export function finding(rep: OntologyAnswer, lang: Lang = "en"): Finding {
  return isDraft(rep) ? draftFinding(rep, FINDING_TEXT[lang]) : savedFinding(rep, FINDING_TEXT[lang]);
}

function savedFinding(rep: OntologyAnswer, t: FindingText): Finding {
  const u = rep.usage;
  const objects = rep.object_types.length;
  const links = rep.link_types.length;
  const foot: Run[][] = [];
  let tone: Tone = "neutral";
  let runs: Run[];

  switch (rep.state) {
    case ONTOLOGY_STATES.unreadable:
      tone = "bad";
      runs = [...t.unreadable];
      foot.push([rep.reason || t.noReason]);
      break;

    case ONTOLOGY_STATES.absent:
      tone = "warn";
      runs = [...t.absent];
      if (u.available) runs.push(...t.absentUsage(u.nodes, u.node_types, u.edges, u.edge_types));
      break;

    case ONTOLOGY_STATES.unused:
      tone = "warn";
      runs = t.unused(objects, links);
      break;

    case ONTOLOGY_STATES.live:
      if (u.available) {
        runs = t.live(objects, links, u.nodes, u.edges);
        const unused = rep.declared_unused_types + rep.declared_unused_links;
        if (unused) runs.push(...t.liveUnused(unused));
        const strays = u.undeclared_node_types + u.undeclared_edge_types;
        if (strays) runs.push(...t.liveStrays(strays));
        if (!unused && !strays) runs.push(...t.liveClean);
      } else {
        // Saved, and the second reading was not taken. Every count on the
        // diagram is zero for a reason that has nothing to do with the data,
        // so this says so instead of letting the boxes imply otherwise.
        runs = t.liveUncounted(objects, links);
        foot.push([u.reason || t.gapOff]);
      }
      break;

    default:
      // A state this build has never heard of. Saying so is the only honest
      // thing left; falling through to a sentence would describe a reading
      // that may not have happened.
      tone = "bad";
      runs = [...t.unknownState];
      foot.push([`${rep.state || t.noState} · ${rep.reason || t.noReason}`]);
  }

  if (u.available && u.counted) foot.push([t.scope(u.counted, u.returned, u.total)]);
  else if (u.available && u.scope) foot.push([u.scope]);
  const own = u.available ? [...(u.store_nodes ?? []), ...(u.store_edges ?? [])] : [];
  if (own.length) foot.push([t.storeOwn(own)]);
  return { tone, runs, foot, notes: [] };
}

function draftFinding(rep: OntologyDraftView, t: FindingText): Finding {
  switch (rep.state) {
    case DRAFT_STATES.undraftable:
      return { tone: "bad", runs: [...t.undraftable], foot: [[rep.reason || t.noReason]], notes: [] };
    case DRAFT_STATES.nothingToDraft:
      return { tone: "warn", runs: [...t.nothingToDraft], foot: [], notes: [] };
    case DRAFT_STATES.drafted:
      break;
    default:
      return {
        tone: "bad",
        runs: [...t.unknownDraftState],
        foot: [[`${rep.state || t.noState} · ${rep.reason || t.noReason}`]],
        notes: [],
      };
  }

  const runs: Run[] = t.drafted(rep.object_types.length, rep.link_types.length, rep.source_nodes, rep.source_edges);
  if (rep.decisions_total) runs.push(...t.waiting(rep.decisions_total));

  // The threshold, stated whether or not it bit. A drawing of thirty boxes
  // with a hundred types behind it reads as a small brain unless the page
  // says otherwise, and "nothing was pruned" is the other half of the same
  // sentence: without it, silence would mean either.
  const threshold: Run[] = t.threshold(rep.min_nodes, rep.min_edges);
  if (rep.pruned_node_types || rep.pruned_edge_types) {
    threshold.push(...t.pruned(rep.pruned_node_types, rep.pruned_nodes, rep.pruned_edge_types, rep.pruned_edges));
  } else {
    threshold.push(...t.notPruned);
  }
  threshold.push(...t.storeCarries(rep.source_node_types, rep.source_edge_types));

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
export function headerOf(rep: OntologyAnswer, lang: Lang = "en"): Header {
  const t = FINDING_TEXT[lang];
  const n = (x: number | undefined | null) => num(x, lang);
  if (isDraft(rep)) {
    if (rep.state === DRAFT_STATES.undraftable) return { title: null, detail: t.noDraft, pills: [] };
    if (rep.state === DRAFT_STATES.nothingToDraft) return { title: null, detail: t.nothingToDraftFrom, pills: [] };
    const pills: Pill[] = [
      { label: t.draftPill, tone: "draft", title: t.draftPillTitle },
      { label: rep.enforcement || "vocabulary", tone: "cold", title: t.draftEnforcementTitle },
      { label: t.thresholdPill(rep.min_nodes, rep.min_edges), tone: "cold", title: t.thresholdTitle },
    ];
    if (rep.against) pills.push({ label: t.vs(rep.against), tone: "warn", title: t.vsTitle });
    return { title: rep.schema_id || t.draftTitle, detail: t.derived, pills };
  }
  if (!rep.saved) {
    return { title: null, detail: rep.state === ONTOLOGY_STATES.unreadable ? t.noAnswer : t.noSchema, pills: [] };
  }
  const pills: Pill[] = [
    rep.active ? { label: t.active, tone: "on", title: t.activeTitle } : { label: t.inactive, tone: "cold", title: t.inactiveTitle },
  ];
  // CortexDB's own sources always say which enforcement a stored schema has,
  // the strict default included. An empty one comes from a host's own source
  // that declared none, and a badge would claim a rule nobody stated.
  if (rep.enforcement) {
    pills.push({ label: rep.enforcement, tone: rep.enforcement === "strict" ? "on" : "warn", title: t.enforcementTitle });
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
  /** "and N more", when the server capped the list and not the count. */
  more: string | null;
}

export interface Outside {
  runs: Run[];
  groups: StrayGroup[];
}

function strayGroup(kind: string, list: OntologyStrayType[], total: number, truncated: boolean | undefined, lang: Lang): StrayGroup {
  return {
    label: `${kind} · ${num(total, lang)}`,
    chips: list.map((s) => ({ name: s.name || null, count: num(s.count, lang) })),
    more: truncated ? FINDING_TEXT[lang].andMore(total - list.length) : null,
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
  const lead = isDraft(rep)
    ? // On a draft this band is the deriver's own exclusions, drawn where a
      // reader already knows to look for what a model leaves out. The reasons
      // are named because only the last of them is the reader's own doing.
      t.outsideDraft
    : rep.saved
      ? t.outsideSaved
      : t.outsideNone;
  const runs: Run[] = [lead, ...t.outsideCounts(u.undeclared_node_count, u.nodes, u.undeclared_edge_count, u.edges)];
  const groups: StrayGroup[] = [];
  if (u.undeclared_nodes.length) groups.push(strayGroup(t.nodeTypes, u.undeclared_nodes, u.undeclared_node_types, u.node_list_truncated, lang));
  if (u.undeclared_edges.length) groups.push(strayGroup(t.edgeTypes, u.undeclared_edges, u.undeclared_edge_types, u.edge_list_truncated, lang));
  return { runs, groups };
}

// ---- the draft's review half ---------------------------------------------

/**
 * The sentence above the diff against a schema somebody did save. Only when
 * there is one: a diff against nothing renders as "every type added", which
 * reads as a change somebody made rather than as a store nobody has modelled.
 */
export function againstSummary(rep: OntologyDraftView, lang: Lang = "en"): Run[] | null {
  if (!rep.against) return null;
  const total = rep.changes_total ?? 0;
  const breaking = (rep.changes ?? []).filter((c) => c.breaking).length;
  return FINDING_TEXT[lang].against(total, !!rep.breaking, breaking);
}
