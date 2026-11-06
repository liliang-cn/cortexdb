import { describe, expect, it } from "vitest";
import type { RecallEntry } from "@/api/recalls";
import {
  appendOlder,
  applyHead,
  applyOlder,
  countCases,
  droppedBeyond,
  emptyTimeline,
  filtersKey,
  finalMatcher,
  goldenFileName,
  hasFilters,
  isLogOff,
  joinPending,
  listQuery,
  markCounts,
  mergeHead,
  noOptions,
  optionsFromParams,
  optionsToParams,
  parseTrace,
  relativeTime,
  replayPath,
  retrieverNames,
  score,
  shortTrace,
  verdictFromToggle,
  verdictOf,
  whoOf,
  windowDays,
  withVerdict,
  withoutFilters,
  type TimelineData,
} from "./recalls";

const TRACE = "4bf92f3577b34da6a3ce929d0e0e4736";

function recall(id: string, at: string, extra: Partial<RecallEntry> = {}): RecallEntry {
  return { id, at, query: `q ${id}`, latency_ms: 1, hit_count: 0, hits: [], ...extra };
}

const params = (s: string) => new URLSearchParams(s);

describe("parseTrace", () => {
  it("takes a trace id as it is, in either case and with stray space", () => {
    expect(parseTrace(TRACE)).toBe(TRACE);
    expect(parseTrace(` ${TRACE.toUpperCase()} `)).toBe(TRACE);
  });
  it("takes the trace id out of a whole traceparent", () => {
    expect(parseTrace(`00-${TRACE}-00f067aa0ba902b7-01`)).toBe(TRACE);
  });
  it("refuses what the server would refuse", () => {
    expect(parseTrace("not-a-trace")).toBeNull();
    expect(parseTrace(TRACE.slice(1))).toBeNull();
    expect(parseTrace("0".repeat(32))).toBeNull();
    expect(parseTrace("")).toBeNull();
    expect(parseTrace(null)).toBeNull();
  });
});

describe("the page's URL options", () => {
  it("read ?trace= from the URL and keep the timeline to it", () => {
    const o = optionsFromParams(params(`trace=${TRACE}`));
    expect(o.trace).toBe(TRACE);
    expect(listQuery(o).trace).toBe(TRACE);
  });

  it("read every option the old page and the docs link to", () => {
    const o = optionsFromParams(params(`view=records&recall=r1&record_id=m-vip&trace=${TRACE}&lang=zh&mode=dark`));
    expect(o).toEqual({ ...noOptions, view: "records", recall: "r1", record: "m-vip", trace: TRACE });
  });

  it("read the timeline's other filters", () => {
    const o = optionsFromParams(params("key=hermes&project=superai&contains=%20VIP%20&empty=1"));
    expect(o).toMatchObject({ key: "hermes", project: "superai", contains: "VIP", empty: true });
    expect(listQuery(o, 1700000000000, 50)).toEqual({
      contains: "VIP",
      empty: true,
      record_id: undefined,
      trace: undefined,
      key: "hermes",
      project: "superai",
      before: 1700000000000,
      limit: 50,
    });
  });

  it("keep a ?trace= that is not a trace id aside, so the page can say so, and filter by nothing", () => {
    const o = optionsFromParams(params("trace=abc"));
    expect(o.trace).toBe("");
    expect(o.badTrace).toBe("abc");
    expect(listQuery(o).trace).toBeUndefined();
    expect(hasFilters(o)).toBe(true);
  });

  it("write a trace filter back into the URL", () => {
    const next = optionsToParams({ ...noOptions, trace: TRACE }, params("lang=zh"));
    expect(next.get("trace")).toBe(TRACE);
    expect(next.get("lang")).toBe("zh");
  });

  it("leave ?lang=, ?mode= and anything else they do not own where it was", () => {
    const next = optionsToParams({ ...noOptions, recall: "r2" }, params("mode=dark&recall=r1&trace=abc&lang=en&foo=1"));
    expect(next.toString()).toBe("mode=dark&lang=en&foo=1&recall=r2");
  });

  it("write nothing for the plain timeline, so /recalls stays plain", () => {
    expect(optionsToParams(noOptions, params("")).toString()).toBe("");
    expect(optionsToParams(noOptions, params(`view=timeline&trace=${TRACE}&empty=1`)).toString()).toBe("");
  });

  it("round-trip through the URL", () => {
    const o = { ...noOptions, view: "records" as const, recall: "r", record: "m", trace: TRACE, key: "k", project: "p", contains: "x y", empty: true };
    expect(optionsFromParams(optionsToParams(o, params("lang=zh")))).toEqual(o);
  });

  it("keep a bad trace in the URL until it is cleared", () => {
    const o = optionsFromParams(params("trace=abc"));
    expect(optionsToParams(o, params("trace=abc")).get("trace")).toBe("abc");
    expect(optionsToParams({ ...o, badTrace: "" }, params("trace=abc")).has("trace")).toBe(false);
  });

  it("take every filter off at once, keeping the view and the open recall", () => {
    const o = { ...noOptions, view: "records" as const, recall: "r", trace: TRACE, key: "k", empty: true };
    expect(withoutFilters(o)).toEqual({ ...noOptions, view: "records", recall: "r" });
    expect(hasFilters(withoutFilters(o))).toBe(false);
  });

  it("change their key only when a filter changes, not the view or the open recall", () => {
    const o = { ...noOptions, trace: TRACE };
    expect(filtersKey({ ...o, recall: "x", view: "records" })).toBe(filtersKey(o));
    expect(filtersKey({ ...o, empty: true })).not.toBe(filtersKey(o));
  });

  it("link a recall to the graph's replay", () => {
    expect(replayPath("a b/c")).toBe("/?replay=a%20b%2Fc");
  });
});

