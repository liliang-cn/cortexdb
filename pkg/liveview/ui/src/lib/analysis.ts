// The Analysis page's arithmetic, kept apart from the drawing so it can be
// tested: which buckets a time axis has, which of them may be filled with a
// zero, where an axis puts its ticks, which series a chart names and which it
// folds into "other", how a number and a bucket are written, the page's state
// in the URL, and the table as CSV.

import type { AnalysisPoint, AnalysisQuery, AnalysisResult, AnalysisRow, BucketUnit, Measure, RowKind } from "@/api/analysis";
import { localeOf, type Lang } from "@/lib/i18n";

// --- buckets -----------------------------------------------------------------

/** The longest axis a chart fills. Past it the buckets that have data are drawn as they are. */
export const MAX_BUCKETS = 800;

const DAY_MS = 86_400_000;

/**
 * A bucket key as a UTC instant: 2026-10-03 for a day or a week (its Monday),
 * 2026-10 for a month, 2026 for a year. NaN for anything else. The key is a
 * calendar date already cut in the reader's zone by the server, so the
 * arithmetic here is on dates, never on local time.
 */
export function bucketTime(key: string, unit: BucketUnit): number {
  const m =
    unit === "year"
      ? /^(\d{4})$/.exec(key)
      : unit === "month"
        ? /^(\d{4})-(\d{2})$/.exec(key)
        : /^(\d{4})-(\d{2})-(\d{2})$/.exec(key);
  if (!m) return NaN;
  const month = m[2] ? Number(m[2]) - 1 : 0;
  const day = m[3] ? Number(m[3]) : 1;
  const t = Date.UTC(Number(m[1]), month, day);
  // Date.UTC rolls an impossible date over (month 13, day 40) instead of refusing it.
  const d = new Date(t);
  return d.getUTCMonth() === month && d.getUTCDate() === day ? t : NaN;
}

const pad = (n: number, width = 2) => String(n).padStart(width, "0");

function keyOf(t: number, unit: BucketUnit): string {
  const d = new Date(t);
  const y = pad(d.getUTCFullYear(), 4);
  if (unit === "year") return y;
  const m = pad(d.getUTCMonth() + 1);
  return unit === "month" ? `${y}-${m}` : `${y}-${m}-${pad(d.getUTCDate())}`;
}

function nextBucket(t: number, unit: BucketUnit): number {
  const d = new Date(t);
  if (unit === "year") return Date.UTC(d.getUTCFullYear() + 1, 0, 1);
  if (unit === "month") return Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1);
  return t + (unit === "week" ? 7 : 1) * DAY_MS;
}

/**
 * Every bucket from first to last, both included. Null when either end is not
 * a bucket of this unit, when they are out of order, or when the axis would
 * be longer than `max`: a chart then draws only the buckets it was given.
 */
export function bucketRange(first: string, last: string, unit: BucketUnit, max = MAX_BUCKETS): string[] | null {
  let t = bucketTime(first, unit);
  const end = bucketTime(last, unit);
  if (Number.isNaN(t) || Number.isNaN(end) || t > end) return null;
  const out: string[] = [];
  while (t <= end) {
    if (out.length >= max) return null;
    out.push(keyOf(t, unit));
    t = nextBucket(t, unit);
  }
  return out;
}

/**
 * The axis for a set of buckets: every bucket between the earliest and the
 * latest of them, or between `first` and `last` when the range is known.
 * When the axis cannot be filled, the buckets present, in order.
 */
export function axisBuckets(present: readonly string[], unit: BucketUnit, first?: string, last?: string): string[] {
  const sorted = [...new Set(present)].sort();
  const ends = [first, last, sorted[0], sorted[sorted.length - 1]].filter((k): k is string => Boolean(k)).sort();
  const lo = ends[0];
  const hi = ends[ends.length - 1];
  if (lo === undefined || hi === undefined) return [];
  return bucketRange(lo, hi, unit) ?? sorted;
}

