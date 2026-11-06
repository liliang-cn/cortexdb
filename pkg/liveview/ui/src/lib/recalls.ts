// Pure helpers for the Recalls page: its URL options and their round trip,
// the timeline query they make, how a refreshed head joins the list, the
// marks, the golden export's file name, and the small readings of a recall
// (who asked, which candidates made it out). Nothing here touches React or
// the network, so all of it is tested directly.

import type { RecallEntry, RecallHit, RecallListQuery, RecallVerdict } from "@/api/recalls";
import type { Lang } from "@/lib/i18n";

// ---- URL options ---------------------------------------------------------

export type RecallsView = "timeline" | "records";

/**
 * What the page is showing, as the URL says it. The keys are the old page's
 * (?trace=, ?recall=, ?record_id=, ?view=), because docs and tool answers
 * link to them, plus the timeline's other filters so a filtered timeline can
 * be shared as a link.
 */
export interface RecallsOptions {
  view: RecallsView;
  /** The recall opened in the detail; "" for none. */
  recall: string;
  /** Keep recalls that returned this record. */
  record: string;
  /** Keep the recalls of one W3C trace: 32 lowercase hex, or "". */
  trace: string;
  /** A ?trace= that is not a trace id, kept so the page can say it ignored it. */
  badTrace: string;
  key: string;
  project: string;
  /** Keep recalls whose query contains this text. */
  contains: string;
  /** Keep only recalls that returned nothing. */
  empty: boolean;
}

/** The URL keys this page owns. Everything else (?lang=, ?mode=, ...) is left alone. */
export const OWN_KEYS = ["view", "recall", "record_id", "trace", "key", "project", "contains", "empty"] as const;

export const noOptions: RecallsOptions = {
  view: "timeline",
  recall: "",
  record: "",
  trace: "",
  badTrace: "",
  key: "",
  project: "",
  contains: "",
  empty: false,
};