describe("the timeline", () => {
  const a = recall("a", "2026-11-06T09:00:03Z");
  const b = recall("b", "2026-11-06T09:00:02Z");
  const c = recall("c", "2026-11-06T09:00:01Z");
  const n1 = recall("n1", "2026-11-06T09:00:05Z");
  const n2 = recall("n2", "2026-11-06T09:00:04Z");

  it("puts recalls the head has and the list does not at the top", () => {
    const m = mergeHead([a, b, c], [n1, n2, a, b], true);
    expect(m.items.map((r) => r.id)).toEqual(["n1", "n2", "a", "b", "c"]);
    expect(m.added.map((r) => r.id)).toEqual(["n1", "n2"]);
    expect(m.gap).toBe(false);
  });

  it("replaces the list when the head shares nothing with it and more lies beyond", () => {
    const m = mergeHead([a, b], [n1, n2], true);
    expect(m.gap).toBe(true);
    expect(m.items.map((r) => r.id)).toEqual(["n1", "n2"]);
    // A short head that shares nothing is simply everything new.
    expect(mergeHead([a, b], [n1, n2], false).gap).toBe(false);
  });

  it("joins new recalls at once, marked to flash, when the reader is at the top", () => {
    const d: TimelineData = { ...emptyTimeline, items: [a, b] };
    const next = applyHead(d, [n1, a, b], undefined, false);
    expect(next.items.map((r) => r.id)).toEqual(["n1", "a", "b"]);
    expect([...next.fresh]).toEqual(["n1"]);
    expect(next.pending).toEqual([]);
  });

  it("holds new recalls while the reader is scrolled down, then joins them", () => {
    const d: TimelineData = { ...emptyTimeline, items: [a, b] };
    const held = applyHead(d, [n2, a, b], undefined, true);
    expect(held.items.map((r) => r.id)).toEqual(["a", "b"]);
    expect(held.pending.map((r) => r.id)).toEqual(["n2"]);
    const more = applyHead(held, [n1, n2, a], undefined, true);
    expect(more.pending.map((r) => r.id)).toEqual(["n1", "n2"]);
    const joined = joinPending(more);
    expect(joined.items.map((r) => r.id)).toEqual(["n1", "n2", "a", "b"]);
    expect(joined.pending).toEqual([]);
    expect(new Set(joined.fresh)).toEqual(new Set(["n1", "n2"]));
  });

  it("returns the same state when nothing arrived, so nothing re-renders", () => {
    const d: TimelineData = { ...emptyTimeline, items: [a, b] };
    expect(applyHead(d, [a, b], 99, false)).toBe(d);
    expect(joinPending(d)).toBe(d);
  });

  it("marks a replaced list as rebuilt and drops what it held", () => {
    const d: TimelineData = { items: [a], pending: [b], fresh: new Set(["b"]), rebuilt: false, nextBefore: 5 };
    const next = applyHead(d, [n1, n2], 1234, true);
    expect(next.rebuilt).toBe(true);
    expect(next.items.map((r) => r.id)).toEqual(["n1", "n2"]);
    expect(next.pending).toEqual([]);
    // Older pages now follow on from the head that replaced the list.
    expect(next.nextBefore).toBe(1234);
  });

  it("keeps the cursor where it was when new recalls join the top", () => {
    const d: TimelineData = { ...emptyTimeline, items: [a, b], nextBefore: 7 };
    expect(applyHead(d, [n1, a, b], 99, false).nextBefore).toBe(7);
  });

  it("appends an older page without a recall twice, and moves the cursor on", () => {
    expect(appendOlder([a, b], [b, c]).map((r) => r.id)).toEqual(["a", "b", "c"]);
    const d: TimelineData = { ...emptyTimeline, items: [a, b], nextBefore: 7 };
    const next = applyOlder(d, [b, c], 0);
    expect(next.items.map((r) => r.id)).toEqual(["a", "b", "c"]);
    expect(next.nextBefore).toBeUndefined();
  });

  it("says who asked with whatever the log knows", () => {
    expect(whoOf({ key: "claude-code", host: "macbook", project: "CortexDB", agent: "claude" })).toBe("claude-code · macbook · CortexDB · claude");
    expect(whoOf({ key: "hermes" })).toBe("hermes");
    expect(whoOf({})).toBe("");
  });

  it("shows a trace by its first characters", () => {
    expect(shortTrace(TRACE)).toBe("4bf92f35");
  });
});

