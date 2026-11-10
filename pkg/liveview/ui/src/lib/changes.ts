// Pure helpers for the change timeline: the words a row says, grouping by day,
// the filters and their round trip through the URL, and how live items and
// fetched pages are joined. Nothing here touches React or the network, so all
// of it is tested directly.

import type { ChangeItem, ChangesQuery } from "@/api";
import { mergeChangeItems } from "@/live/reducer";
import { FEED_TEXT } from "@/lib/feedStrings";
import { localeOf, type Lang } from "@/lib/i18n";

// ---- the words a row says ------------------------------------------------

/** What a raw history reason means in a sentence. Unknown codes are spelled out as they came. */
export function reasonWords(reason: string | undefined, lang: Lang): string {
  if (!reason) return "";
  return FEED_TEXT[lang].reasons[reason] ?? reason.replaceAll("_", " ");
}

/** "people v2" for a schema decision, "people" when no version was recorded. */
export function schemaLabel(schema: ChangeItem["schema"]): string {
  if (!schema) return "";
  return schema.version > 0 ? `${schema.id} v${schema.version}` : schema.id;
}

/**
 * The value part of another row's summary. A replaced edge is summarised as
 * "Leo lives_in Beijing"; next to "Leo lives_in Chengdu" only "Beijing" is news.
 */
export function valueOfSummary(summary: string, item: ChangeItem): string {
  const e = item.edge;
  if (e) {
    const prefix = `${e.source.label} ${e.predicate} `;
    if (summary.startsWith(prefix) && summary.length > prefix.length) return summary.slice(prefix.length);
  }
  const t = item.triple;
  if (t) {
    const prefix = `${t.subject} ${t.predicate} `;
    if (summary.startsWith(prefix) && summary.length > prefix.length) return summary.slice(prefix.length);
  }
  return summary;
}

export interface ReasonView {
  /** The sentence for the chip: "replaced Beijing". */
  text: string;
  /** The schema decision it was made under, "people v2", or "". */
  schema: string;
}

/**
 * Why a change happened, in words, or null when the row says it all (a plain
 * addition). The links to the other row stay in the details; this is the chip.
 */
export function reasonView(item: ChangeItem, lang: Lang): ReasonView | null {
  const t = FEED_TEXT[lang];
  const schema = schemaLabel(item.schema);
  let text = "";
  if (item.replaces) text = t.replaced(valueOfSummary(item.replaces.summary, item));
  else if (item.replaced_by) {
    const by = valueOfSummary(item.replaced_by.summary, item);
    text = item.op === "merged" ? t.mergedInto(by) : t.replacedBy(by);
  } else if (item.op === "retracted") text = t.takenBack;
  else if (item.op === "merged") text = t.merged;
  else if (item.reason && item.op !== "added") text = reasonWords(item.reason, lang);
  if (!text && !schema) return null;
  return { text, schema };
}

// ---- day grouping --------------------------------------------------------

export interface DayGroup<T> {
  /** yyyy-mm-dd in local time. */
  key: string;
  label: string;
  items: T[];
}

