// Pure helpers for the Versions page: the rules for picking the two versions
// to compare and their round trip through the URL, how pages of versions are
// joined with each other and with a re-read after a live change, and the
// words a version's summary and activations say. Nothing here touches React
// or the network, so all of it is tested directly.

import type { ChangeItem } from "@/api/types";
import type { OntologyActivation, OntologyChangeSummary, OntologyHistoryResponse, OntologySchemaRef, OntologyVersionInfo } from "@/api/ontologyHistory";
import { summaryCounts, type DiffCounts } from "./ontologyHistoryDiff";
import type { Lang } from "@/lib/i18n";
import { VERSIONS_TEXT } from "@/lib/versionsStrings";

/** Versions per page. A version is a few lines, so a page is a screenful or two. */
export const PAGE_SIZE = 25;

/** The most one request may ask for: cortexdb's ontologyHistoryMaxLimit. */
export const HISTORY_MAX_LIMIT = 500;

// ---- picking two versions -------------------------------------------------

/** The picked versions in the order they were picked, at most two. */
export type Picks = readonly number[];

/**
 * Picking a picked version unpicks it. Picking a third drops the one picked
 * first, so the two most recent picks are always the pair.
 */
export function togglePick(picks: Picks, version: number): number[] {
  if (picks.includes(version)) return picks.filter((v) => v !== version);
  return [...picks, version].slice(-2);
}

export interface Pair {
  /** The older version, the diff's "before" side. */
  from: number;
  /** The newer version, the "after" side. */
  to: number;
}

/** The pair to compare, older first whatever the order of picking, or null until there are two. */
export function pairOf(picks: Picks): Pair | null {
  if (picks.length !== 2) return null;
  const [a = 0, b = 0] = picks;
  return { from: Math.min(a, b), to: Math.max(a, b) };
}

/** Which side of the comparison a version is on: "picked" while it waits for a second. */
export type PickRole = "from" | "to" | "picked";

export function pickRole(picks: Picks, version: number): PickRole | null {
  if (!picks.includes(version)) return null;
  const pair = pairOf(picks);
  if (!pair) return "picked";
  return version === pair.from ? "from" : "to";
}

/** A version against the one saved before it. Versions are numbered 1, 2, 3 with no gaps. */
export function previousPair(version: number): Pair | null {
  return version > 1 ? { from: version - 1, to: version } : null;
}

export function picksOfPair(pair: Pair): number[] {
  return [pair.from, pair.to];
}

/** Picks that name a version the schema has: a hand-edited link may name one it never had. */
export function picksWithin(picks: Picks, total: number): number[] {
  return picks.filter((v) => v >= 1 && v <= total);
}

/** Whether two lists pick the same versions, in whatever order. */
export function samePickSet(a: Picks, b: Picks): boolean {
  return a.length === b.length && a.every((v) => b.includes(v));
}

/** The picks as one comparable string, whatever the order they were picked in. */
export function picksKey(picks: Picks): string {
  return [...picks].sort((a, b) => a - b).join(",");
}

/**
 * Whether the picks the URL now names are ones the page wrote itself.
 *
 * The page holds the picks, in the order they were picked, and writes them to
 * the URL; the URL forgets the order and lags behind, because the router
 * applies a write in a transition. Two quick picks can therefore meet a URL
 * that does not yet name the first, and reading the picks back from it would
 * drop one. So the page keeps the keys it wrote, in order, and when the URL
 * arrives at one of them it is an echo: that key and every older one are
 * settled, and the page's own picks stand. Any other key came from outside, a
 * link opened or Back pressed, and wins.
 */
export function settleEcho(written: ReadonlyArray<string>, urlKey: string): { echo: boolean; written: string[] } {
  const at = written.indexOf(urlKey);
  if (at < 0) return { echo: false, written: [] };
  return { echo: true, written: written.slice(at + 1) };
}

// ---- the URL ----------------------------------------------------------------

function versionParam(raw: string | null): number | null {
  if (raw === null || !/^\d+$/.test(raw.trim())) return null;
  const n = Number(raw.trim());
  return Number.isSafeInteger(n) && n >= 1 ? n : null;
}

/**
 * ?from=&to= as picks. One side alone is a pick waiting for its second, so a
 * reload in the middle of choosing keeps the choice; the same version twice
 * is one pick.
 */
export function picksFromParams(params: URLSearchParams): number[] {
  const out: number[] = [];
  for (const key of ["from", "to"]) {
    const v = versionParam(params.get(key));
    if (v !== null && !out.includes(v)) out.push(v);
  }
  return out;
}