describe("marks", () => {
  const entry = recall("r", "2026-11-06T09:00:00Z", {
    hits: [
      { kind: "memory", id: "m1", rank: 1, score: 0.5, verdict: "useful" },
      { kind: "knowledge", id: "d1", rank: 1, score: 0.4 },
    ],
  });

  it("send clear when the mark already on is pressed again", () => {
    expect(verdictFromToggle("useful")).toBe("useful");
    expect(verdictFromToggle("noise")).toBe("noise");
    expect(verdictFromToggle("")).toBe("clear");
  });

  it("read a hit's mark, and anything unknown as none", () => {
    expect(verdictOf({ verdict: "noise" })).toBe("noise");
    expect(verdictOf({})).toBe("clear");
    expect(verdictOf({ verdict: "maybe" })).toBe("clear");
  });

  it("change one hit only, and take a mark off for clear", () => {
    const noisy = withVerdict(entry, "d1", "noise");
    expect(noisy.hits.map((h) => h.verdict)).toEqual(["useful", "noise"]);
    expect(entry.hits[1]?.verdict).toBeUndefined();
    const cleared = withVerdict(noisy, "m1", "clear");
    expect("verdict" in (cleared.hits[0] ?? {})).toBe(false);
    expect(markCounts(cleared.hits)).toEqual({ useful: 0, noise: 1 });
  });
});