const TRACE_ID = /^[0-9a-f]{32}$/;
const TRACEPARENT = /^[0-9a-f]{2}-([0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$/;

/**
 * A trace id from what a person pasted: the id itself, or a whole
 * traceparent header, in either case. The all-zero id is invalid in W3C
 * Trace Context, and the server refuses anything that is not 32 lowercase
 * hex, so both come back as null.
 */
export function parseTrace(raw: string | null | undefined): string | null {
  const s = (raw ?? "").trim().toLowerCase();
  const id = TRACE_ID.test(s) ? s : (TRACEPARENT.exec(s)?.[1] ?? null);
  if (!id || /^0+$/.test(id)) return null;
  return id;
}

export function optionsFromParams(p: URLSearchParams): RecallsOptions {
  const rawTrace = (p.get("trace") ?? "").trim();
  const trace = parseTrace(rawTrace);
  return {
    view: p.get("view") === "records" ? "records" : "timeline",
    recall: (p.get("recall") ?? "").trim(),
    record: (p.get("record_id") ?? "").trim(),
    trace: trace ?? "",
    badTrace: rawTrace && !trace ? rawTrace : "",
    key: (p.get("key") ?? "").trim(),
    project: (p.get("project") ?? "").trim(),
    contains: (p.get("contains") ?? "").trim(),
    empty: p.get("empty") === "1",
  };
}

/**
 * The URL for a set of options, keeping every parameter the page does not
 * own where it was. A timeline with no filters writes nothing, so the plain
 * /recalls stays plain.
 */
export function optionsToParams(o: RecallsOptions, current: URLSearchParams): URLSearchParams {
  const next = new URLSearchParams(current);
  for (const k of OWN_KEYS) next.delete(k);
  if (o.view === "records") next.set("view", "records");
  if (o.recall) next.set("recall", o.recall);
  if (o.record) next.set("record_id", o.record);
  if (o.trace) next.set("trace", o.trace);
  else if (o.badTrace) next.set("trace", o.badTrace);
  if (o.key) next.set("key", o.key);
  if (o.project) next.set("project", o.project);
  if (o.contains) next.set("contains", o.contains);
  if (o.empty) next.set("empty", "1");
  return next;
}

/** The filters that narrow the timeline, as one comparable string. */
export function filtersKey(o: RecallsOptions): string {
  return JSON.stringify([o.record, o.trace, o.key, o.project, o.contains, o.empty]);
}

export function hasFilters(o: RecallsOptions): boolean {
  return Boolean(o.record || o.trace || o.badTrace || o.key || o.project || o.contains || o.empty);
}

/** The same options with every timeline filter taken off; the view and the open recall stay. */
export function withoutFilters(o: RecallsOptions): RecallsOptions {
  return { ...noOptions, view: o.view, recall: o.recall };
}

/** The query a page of the timeline is read with. */
export function listQuery(o: RecallsOptions, before?: number, limit?: number): RecallListQuery {
  return {
    contains: o.contains || undefined,
    empty: o.empty || undefined,
    record_id: o.record || undefined,
    trace: o.trace || undefined,
    key: o.key || undefined,
    project: o.project || undefined,
    before: before || undefined,
    limit,
  };
}

// ---- links ----------------------------------------------------------------

/** The graph page lighting the nodes this recall returned (its ?replay=). */
export const replayPath = (recallId: string) => `/?replay=${encodeURIComponent(recallId)}`;

/** The graph page opened on one node (its ?focus=). */
export const focusPath = (nodeId: string) => `/?focus=${encodeURIComponent(nodeId)}`;

/** The graph shaded by how often recalls reached each node. */
export const heatPath = "/?color=recalls";

// ---- the timeline ---------------------------------------------------------

export interface HeadMerge {
  /** The list after the merge, newest first. */
  items: RecallEntry[];
  /** Recalls that were not in the list before, newest first. */
  added: RecallEntry[];
  /**
   * The fresh head shares nothing with the list and more lay beyond it: more
   * recalls arrived than one page holds, so the list was replaced rather
   * than joined across a hole.
   */
  gap: boolean;
}

/**
 * Join a freshly read first page onto the list. The list is newest first and
 * read under the same filters, so anything in the head the list does not
 * hold arrived since it was read.
 */
export function mergeHead(existing: ReadonlyArray<RecallEntry>, head: ReadonlyArray<RecallEntry>, headIsFull: boolean): HeadMerge {
  const have = new Set(existing.map((r) => r.id));
  const added = head.filter((r) => !have.has(r.id));
  if (added.length === 0) return { items: [...existing], added: [], gap: false };
  if (existing.length > 0 && added.length === head.length && headIsFull) {
    return { items: [...head], added, gap: true };
  }
  const items = added.concat(existing).sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
  return { items, added, gap: false };
}

/** The timeline as the page holds it between reads. */
export interface TimelineData {
  /** What the list shows, newest first. */
  items: RecallEntry[];
  /** Recalls that arrived while the reader was scrolled away from the top, newest first. */
  pending: RecallEntry[];
  /** Ids that arrived live, which flash once as they join the list. */
  fresh: ReadonlySet<string>;
  /** The last refresh had to replace the list (see HeadMerge.gap). */
  rebuilt: boolean;
  /** Where the next older page starts (the server's next_before); absent at the oldest. */
  nextBefore?: number;
}

export const emptyTimeline: TimelineData = { items: [], pending: [], fresh: new Set(), rebuilt: false };

const newestFirst = (a: RecallEntry, b: RecallEntry) => Date.parse(b.at) - Date.parse(a.at);

/**
 * A refreshed head arrives. New recalls join the top at once, or, while the
 * reader is scrolled down (`hold`), wait in `pending` so the rows under the
 * reader's eyes do not move.
 */
export function applyHead(d: TimelineData, head: ReadonlyArray<RecallEntry>, headNextBefore: number | undefined, hold: boolean): TimelineData {
  const m = mergeHead(d.pending.concat(d.items).sort(newestFirst), head, Boolean(headNextBefore));
  // A replaced list pages on from the head it was replaced with.
  if (m.gap) return { items: m.items, pending: [], fresh: new Set(), rebuilt: true, nextBefore: headNextBefore || undefined };
  if (m.added.length === 0) return d;
  if (hold) return { ...d, pending: d.pending.concat(m.added).sort(newestFirst) };
  return joinPending({ ...d, pending: d.pending.concat(m.added) });
}

/** Move whatever is pending into the list, marked fresh. */
export function joinPending(d: TimelineData): TimelineData {
  if (d.pending.length === 0) return d;
  const fresh = new Set(d.fresh);
  for (const r of d.pending) fresh.add(r.id);
  return { ...d, items: mergeHead(d.items, d.pending, false).items, pending: [], fresh };
}

/** Append an older page, leaving out any recall the list already holds. */
export function appendOlder(existing: ReadonlyArray<RecallEntry>, older: ReadonlyArray<RecallEntry>): RecallEntry[] {
  const have = new Set(existing.map((r) => r.id));
  return existing.concat(older.filter((r) => !have.has(r.id)));
}

/** An older page joins the bottom, and the cursor moves on to the page after it. */
export function applyOlder(d: TimelineData, older: ReadonlyArray<RecallEntry>, nextBefore: number | undefined): TimelineData {
  return { ...d, items: appendOlder(d.items, older), nextBefore: nextBefore || undefined };
}

/** Who asked, as the log has it: key, host, project, agent, whichever are known. */
export function whoOf(r: Pick<RecallEntry, "key" | "host" | "project" | "agent">): string {
  return [r.key, r.host, r.project, r.agent].filter(Boolean).join(" · ");
}

/** The first characters of a trace id, enough to tell the flows on one screen apart. */
export const shortTrace = (id: string) => id.slice(0, 8);

// ---- marks ----------------------------------------------------------------

/**
 * The verdict a press on the two-button mark sends. The toggle reports ""
 * when the pressed mark was already on, and taking a mark off is what the
 * server calls "clear".
 */
export function verdictFromToggle(value: string): RecallVerdict | "clear" {
  return value === "useful" || value === "noise" ? value : "clear";
}

/** The mark a hit carries, as the server names it; "clear" for none, or for one this page does not know. */
export function verdictOf(hit: Pick<RecallHit, "verdict">): RecallVerdict | "clear" {
  return hit.verdict === "useful" ? "useful" : hit.verdict === "noise" ? "noise" : "clear";
}

/** The recall with one hit's mark set, or taken off for "clear". Hits of other records are untouched. */
export function withVerdict(entry: RecallEntry, recordId: string, verdict: RecallVerdict | "clear"): RecallEntry {
  return {
    ...entry,
    hits: entry.hits.map((h): RecallHit => {
      if (h.id !== recordId) return h;
      const next: RecallHit = { ...h };
      if (verdict === "clear") delete next.verdict;
      else next.verdict = verdict;
      return next;
    }),
  };
}

/** How many hits carry each mark. */
export function markCounts(hits: ReadonlyArray<RecallHit>): { useful: number; noise: number } {
  let useful = 0;
  let noise = 0;
  for (const h of hits) {
    if (h.verdict === "useful") useful++;
    else if (h.verdict === "noise") noise++;
  }
  return { useful, noise };
}

// ---- explain --------------------------------------------------------------

/**
 * The hits that came out, as ids a candidate row can be matched against. A
 * knowledge retriever ranks chunks ("chunk:<document>:<n>") while the hit is
 * the document, so a chunk counts as its document.
 */
export function finalMatcher(hits: ReadonlyArray<Pick<RecallHit, "id">>): (candidateId: string) => boolean {
  const ids = new Set(hits.map((h) => h.id));
  return (candidateId) => {
    if (ids.has(candidateId)) return true;
    const m = /^chunk:(.+):[^:]+$/.exec(candidateId);
    return m ? ids.has(m[1] ?? "") : false;
  };
}

/** Retrievers in a stable order, so the tables do not swap places between recalls. */
export function retrieverNames(candidates: Record<string, unknown> | undefined): string[] {
  return Object.keys(candidates ?? {}).sort();
}

/** How many drops the record left out past the ones it kept. */
export function droppedBeyond(kept: number, totals: Record<string, number> | undefined): number {
  const all = Object.values(totals ?? {}).reduce((a, b) => a + b, 0);
  return Math.max(0, all - kept);
}

/** A score as the explain tables print it; "" for none. */
export function score(x: number | undefined | null, digits = 3): string {
  return typeof x === "number" && Number.isFinite(x) ? x.toFixed(digits) : "";
}

// ---- records view ---------------------------------------------------------

/** How many days the stats cover: from the start of retention to now, at least one. */
export function windowDays(since: string, now: number): number {
  const t = Date.parse(since);
  if (!Number.isFinite(t)) return 1;
  return Math.max(1, Math.round((now - t) / 86_400_000));
}

// ---- errors ---------------------------------------------------------------

/**
 * Whether a failed read means the brain keeps no recall log. The routes answer
 * that with a 409 (the brain is fine; the request needs a log it was not
 * started with), read from the status and never from the sentence, which the
 * page shows in its own words with the setting to turn it on.
 */
export function isLogOff(status: number | null | undefined): boolean {
  return status === 409;
}

// ---- golden export --------------------------------------------------------

/** How many cases a golden JSONL holds: one per non-empty line. */
export function countCases(jsonl: string): number {
  return jsonl.split("\n").filter((l) => l.trim() !== "").length;
}

/**
 * The file a golden export is saved as: which brain and which day, so two
 * exports side by side say where each came from. The brain is the last part
 * of the source's description (a file name without its extension, or an
 * address), reduced to something every file system takes.
 */
export function goldenFileName(source: string, now: Date): string {
  const last = source.trim().split(/[\\/]/).filter(Boolean).pop() ?? "";
  const stem = last.replace(/\.(db|sqlite3?)$/i, "");
  const slug = stem
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40)
    .replace(/-+$/g, "");
  const pad = (n: number) => String(n).padStart(2, "0");
  const day = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
  return `recall-feedback-golden-${slug || "brain"}-${day}.jsonl`;
}

// ---- time -----------------------------------------------------------------

export const localeOf = (lang: Lang) => (lang === "zh" ? "zh-CN" : "en");

const rtfs = new Map<Lang, Intl.RelativeTimeFormat>();
function rtf(lang: Lang): Intl.RelativeTimeFormat {
  let f = rtfs.get(lang);
  if (!f) {
    f = new Intl.RelativeTimeFormat(localeOf(lang), { numeric: "auto", style: "short" });
    rtfs.set(lang, f);
  }
  return f;
}

/** "3 min ago" or "3分钟前"; the date after a week. */
export function relativeTime(lang: Lang, ms: number, now: number): string {
  if (!Number.isFinite(ms)) return "";
  const diff = Math.round((ms - now) / 1000);
  const abs = Math.abs(diff);
  if (abs < 5) return lang === "zh" ? "刚刚" : "just now";
  if (abs < 60) return rtf(lang).format(diff, "second");
  if (abs < 3600) return rtf(lang).format(Math.round(diff / 60), "minute");
  if (abs < 86400) return rtf(lang).format(Math.round(diff / 3600), "hour");
  if (abs < 7 * 86400) return rtf(lang).format(Math.round(diff / 86400), "day");
  return new Date(ms).toLocaleDateString(localeOf(lang), { year: "numeric", month: "short", day: "numeric" });
}

/** The full local date and time, for a title or the detail. */
export function clockTime(lang: Lang, ms: number): string {
  return Number.isFinite(ms) ? new Date(ms).toLocaleString(localeOf(lang), { dateStyle: "medium", timeStyle: "medium" }) : "";
}