/** One series of a chart: a value per bucket of the axis, null where there is none. */
export interface ChartSeries {
  key: string;
  values: (number | null)[];
}

/**
 * The points laid on an axis, one series per `series` name. A bucket with no
 * point is `missing`: 0 for a count, where nothing counted is zero, and null
 * for a measure, where nothing measured is not a value.
 */
export function layOut(points: readonly AnalysisPoint[], buckets: readonly string[], missing: 0 | null): ChartSeries[] {
  const index = new Map(buckets.map((b, i) => [b, i]));
  const by = new Map<string, (number | null)[]>();
  for (const p of points) {
    const i = index.get(p.bucket);
    if (i === undefined) continue;
    const key = p.series ?? "";
    let values = by.get(key);
    if (!values) {
      values = buckets.map(() => missing);
      by.set(key, values);
    }
    values[i] = (values[i] ?? 0) + p.value;
  }
  return [...by].map(([key, values]) => ({ key, values }));
}

const sum = (values: readonly (number | null)[]) => values.reduce<number>((a, v) => a + (v ?? 0), 0);

/** The key of the series the smaller ones are folded into. */
export const OTHER = "\u0000other";

/** How many series a chart names before the rest become one. Seven colours and a grey. */
export const MAX_SERIES = 7;

export interface Folded {
  series: ChartSeries[];
  /** How many series were folded into the last one, or left out when they cannot be added up. */
  folded: number;
}

/**
 * The largest `max` series by total, in that order, and the rest as one
 * "other" series when the values add up (a count, a sum). Where they do not
 * (an average, a percentile) the rest are left out and `folded` says how many.
 */
export function foldSeries(series: readonly ChartSeries[], additive: boolean, max = MAX_SERIES): Folded {
  const ranked = [...series].sort((a, b) => sum(b.values) - sum(a.values) || a.key.localeCompare(b.key));
  if (ranked.length <= max) return { series: ranked, folded: 0 };
  const kept = ranked.slice(0, max);
  const rest = ranked.slice(max);
  if (!additive) return { series: kept, folded: rest.length };
  const length = kept[0]?.values.length ?? 0;
  const values = Array.from({ length }, (_, i) => {
    let total: number | null = null;
    for (const s of rest) {
      const v = s.values[i];
      if (v !== null && v !== undefined) total = (total ?? 0) + v;
    }
    return total;
  });
  return { series: [...kept, { key: OTHER, values }], folded: rest.length };
}

// --- colours ----------------------------------------------------------------

/** The series colours, in the fixed order index.css defines them. */
export const SERIES_COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
  "var(--chart-6)",
  "var(--chart-7)",
] as const;
export const OTHER_COLOR = "var(--chart-other)";

/**
 * A colour for each series key. A key in `fixed` always takes the slot named
 * there, so a write kind or a scope is the same colour whichever of them a
 * range happens to hold; the rest take the free slots in the order given.
 */
export function seriesColors(keys: readonly string[], fixed: Readonly<Record<string, number>> = {}): Map<string, string> {
  const out = new Map<string, string>();
  const taken = new Set<number>();
  for (const key of keys) {
    const slot = fixed[key];
    if (slot !== undefined && slot < SERIES_COLORS.length && !taken.has(slot)) {
      out.set(key, SERIES_COLORS[slot] as string);
      taken.add(slot);
    }
  }
  let next = 0;
  for (const key of keys) {
    if (out.has(key)) continue;
    if (key === OTHER) {
      out.set(key, OTHER_COLOR);
      continue;
    }
    while (taken.has(next)) next++;
    out.set(key, SERIES_COLORS[next] ?? OTHER_COLOR);
    taken.add(next);
  }
  return out;
}

// --- axes --------------------------------------------------------------------

/**
 * Round ticks from 0 up to at least `max`: steps of 1, 2 or 5 times a power
 * of ten, about `want` of them. Counts never get a fractional step.
 */