describe("explain", () => {
  it("counts a knowledge retriever's chunk as the document it came out as", () => {
    const isFinal = finalMatcher([{ id: "doc-ha" }, { id: "m-vip" }]);
    expect(isFinal("m-vip")).toBe(true);
    expect(isFinal("chunk:doc-ha:000")).toBe(true);
    expect(isFinal("chunk:doc-guide:000")).toBe(false);
    expect(isFinal("m-public")).toBe(false);
  });

  it("orders retrievers the same way every time", () => {
    expect(retrieverNames({ vector: [], keyword: [], graph: [] })).toEqual(["graph", "keyword", "vector"]);
    expect(retrieverNames(undefined)).toEqual([]);
  });

  it("counts the drops the record did not keep", () => {
    expect(droppedBeyond(40, { memory_keyword_floor: 45, expired: 2 })).toBe(7);
    expect(droppedBeyond(1, { keyword_one_word: 1 })).toBe(0);
    expect(droppedBeyond(0, undefined)).toBe(0);
  });

  it("prints scores to three places, and nothing for none", () => {
    expect(score(0.5476303810061516)).toBe("0.548");
    expect(score(undefined)).toBe("");
    expect(score(Number.NaN)).toBe("");
  });
});

describe("the records view", () => {
  it("covers the days from the start of retention, at least one", () => {
    const now = Date.parse("2026-11-06T09:00:00Z");
    expect(windowDays("2026-10-07T09:00:00Z", now)).toBe(30);
    expect(windowDays("2026-11-06T08:00:00Z", now)).toBe(1);
    expect(windowDays("not a date", now)).toBe(1);
  });
});

describe("errors", () => {
  it("knows the server's sentence for a log that is off", () => {
    expect(isLogOff(409)).toBe(true);
    expect(isLogOff(404)).toBe(false);
    expect(isLogOff(502)).toBe(false);
    expect(isLogOff(null)).toBe(false);
  });
});

describe("the golden export", () => {
  it("counts one case per line", () => {
    expect(countCases('{"id":"a"}\n{"id":"b"}\n')).toBe(2);
    expect(countCases("")).toBe(0);
    expect(countCases("\n\n")).toBe(0);
  });

  it("names the file for the brain and the day", () => {
    const day = new Date(2026, 10, 6, 17, 30);
    expect(goldenFileName("/Users/liliang/.cache/p2-recalls/brain.db", day)).toBe("recall-feedback-golden-brain-2026-11-06.jsonl");
    expect(goldenFileName("shared brain 192.168.123.253:47821", day)).toBe("recall-feedback-golden-shared-brain-192-168-123-253-47821-2026-11-06.jsonl");
    expect(goldenFileName("C:\\data\\Team Brain.sqlite", new Date(2026, 0, 2))).toBe("recall-feedback-golden-team-brain-2026-01-02.jsonl");
  });

  it("falls back to a plain name when the source says nothing usable", () => {
    expect(goldenFileName("", new Date(2026, 10, 6))).toBe("recall-feedback-golden-brain-2026-11-06.jsonl");
    expect(goldenFileName("/// 大脑 ///", new Date(2026, 10, 6))).toBe("recall-feedback-golden-brain-2026-11-06.jsonl");
  });

  it("keeps a long name short", () => {
    const name = goldenFileName("x".repeat(100), new Date(2026, 10, 6));
    expect(name).toBe(`recall-feedback-golden-${"x".repeat(40)}-2026-11-06.jsonl`);
  });
});

describe("relative time", () => {
  const now = Date.parse("2026-11-06T09:00:00Z");
  it("speaks the page's language", () => {
    expect(relativeTime("en", now - 2000, now)).toBe("just now");
    expect(relativeTime("zh", now - 2000, now)).toBe("刚刚");
    expect(relativeTime("en", now - 3 * 60_000, now)).toMatch(/3 min/);
    expect(relativeTime("zh", now - 3 * 60_000, now)).toMatch(/3\s*分钟前/);
  });
  it("says nothing for a time it cannot read", () => {
    expect(relativeTime("en", Number.NaN, now)).toBe("");
  });
});