function dayKey(ms: number): string {
  const d = new Date(ms);
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${d.getFullYear()}-${m}-${day}`;
}

function startOfDay(ms: number): number {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

export function dayLabel(ms: number, now: number, lang: Lang): string {
  const days = Math.round((startOfDay(now) - startOfDay(ms)) / 86_400_000);
  if (days === 0) return FEED_TEXT[lang].today;
  if (days === 1) return FEED_TEXT[lang].yesterday;
  const sameYear = new Date(ms).getFullYear() === new Date(now).getFullYear();
  return new Date(ms).toLocaleDateString(localeOf(lang), {
    weekday: days < 7 ? "long" : undefined,
    month: "long",
    day: "numeric",
    year: sameYear ? undefined : "numeric",
  });
}

/** Consecutive items sharing a local calendar day; order is kept. */
export function groupByDay<T extends { at: number }>(items: ReadonlyArray<T>, now: number, lang: Lang): DayGroup<T>[] {
  const groups: DayGroup<T>[] = [];
  for (const item of items) {
    const key = dayKey(item.at);
    const last = groups[groups.length - 1];
    if (last && last.key === key) last.items.push(item);
    else groups.push({ key, label: dayLabel(item.at, now, lang), items: [item] });
  }
  return groups;
}

// ---- filters -------------------------------------------------------------

export interface ChangeFilters {
  kind: string[];
  op: string[];
  producer: string[];
  entity: string[];
}

export const FILTER_KEYS = ["kind", "op", "producer", "entity"] as const;
export type FilterKey = (typeof FILTER_KEYS)[number];

export const noFilters: ChangeFilters = { kind: [], op: [], producer: [], entity: [] };

export function hasFilters(f: ChangeFilters): boolean {
  return FILTER_KEYS.some((k) => f[k].length > 0);
}

function unique(values: string[]): string[] {
  return Array.from(new Set(values.filter((v) => v !== "")));
}

export function filtersFromParams(params: URLSearchParams): ChangeFilters {
  return {
    kind: unique(params.getAll("kind")),
    op: unique(params.getAll("op")),
    producer: unique(params.getAll("producer")),
    entity: unique(params.getAll("entity")),
  };
}

/** Write the filters onto a copy of `base`, leaving unrelated parameters alone. */
export function filtersToParams(f: ChangeFilters, base: URLSearchParams = new URLSearchParams()): URLSearchParams {
  const next = new URLSearchParams(base);
  for (const k of FILTER_KEYS) {
    next.delete(k);
    for (const v of unique(f[k])) next.append(k, v);
  }
  return next;
}

/** The server query for a filter set; unset filters are left out. */
export function filtersToQuery(f: ChangeFilters): ChangesQuery {
  const q: ChangesQuery = {};
  for (const k of FILTER_KEYS) if (f[k].length > 0) q[k] = f[k];
  return q;
}

/** Stable identity of a filter set, for effect dependencies. */
export function filtersKey(f: ChangeFilters): string {
  return FILTER_KEYS.map((k) => `${k}=${[...f[k]].sort().join(",")}`).join("&");
}

/** The same test the server applies: ORed within a filter, ANDed across filters. */
export function matchesFilters(item: ChangeItem, f: ChangeFilters): boolean {
  if (f.kind.length > 0 && !f.kind.includes(item.kind)) return false;
  if (f.op.length > 0 && !f.op.includes(item.op)) return false;
  if (f.producer.length > 0 && !(item.producer !== undefined && f.producer.includes(item.producer))) return false;
  if (f.entity.length > 0) {
    const touched = f.entity.some((id) => item.id === id || (item.entities ?? []).includes(id));
    if (!touched) return false;
  }
  return true;
}

export function toggleValue(values: string[], value: string): string[] {
  return values.includes(value) ? values.filter((v) => v !== value) : [...values, value];
}

// ---- joining live and fetched rows ---------------------------------------

/**
 * The rows to show: the fetched pages plus the live tail that passes the
 * filters, newest first. When older pages remain, live rows below the oldest
 * fetched one are dropped, so the list never has a hole that "load older"
 * would then fill out of order.
 */
export function mergeFeed(
  paged: ReadonlyArray<ChangeItem>,
  live: ReadonlyArray<ChangeItem>,
  filters: ChangeFilters,
  hasMore: boolean,
  limit = 10_000,
): ChangeItem[] {
  const liveMatches = hasFilters(filters) ? live.filter((c) => matchesFilters(c, filters)) : live;
  const all = mergeChangeItems(paged, liveMatches, limit);
  if (!hasMore || paged.length === 0) return [...all];
  const floor = Math.min(...paged.map((c) => c.seq));
  return all.filter((c) => c.seq >= floor);
}

/** Producers worth offering as filter options: those seen, then the selected. */
export function producersSeen(items: ReadonlyArray<ChangeItem>, selected: string[]): string[] {
  const set = new Set<string>(selected);
  for (const c of items) if (c.producer) set.add(c.producer);
  return Array.from(set).sort();
}