export function niceTicks(max: number, want = 4, whole = false): number[] {
  if (!Number.isFinite(max) || max <= 0) return [0, 1];
  const rough = max / Math.max(1, want);
  const power = 10 ** Math.floor(Math.log10(rough));
  const unit = rough / power;
  let step = (unit <= 1 ? 1 : unit <= 2 ? 2 : unit <= 5 ? 5 : 10) * power;
  if (whole && step < 1) step = 1;
  const ticks: number[] = [];
  // Multiplying an index avoids 0.1 + 0.2: every tick is one rounding away from exact.
  for (let i = 0; i * step < max + step - step * 1e-9; i++) ticks.push(Number((i * step).toPrecision(12)));
  return ticks;
}

/**
 * Which of `count` positions carry a label when only `fit` labels fit: evenly
 * stepped from the first, so the labels never collide and the spacing is
 * regular. The last position is not forced in, since a label squeezed against
 * its neighbour is worse than an unlabelled last bar.
 */
export function labelSlots(count: number, fit: number): number[] {
  if (count <= 0) return [];
  const step = Math.max(1, Math.ceil(count / Math.max(1, fit)));
  const out: number[] = [];
  for (let i = 0; i < count; i += step) out.push(i);
  return out;
}

// --- numbers and dates -------------------------------------------------------

/** A number as it is read: grouped thousands, and no more decimals than its size needs. */
export function formatNumber(value: number, lang: Lang): string {
  const abs = Math.abs(value);
  const digits = Number.isInteger(value) || abs >= 1000 ? 0 : abs >= 100 ? 1 : abs >= 1 ? 2 : 3;
  return new Intl.NumberFormat(localeOf(lang), { maximumFractionDigits: digits }).format(value);
}