/** The picks written onto a copy of `base`, older version as from. Unrelated parameters stay. */
export function picksToParams(picks: Picks, base: URLSearchParams = new URLSearchParams()): URLSearchParams {
  const next = new URLSearchParams(base);
  next.delete("from");
  next.delete("to");
  const pair = pairOf(picks);
  if (pair) {
    next.set("from", String(pair.from));
    next.set("to", String(pair.to));
  } else if (picks[0] !== undefined) {
    next.set("from", String(picks[0]));
  }
  return next;
}

/** Another schema: its versions are not this one's, so the picks go with the old one. */
export function schemaToParams(schema: string, base: URLSearchParams = new URLSearchParams()): URLSearchParams {
  const next = new URLSearchParams(base);
  next.delete("from");
  next.delete("to");
  if (schema) next.set("schema", schema);
  else next.delete("schema");
  return next;
}

// ---- which schema ---------------------------------------------------------------

/** The schema shown when the URL names none: the active one, else the first saved, the order the server lists them in. */
export function defaultSchema(schemas: ReadonlyArray<OntologySchemaRef>): string {
  return (schemas.find((s) => s.active) ?? schemas[0])?.schema_id ?? "";
}

/**
 * The schemas to offer: the saved ones, plus the one the URL names when it is
 * not among them. A deleted schema keeps its history, and a link to that
 * history must still open it.
 */
export function schemaOptions(schemas: ReadonlyArray<OntologySchemaRef>, current: string): OntologySchemaRef[] {
  if (!current || schemas.some((s) => s.schema_id === current)) return [...schemas];
  return [...schemas, { schema_id: current, version: 0, active: false }];
}

// ---- pages of versions ------------------------------------------------------

/**
 * What the page holds of one schema's history. `versions` is always the
 * newest versions.length versions as of `total`, newest first, with no gap:
 * that is what lets a re-read after a live change and an older page each be
 * joined without a hole or a repeat.
 */
export interface VersionList {
  schema: string;
  versions: OntologyVersionInfo[];
  total: number;
  hasMore: boolean;
  exists: boolean;
  currentVersion: number;
}

export function listFromPage(page: OntologyHistoryResponse): VersionList {
  const versions = byVersionDesc(page.versions ?? []);
  return {
    schema: page.schema_id,
    versions,
    total: page.total,
    hasMore: versions.length < page.total,
    exists: page.exists,
    currentVersion: page.current_version ?? 0,
  };
}

function byVersionDesc(versions: ReadonlyArray<OntologyVersionInfo>): OntologyVersionInfo[] {
  return [...versions].sort((a, b) => b.version - a.version);
}

/** Both sets of versions, one entry per version, newest first; `fresher` wins where they overlap. */
function mergeVersions(older: ReadonlyArray<OntologyVersionInfo>, fresher: ReadonlyArray<OntologyVersionInfo>): OntologyVersionInfo[] {
  const m = new Map<number, OntologyVersionInfo>();
  for (const v of older) m.set(v.version, v);
  for (const v of fresher) m.set(v.version, v);
  return byVersionDesc(Array.from(m.values()));
}

/** Where the next older page starts. */
export function olderOffset(list: VersionList): number {
  return list.versions.length;
}

/**
 * An older page joined on. If k versions were saved since the list was read,
 * every offset moved down by k, so the page repeats the list's last k
 * versions: they are merged, not shown twice. The list keeps its own total,
 * because the join is still the newest versions as of that total; the re-read
 * the live change triggers brings in the k new ones and moves the total.
 */
export function appendOlder(list: VersionList, page: OntologyHistoryResponse): VersionList {
  const versions = mergeVersions(list.versions, page.versions ?? []);
  return { ...list, versions, hasMore: versions.length < list.total };
}

/** How many versions a re-read asks for: what is shown plus room for the ones just saved. */
export function refreshLimit(list: VersionList | null, pageSize = PAGE_SIZE): number {
  return Math.min(HISTORY_MAX_LIMIT, (list?.versions.length ?? 0) + pageSize);
}

/**
 * A re-read of the newest versions, taken after a live change, joined with
 * what the list holds. It refreshes what it covers (a version's activations
 * close when the next one takes over) and adds what was saved since. The list
 * grows by exactly the versions that arrived, so nothing the reader had
 * loaded falls off the end and "load older" goes on from the same place. If
 * more arrived than the re-read could hold, there would be a hole between it
 * and the old list, so the re-read alone is kept.
 */
