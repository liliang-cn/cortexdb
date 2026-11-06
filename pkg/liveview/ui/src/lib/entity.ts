// Pure helpers for the entity page: folding co_occurs away from the real
// relations, the plain-words explanation of why a version is in effect, and
// lining history up under the relations it belongs to.

import type { EntityRelation, GraphHistory, HistoryLine, HistoryVersion, InEffect, RelationGroup } from "@/api";

export const CO_OCCURS = "co_occurs";

export interface SplitRelations {
  groups: RelationGroup[];
  /** Everything mentioned together with the entity, once each, either direction. */
  together: EntityRelation[];
}

export function splitCoOccurs(groups: ReadonlyArray<RelationGroup>): SplitRelations {
  const together = new Map<string, EntityRelation>();
  const rest: RelationGroup[] = [];
  for (const g of groups) {
    if (g.predicate === CO_OCCURS) {
      for (const e of g.edges) if (!together.has(e.other.id)) together.set(e.other.id, e);
    } else {
      rest.push(g);
    }
  }
  return { groups: rest, together: Array.from(together.values()) };
}

export function countEdges(groups: ReadonlyArray<RelationGroup>): number {
  return groups.reduce((n, g) => n + g.edges.length, 0);
}

// ---- why a version is in effect -------------------------------------------

const IE_TITLE: Record<string, string> = {
  only_version: "The only version",
  replaced: "Replaced an earlier value",
  revised: "Revised in place",
  merge_survivor: "Survivor of a merge",
  several_in_effect: "Several values in effect",
};

const OPINION: Record<string, string> = {
  no_opinion: "The schema has no opinion on this relation, so nothing is closed and no value is picked as the winner.",
  multi_valued: "The schema declares this relation multi-valued, so these values hold together.",
  single_valued_unenforced:
    "The schema declares this relation single-valued, but these values were written by a path that does not close the old one.",
};

export interface InEffectView {
  title: string;
  /** The server's own sentence. */
  summary: string;
  /** Extra plain-words lines: the schema's opinion, the rule, the schema version. */
  notes: string[];
  several: boolean;
}

export function describeInEffect(ie: InEffect | undefined): InEffectView | null {
  if (!ie) return null;
  const notes: string[] = [];
  const opinion = ie.schema_opinion ? OPINION[ie.schema_opinion] : undefined;
  if (opinion) notes.push(opinion);
  // The server's sentence often already names the rule and the schema.
  const said = ie.summary.toLowerCase();
  if (ie.rule && !said.includes(ie.rule.replaceAll("_", "-")) && !said.includes(ie.rule.replaceAll("_", " "))) notes.push(`Rule: ${ie.rule.replaceAll("_", " ")}.`);
  if (ie.schema_id && !said.includes(`schema ${ie.schema_id.toLowerCase()}`)) notes.push(`Decided under schema ${ie.schema_id}${ie.schema_version ? ` v${ie.schema_version}` : ""}.`);
  if (ie.also_asserted_by && ie.also_asserted_by.length > 0) {
    const n = ie.also_asserted_by.length;
    notes.push(`Also stated by ${n} other record${n === 1 ? "" : "s"}.`);
  }
  return { title: IE_TITLE[ie.kind] ?? ie.kind.replaceAll("_", " "), summary: ie.summary, notes, several: ie.kind === "several_in_effect" };
}

/** An in_effect that adds nothing: a lone, unremarkable version. */
export function isPlainInEffect(ie: InEffect | undefined): boolean {
  return !ie || ie.kind === "only_version";
}

// ---- history under relations ------------------------------------------------

export function isPast(v: HistoryVersion): boolean {
  return v.status !== "current";
}

export function relationKey(direction: "out" | "in", predicate: string): string {
  return `${direction}|${predicate}`;
}

/**
 * History lines keyed by `direction|predicate`, co_occurs left out. An incoming
 * predicate has one line per subject, so each key holds a list.
 */
export function historyByRelation(h: GraphHistory | null): Map<string, HistoryLine[]> {
  const m = new Map<string, HistoryLine[]>();
  for (const line of h?.lines ?? []) {
    if (line.predicate === CO_OCCURS) continue;
    const k = relationKey(line.direction === "in" ? "in" : "out", line.predicate);
    m.set(k, [...(m.get(k) ?? []), line]);
  }
  return m;
}

/**
 * The relation groups to draw for one side: the live groups, plus predicates
 * that have past versions but nothing live (everything retracted), as empty groups.
 */
export function groupsWithHistory(
  direction: "out" | "in",
  groups: ReadonlyArray<RelationGroup>,
  history: Map<string, HistoryLine[]>,
): RelationGroup[] {
  const out = [...groups];
  const known = new Set(groups.map((g) => g.predicate));
  for (const [key, lines] of history) {
    const [dir, ...rest] = key.split("|");
    const predicate = rest.join("|");
    if (dir !== direction || known.has(predicate)) continue;
    if (!lines.some((l) => l.versions.some(isPast))) continue;
    out.push({ predicate, edges: [] });
  }
  return out;
}

/** The version that is the current relation: by its edge row id, else by the far end. */
export function versionFor(lines: ReadonlyArray<HistoryLine>, rel: EntityRelation): HistoryVersion | undefined {
  for (const l of lines) {
    const byId = l.versions.find((v) => v.id === rel.edge_id);
    if (byId) return byId;
  }
  for (const l of lines) {
    const far = l.direction === "in" ? l.subject === rel.other.id : false;
    const v = l.versions.find((x) => !isPast(x) && (far || (l.direction !== "in" && x.object === rel.other.id)));
    if (v) return v;
  }
  return undefined;
}

export interface PastEntry {
  line: HistoryLine;
  version: HistoryVersion;
}

function endedAt(v: HistoryVersion): string {
  return v.valid_to || v.retracted_at || v.recorded_at || "";
}

/** Past versions across a group's lines, the most recently ended first. */
export function pastEntries(lines: ReadonlyArray<HistoryLine>): PastEntry[] {
  const out: PastEntry[] = [];
  for (const line of lines) for (const version of line.versions) if (isPast(version)) out.push({ line, version });
  return out.sort((a, b) => endedAt(b.version).localeCompare(endedAt(a.version)));
}

/** The name of the value at the far end of a version, from the entity's side. */
export function versionOther(line: HistoryLine, v: HistoryVersion): { id: string; name: string } {
  if (line.direction === "in") return { id: line.subject, name: line.subject_name || line.subject };
  return { id: v.object, name: v.object_name || v.object };
}

/** What took a past version's place, when the history still holds it. */
export function successorOf(lines: ReadonlyArray<HistoryLine>, v: HistoryVersion): { id: string; name: string; status: string } | undefined {
  if (!v.superseded_by) return undefined;
  for (const l of lines) {
    const next = l.versions.find((x) => x.id === v.superseded_by);
    if (next) return { ...versionOther(l, next), status: next.status };
  }
  return undefined;
}

/** "superseded" reads as "replaced"; the rest as they are. */
export function statusWords(status: string): string {
  return status === "superseded" ? "replaced" : status;
}