/** A number on an axis, where width is scarce: 12,400 is 12.4K. */
export function formatTick(value: number, lang: Lang): string {
  if (Math.abs(value) < 10_000) return formatNumber(value, lang);
  return new Intl.NumberFormat(localeOf(lang), { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

/** Milliseconds, in seconds once they are long enough to be read as seconds. */
export function formatMs(ms: number, lang: Lang): string {
  if (Math.abs(ms) >= 10_000) return `${new Intl.NumberFormat(localeOf(lang), { maximumFractionDigits: 1 }).format(ms / 1000)} s`;
  return `${new Intl.NumberFormat(localeOf(lang), { maximumFractionDigits: ms < 10 ? 1 : 0 }).format(ms)} ms`;
}

/** A share as a percentage; an empty whole has no share. */
export function formatShare(part: number, whole: number, lang: Lang): string {
  if (whole <= 0) return "";
  return new Intl.NumberFormat(localeOf(lang), { style: "percent", maximumFractionDigits: 1 }).format(part / whole);
}

/**
 * A bucket in the reader's language. `long` is the tooltip's and the table's
 * spelling, with the year; the short one is the axis's.
 */
export function bucketLabel(key: string, unit: BucketUnit, lang: Lang, long = false): string {
  const t = bucketTime(key, unit);
  if (Number.isNaN(t) || unit === "year") return key;
  const options: Intl.DateTimeFormatOptions =
    unit === "month"
      ? { year: "numeric", month: "short" }
      : long
        ? { year: "numeric", month: "short", day: "numeric" }
        : { month: "short", day: "numeric" };
  return new Intl.DateTimeFormat(localeOf(lang), { ...options, timeZone: "UTC" }).format(new Date(t));
}

// --- the page's state in the URL ---------------------------------------------

export type AnalysisView = "overview" | "explore";

/** The ranges the overview offers, in days; 0 is everything. */
export const RANGES = [7, 30, 90, 0] as const;
export type RangeDays = (typeof RANGES)[number];
export const DEFAULT_RANGE: RangeDays = 30;

export const MEASURES: readonly Measure[] = ["count", "sum", "avg", "median", "p95", "min", "max"];
export const UNITS: readonly BucketUnit[] = ["day", "week", "month", "year"];

/** The shape of a metadata field name the server takes. */
export const FIELD_NAME = /^[A-Za-z_][A-Za-z0-9_.-]{0,63}$/;
const DATE = /^\d{4}-\d{2}-\d{2}$/;

export interface AnalysisState {
  view: AnalysisView;
  range: RangeDays;
  query: Required<Omit<AnalysisQuery, "timezone">>;
}

export const DEFAULT_QUERY: AnalysisState["query"] = {
  of: "documents",
  collection: "",
  measure: "count",
  field: "",
  group: "",
  bucket: "",
  bucket_field: "",
  since: "",
  until: "",
};

const name = (v: string | null) => (v && FIELD_NAME.test(v) ? v : "");
const date = (v: string | null) => (v && DATE.test(v) && !Number.isNaN(bucketTime(v, "day")) ? v : "");

/** The page's state as the query string spells it. What is not a value the page offers is its default. */
export function readState(params: URLSearchParams): AnalysisState {
  const range = params.get("range");
  const days = range === "all" ? 0 : range !== null && /^\d+$/.test(range) ? Number(range) : NaN;
  const measure = params.get("measure") as Measure;
  const bucket = params.get("bucket") as BucketUnit;
  const of = params.get("of") as RowKind;
  const query: AnalysisState["query"] = {
    of: of === "chunks" ? "chunks" : "documents",
    collection: (params.get("collection") ?? "").slice(0, 128),
    measure: MEASURES.includes(measure) ? measure : "count",
    field: name(params.get("field")),
    group: name(params.get("group")),
    bucket: UNITS.includes(bucket) ? bucket : "",
    bucket_field: name(params.get("bucket_field")),
    since: date(params.get("since")),
    until: date(params.get("until")),
  };
  if (query.measure === "count") query.field = "";
  if (!query.bucket) query.bucket_field = "";
  return {
    view: params.get("view") === "explore" ? "explore" : "overview",
    range: range !== null && (RANGES as readonly number[]).includes(days) ? (days as RangeDays) : DEFAULT_RANGE,
    query,
  };
}

const OWN = ["view", "range", "of", "collection", "measure", "field", "group", "bucket", "bucket_field", "since", "until"];

/**
 * The state written back over the current query string. Defaults are left
 * out, so a plain link is a plain page, and parameters that are not this
 * page's (the language, the theme) are kept.
 */
export function writeState(state: AnalysisState, current: URLSearchParams): URLSearchParams {
  const out = new URLSearchParams(current);
  for (const key of OWN) out.delete(key);
  if (state.view !== "overview") out.set("view", state.view);
  if (state.range !== DEFAULT_RANGE) out.set("range", state.range === 0 ? "all" : String(state.range));
  const q = state.query;
  const set = (key: string, value: string, fallback = "") => {
    if (value !== fallback) out.set(key, value);
  };
  set("of", q.of, DEFAULT_QUERY.of);
  set("collection", q.collection);
  set("measure", q.measure, DEFAULT_QUERY.measure);
  if (q.measure !== "count") set("field", q.field);
  set("group", q.group);
  set("bucket", q.bucket);
  if (q.bucket) set("bucket_field", q.bucket_field);
  set("since", q.since);
  set("until", q.until);
  return out;
}

/** Why a question cannot be asked yet, or null when it can. */
export function incomplete(q: AnalysisState["query"]): "field" | "range" | null {
  if (q.measure !== "count" && !q.field) return "field";
  if (q.since && q.until && q.until < q.since) return "range";
  return null;
}

/** A measure whose groups add up: the rest of them can be one "other". */
export const additive = (m: Measure) => m === "count" || m === "sum";

// --- the builder's answer ----------------------------------------------------

/** What the answer is drawn as. */
export type ResultShape = "number" | "categories" | "time" | "time-series";

export function shapeOf(q: Pick<AnalysisQuery, "group" | "bucket">): ResultShape {
  if (q.bucket && q.group) return "time-series";
  if (q.bucket) return "time";
  return q.group ? "categories" : "number";
}

/** The points of a bucketed answer. Rows whose time could not be read have no place on an axis. */
export function resultPoints(rows: readonly AnalysisRow[], noGroup: string): AnalysisPoint[] {
  const out: AnalysisPoint[] = [];
  for (const r of rows) {
    if (typeof r.bucket !== "string" || r.value === null) continue;
    out.push({ bucket: r.bucket, series: r.group === undefined ? "" : (r.group ?? noGroup), value: r.value });
  }
  return out;
}

export interface Category {
  key: string;
  value: number;
  rows: number;
}

/**
 * The groups of an ungrouped-in-time answer, largest value first, as at most
 * `max` bars. The rest are added into one bar when the measure adds up and
 * counted in `left` when it does not.
 */
export function categories(rows: readonly AnalysisRow[], additiveMeasure: boolean, noGroup: string, max = 12): { bars: Category[]; left: number } {
  const all = rows
    .filter((r): r is AnalysisRow & { value: number } => r.value !== null)
    .map((r) => ({ key: r.group ?? noGroup, value: r.value, rows: r.rows }))
    .sort((a, b) => b.value - a.value || a.key.localeCompare(b.key));
  if (all.length <= max) return { bars: all, left: 0 };
  const bars = all.slice(0, max);
  const rest = all.slice(max);
  if (!additiveMeasure) return { bars, left: rest.length };
  bars.push({ key: OTHER, value: rest.reduce((a, r) => a + r.value, 0), rows: rest.reduce((a, r) => a + r.rows, 0) });
  return { bars, left: rest.length };
}

/** The column the measure has in the table and the CSV: count, avg_score, p95_latency_ms. */
export function measureColumn(q: Pick<AnalysisQuery, "measure" | "field">): string {
  return q.measure === "count" || !q.field ? "count" : `${q.measure}_${q.field}`;
}

/** The table's columns for a question, in order. */
export function columnsOf(q: AnalysisQuery): string[] {
  const out: string[] = [];
  if (q.bucket) out.push(q.bucket_field ? `${q.bucket}_${q.bucket_field}` : q.bucket);
  if (q.group) out.push(q.group);
  if (q.measure !== "count") out.push(measureColumn(q));
  out.push("rows");
  if (q.measure !== "count") out.push("skipped");
  return out;
}

/**
 * One cell of CSV. A cell is quoted when it holds a comma, a quote or a line
 * break. Text that a spreadsheet would run as a formula (it starts with =, +,
 * - or @) is written with a leading apostrophe, which is the spreadsheet's
 * own way of saying "this is text".
 */
export function csvCell(value: string | number | null, text = false): string {
  if (value === null) return "";
  let s = String(value);
  if (text && /^[=+\-@\t\r]/.test(s)) s = `'${s}`;
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

/** The answer as CSV: the table's columns, bucket keys as the server wrote them, numbers unformatted. */
export function toCSV(result: AnalysisResult): string {
  const q = result.query;
  const lines = [columnsOf(q).map((c) => csvCell(c, true)).join(",")];
  for (const r of result.rows) {
    const cells: string[] = [];
    if (q.bucket) cells.push(csvCell(r.bucket ?? null, true));
    if (q.group) cells.push(csvCell(r.group ?? null, true));
    if (q.measure !== "count") cells.push(csvCell(r.value));
    cells.push(csvCell(r.rows));
    if (q.measure !== "count") cells.push(csvCell(r.skipped ?? 0));
    lines.push(cells.join(","));
  }
  return `${lines.join("\r\n")}\r\n`;
}

/** A file name for the download: what was asked, and the day. */
export function csvFileName(q: AnalysisQuery, now: Date): string {
  const parts = ["cortexdb", q.of, measureColumn(q), q.group, q.bucket].filter(Boolean).join("-");
  return `${parts.replace(/[^A-Za-z0-9_.-]+/g, "_")}-${now.toISOString().slice(0, 10)}.csv`;
}