export function applyRefresh(list: VersionList, page: OntologyHistoryResponse): VersionList {
  const fresh = listFromPage(page);
  if (list.schema !== fresh.schema) return fresh;
  const arrived = Math.max(0, fresh.total - list.total);
  if (fresh.versions.length < arrived) return fresh;
  const keep = list.versions.length + arrived;
  const versions = mergeVersions(list.versions, fresh.versions).slice(0, keep);
  return { ...fresh, versions, hasMore: versions.length < fresh.total };
}

export function newestVersion(list: VersionList | null): number {
  return list?.versions[0]?.version ?? 0;
}

/** Versions in `after` newer than anything in `before`: the ones a re-read brought in, newest first. */
export function arrivals(before: VersionList | null, after: VersionList | null): number[] {
  if (!before || !after || before.schema !== after.schema) return [];
  const newest = newestVersion(before);
  return after.versions.filter((v) => v.version > newest).map((v) => v.version);
}

/**
 * The name a version gave the schema when it differs from the name of the
 * version saved just before it: a rename is worth a word on its row, the same
 * name on every row is not. Empty where the version before is not loaded.
 */
export function renamedTo(versions: ReadonlyArray<OntologyVersionInfo>, i: number): string {
  const v = versions[i];
  const before = versions[i + 1];
  if (!v || !before || before.version !== v.version - 1) return "";
  return v.name !== before.name ? v.name : "";
}

export function findVersion(list: VersionList | null, version: number): OntologyVersionInfo | undefined {
  return list?.versions.find((v) => v.version === version);
}

// ---- the words a version says -----------------------------------------------

export type SummaryView =
  /** Saved before the history kept summaries. */
  | { kind: "unrecorded" }
  | { kind: "initial" }
  /** Saved again with the same definition. */
  | { kind: "unchanged" }
  /** The definition changed, but only in parts the diff does not compare. */
  | { kind: "uncounted" }
  | { kind: "changes"; total: number; counts: DiffCounts };

export function summaryView(s: OntologyChangeSummary | undefined | null): SummaryView {
  if (!s) return { kind: "unrecorded" };
  if (s.initial) return { kind: "initial" };
  if (!s.definition_changed) return { kind: "unchanged" };
  if (!s.changes) return { kind: "uncounted" };
  const counts = summaryCounts(s.by_kind, s.breaking);
  // A summary without a per-kind breakdown still has a total; say it as changes.
  if (counts.added + counts.removed + counts.changed === 0) counts.changed = s.changes;
  return { kind: "changes", total: s.changes, counts };
}

/** Why an activation ended, in words. A reason this page does not know is spelled out. */
export function endWords(reason: string | undefined, lang: Lang = "en"): string {
  if (!reason) return "";
  return VERSIONS_TEXT[lang].endWords[reason] ?? reason.replaceAll("_", " ");
}

export interface ActivationView {
  /** Unix milliseconds. */
  from: number;
  /** Unix milliseconds, or null while it is still the active validator. */
  to: number | null;
  activatedBy: string;
  ended: string;
  endedBy: string;
}

export function activationView(a: OntologyActivation, lang: Lang = "en"): ActivationView {
  const to = a.active_to ? Date.parse(a.active_to) : NaN;
  return {
    from: Date.parse(a.active_from),
    to: Number.isFinite(to) ? to : null,
    activatedBy: a.activated_by ?? "",
    ended: endWords(a.end_reason, lang),
    endedBy: a.ended_by ?? "",
  };
}

/**
 * Whether two instants fall in one minute, where a clock to the minute would
 * print the same time for both ends of an interval and the reader could not
 * tell it from no interval at all.
 */
export function sameMinute(a: number, b: number): boolean {
  return Math.floor(a / 60_000) === Math.floor(b / 60_000);
}

// ---- live changes ---------------------------------------------------------------

/**
 * The newest live change to a schema's row, as its seq, or 0 for none. Every
 * save writes the row, and so does activating another schema, which switches
 * this one off; so a change to the row is exactly when the history may have
 * moved. A null schema matches every schema, for the list of schemas itself.
 */
export function newestSchemaChange(changes: ReadonlyArray<Pick<ChangeItem, "seq" | "kind" | "id">>, schema: string | null): number {
  let newest = 0;
  for (const c of changes) {
    if (c.kind === "ontology_schema" && (schema === null || c.id === schema) && c.seq > newest) newest = c.seq;
  }
  return newest;
}

// ---- errors -------------------------------------------------------------------

/**
 * Whether a failed history read means the schema has no versions at all. The
 * route answers that with a 404 (the tool said the schema is not there), and
 * it is an empty state, not a failure. Read from the status, never from the
 * wording of the error, which is the tool's to change.
 */
export function isNoHistory(status: number | null | undefined): boolean {
  return status === 404;
}
