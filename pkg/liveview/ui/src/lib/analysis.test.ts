import { describe, expect, it } from "vitest";
import type { AnalysisResult } from "@/api/analysis";
import {
  DEFAULT_QUERY,
  MAX_SERIES,
  OTHER,
  OTHER_COLOR,
  SERIES_COLORS,
  axisBuckets,
  bucketLabel,
  bucketRange,
  bucketTime,
  categories,
  columnsOf,
  csvCell,
  csvFileName,
  foldSeries,
  formatMs,
  formatNumber,
  formatShare,
  formatTick,
  incomplete,
  labelSlots,
  layOut,
  niceTicks,
  readState,
  resultPoints,
  seriesColors,
  shapeOf,
  toCSV,
  writeState,
} from "./analysis";

describe("buckets", () => {
  it("reads each unit's key and refuses what is not one", () => {
    expect(bucketTime("2026-10-03", "day")).toBe(Date.UTC(2026, 9, 3));
    expect(bucketTime("2026-10", "month")).toBe(Date.UTC(2026, 9, 1));
    expect(bucketTime("2026", "year")).toBe(Date.UTC(2026, 0, 1));
    for (const [key, unit] of [["2026-10", "day"], ["2026-13-01", "day"], ["2026-02-30", "day"], ["2026-10-03", "month"], ["", "year"], ["soon", "week"]] as const) {
      expect(bucketTime(key, unit), `${key} as ${unit}`).toBeNaN();
    }
  });

  it("lists every bucket between two, across month and year ends", () => {
    expect(bucketRange("2026-02-27", "2026-03-02", "day")).toEqual(["2026-02-27", "2026-02-28", "2026-03-01", "2026-03-02"]);
    expect(bucketRange("2028-02-28", "2028-03-01", "day")).toEqual(["2028-02-28", "2028-02-29", "2028-03-01"]);
    expect(bucketRange("2026-12-21", "2027-01-11", "week")).toEqual(["2026-12-21", "2026-12-28", "2027-01-04", "2027-01-11"]);
    expect(bucketRange("2026-11", "2027-02", "month")).toEqual(["2026-11", "2026-12", "2027-01", "2027-02"]);
    expect(bucketRange("2024", "2026", "year")).toEqual(["2024", "2025", "2026"]);
    expect(bucketRange("2026-10-03", "2026-10-03", "day")).toEqual(["2026-10-03"]);
  });

  it("is not thrown by a clock change: the days around one are still one apart", () => {
    // 2026-03-29 is the day Europe's clocks move; the keys are dates, not local instants.
    expect(bucketRange("2026-03-28", "2026-03-30", "day")).toEqual(["2026-03-28", "2026-03-29", "2026-03-30"]);
  });

  it("gives up on an axis that is backwards, malformed or too long", () => {
    expect(bucketRange("2026-10-05", "2026-10-03", "day")).toBeNull();
    expect(bucketRange("2026-10", "2026-10-03", "day")).toBeNull();
    expect(bucketRange("0001-01-01", "2026-10-03", "day")).toBeNull();
    expect(bucketRange("2026-01-01", "2026-01-10", "day", 5)).toBeNull();
  });

  it("builds an axis over the known range, or over the data when there is none", () => {
    expect(axisBuckets(["2026-10-03"], "day", "2026-10-01", "2026-10-04")).toEqual(["2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04"]);
    expect(axisBuckets(["2026-10-05", "2026-10-03", "2026-10-03"], "day")).toEqual(["2026-10-03", "2026-10-04", "2026-10-05"]);
    // Data outside the stated range widens the axis; it is never dropped.
    expect(axisBuckets(["2026-09-30"], "day", "2026-10-01", "2026-10-02")).toEqual(["2026-09-30", "2026-10-01", "2026-10-02"]);
    expect(axisBuckets([], "day")).toEqual([]);
    expect(axisBuckets([], "month", "2026-09", "2026-11")).toEqual(["2026-09", "2026-10", "2026-11"]);
    // An axis that cannot be filled is the buckets that have data, in order.
    expect(axisBuckets(["2026-10-03", "0001-01-01"], "day")).toEqual(["0001-01-01", "2026-10-03"]);
  });
});

describe("laying points on an axis", () => {
  const axis = ["2026-10-01", "2026-10-02", "2026-10-03"];
  const points = [
    { bucket: "2026-10-01", series: "a", value: 2 },
    { bucket: "2026-10-03", series: "a", value: 1 },
    { bucket: "2026-10-03", series: "b", value: 4 },
    { bucket: "2026-11-09", series: "b", value: 9 },
  ];

  it("fills a count's empty buckets with zero", () => {
    expect(layOut(points, axis, 0)).toEqual([
      { key: "a", values: [2, 0, 1] },
      { key: "b", values: [0, 0, 4] },
    ]);
  });

  it("leaves a measure's empty buckets empty, since nothing measured is not zero", () => {
    expect(layOut(points, axis, null)).toEqual([
      { key: "a", values: [2, null, 1] },
      { key: "b", values: [null, null, 4] },
    ]);
  });

  it("puts points without a series in one, and keeps a real zero a zero", () => {
    expect(layOut([{ bucket: "2026-10-02", value: 0 }], axis, null)).toEqual([{ key: "", values: [null, 0, null] }]);
  });
});

describe("folding series", () => {
  const many = Array.from({ length: 10 }, (_, i) => ({ key: `s${i}`, values: [i + 1, null] as (number | null)[] }));

  it("keeps the largest and adds the rest into one when the values add up", () => {
    const { series, folded } = foldSeries(many, true);
    expect(series.map((s) => s.key)).toEqual(["s9", "s8", "s7", "s6", "s5", "s4", "s3", OTHER]);
    expect(series).toHaveLength(MAX_SERIES + 1);
    expect(folded).toBe(3);
    // 1 + 2 + 3, and a bucket none of them had stays empty instead of becoming 0.
    expect(series[MAX_SERIES]?.values).toEqual([6, null]);
  });

  it("leaves the rest out when the values cannot be added, and says how many", () => {
    const { series, folded } = foldSeries(many, false);
    expect(series).toHaveLength(MAX_SERIES);
    expect(series.some((s) => s.key === OTHER)).toBe(false);
    expect(folded).toBe(3);
  });

  it("does nothing to a chart that already fits, but orders it largest first", () => {
    const { series, folded } = foldSeries([{ key: "small", values: [1] }, { key: "big", values: [5] }], true);
    expect(series.map((s) => s.key)).toEqual(["big", "small"]);
    expect(folded).toBe(0);
  });
});

describe("series colours", () => {
  it("takes the slots in order and never hands one out twice", () => {
    const colors = seriesColors(["a", "b", "c"]);
    expect([...colors.values()]).toEqual(SERIES_COLORS.slice(0, 3));
  });

  it("keeps an entity on its own slot whatever else is drawn", () => {
    const slots = { memory: 0, knowledge: 1, node: 2 };
    expect(seriesColors(["node"], slots).get("node")).toBe(SERIES_COLORS[2]);
    const mixed = seriesColors(["x", "node", "memory"], slots);
    expect(mixed.get("memory")).toBe(SERIES_COLORS[0]);
    expect(mixed.get("node")).toBe(SERIES_COLORS[2]);
    // The newcomer takes the first slot nobody owns.
    expect(mixed.get("x")).toBe(SERIES_COLORS[1]);
  });

  it("draws the folded rest in grey, and anything past the last slot too", () => {
    const keys = [...Array.from({ length: 9 }, (_, i) => `s${i}`), OTHER];
    const colors = seriesColors(keys);
    expect(colors.get(OTHER)).toBe(OTHER_COLOR);
    expect(colors.get("s6")).toBe(SERIES_COLORS[6]);
    expect(colors.get("s7")).toBe(OTHER_COLOR);
    expect(new Set(SERIES_COLORS).size).toBe(SERIES_COLORS.length);
  });
});

describe("axis ticks", () => {
  it("are round and reach the largest value", () => {
    expect(niceTicks(7)).toEqual([0, 2, 4, 6, 8]);
    expect(niceTicks(100)).toEqual([0, 50, 100]);
    expect(niceTicks(101)).toEqual([0, 50, 100, 150]);
    expect(niceTicks(1234)).toEqual([0, 500, 1000, 1500]);
    expect(niceTicks(0.37)).toEqual([0, 0.1, 0.2, 0.3, 0.4]);
  });

  it("never step by a fraction for a count", () => {
    expect(niceTicks(1, 4, true)).toEqual([0, 1]);
    expect(niceTicks(3, 4, true)).toEqual([0, 1, 2, 3]);
    expect(niceTicks(1)).toEqual([0, 0.5, 1]);
  });

  it("give a flat or empty chart an axis all the same", () => {
    expect(niceTicks(0)).toEqual([0, 1]);
    expect(niceTicks(NaN)).toEqual([0, 1]);
    expect(niceTicks(-5)).toEqual([0, 1]);
  });

  it("carry no floating-point dust", () => {
    for (const t of niceTicks(0.7)) expect(String(t).length).toBeLessThan(5);
  });
});

describe("axis labels", () => {
  it("label every bucket when they fit and every nth when they do not", () => {
    expect(labelSlots(7, 10)).toEqual([0, 1, 2, 3, 4, 5, 6]);
    expect(labelSlots(30, 6)).toEqual([0, 5, 10, 15, 20, 25]);
    expect(labelSlots(30, 0)).toEqual([0]);
    expect(labelSlots(0, 5)).toEqual([]);
  });

  it("never asks for more labels than fit", () => {
    for (const [count, fit] of [[13, 4], [90, 7], [365, 9], [5, 5]] as const) {
      expect(labelSlots(count, fit).length, `${count} in ${fit}`).toBeLessThanOrEqual(fit);
    }
  });
});

describe("numbers", () => {
  it("group thousands and keep only the decimals a size needs", () => {
    expect(formatNumber(1234567, "en")).toBe("1,234,567");
    expect(formatNumber(1234.56, "en")).toBe("1,235");
    expect(formatNumber(123.456, "en")).toBe("123.5");
    expect(formatNumber(12.345, "en")).toBe("12.35");
    expect(formatNumber(0.12345, "en")).toBe("0.123");
    expect(formatNumber(0, "en")).toBe("0");
    expect(formatNumber(1234567, "zh")).toBe("1,234,567");
  });

  it("shorten on an axis only once a number is long", () => {
    expect(formatTick(9500, "en")).toBe("9,500");
    expect(formatTick(12400, "en")).toBe("12.4K");
    expect(formatTick(20000, "zh")).toBe("2万");
  });

  it("write milliseconds with their unit, and seconds once they are long", () => {
    expect(formatMs(3.25, "en")).toBe("3.3 ms");
    expect(formatMs(182.4, "en")).toBe("182 ms");
    expect(formatMs(1500, "en")).toBe("1,500 ms");
    expect(formatMs(12340, "en")).toBe("12.3 s");
  });

  it("write a share, and none of an empty whole", () => {
    expect(formatShare(1, 8, "en")).toBe("12.5%");
    expect(formatShare(0, 8, "en")).toBe("0%");
    expect(formatShare(0, 0, "en")).toBe("");
  });
});

describe("bucket labels", () => {
  it("are written in the reader's language, with the year only where it is needed", () => {
    expect(bucketLabel("2026-11-09", "day", "en")).toBe("Nov 9");
    expect(bucketLabel("2026-11-09", "week", "en", true)).toBe("Nov 9, 2026");
    expect(bucketLabel("2026-11-09", "day", "zh")).toBe("11月9日");
    expect(bucketLabel("2026-11", "month", "en")).toBe("Nov 2026");
    expect(bucketLabel("2026-11", "month", "zh")).toBe("2026年11月");
    expect(bucketLabel("2026", "year", "en")).toBe("2026");
  });

  it("show a key they cannot read as it is", () => {
    expect(bucketLabel("someday", "day", "en")).toBe("someday");
  });
});

describe("the page's state in the address", () => {
  const read = (qs: string) => readState(new URLSearchParams(qs));

  it("is the overview of the last 30 days when the address says nothing", () => {
    expect(read("")).toEqual({ view: "overview", range: 30, query: DEFAULT_QUERY });
    expect(writeState(read(""), new URLSearchParams()).toString()).toBe("");
  });

  it("round-trips a whole question", () => {
    const qs = "view=explore&range=90&of=chunks&collection=notes&measure=p95&field=latency_ms&group=kind&bucket=week&bucket_field=opened_at&since=2026-10-01&until=2026-10-31";
    const state = read(qs);
    expect(state.query).toEqual({
      of: "chunks", collection: "notes", measure: "p95", field: "latency_ms", group: "kind",
      bucket: "week", bucket_field: "opened_at", since: "2026-10-01", until: "2026-10-31",
    });
    expect(state.view).toBe("explore");
    expect(state.range).toBe(90);
    expect(writeState(state, new URLSearchParams()).toString()).toBe(qs);
  });

  it("spells all time as a word, and reads it back", () => {
    expect(read("range=all").range).toBe(0);
    expect(writeState({ ...read(""), range: 0 }, new URLSearchParams()).get("range")).toBe("all");
  });

  it("falls back to the default for what the page does not offer", () => {
    const state = read("view=settings&range=13&of=memories&measure=drop&bucket=hour&since=tomorrow&until=2026-02-30&group=a%20b&field=x%27--");
    expect(state).toEqual({ view: "overview", range: 30, query: DEFAULT_QUERY });
    expect(read("range=").range).toBe(30);
  });

  it("drops what a question does not use", () => {
    // A count reads no field, and a time field means nothing without a bucket.
    expect(read("measure=count&field=score&bucket_field=opened_at").query).toEqual(DEFAULT_QUERY);
    const written = writeState({ ...read(""), query: { ...DEFAULT_QUERY, field: "score", bucket_field: "at" } }, new URLSearchParams());
    expect(written.toString()).toBe("");
  });

  it("keeps the parameters that are not its own, and clears its own stale ones", () => {
    const current = new URLSearchParams("lang=zh&mode=dark&group=old&since=2026-01-01");
    const next = writeState({ ...read("lang=zh"), query: { ...DEFAULT_QUERY, group: "kind" } }, current);
    expect(next.get("lang")).toBe("zh");
    expect(next.get("mode")).toBe("dark");
    expect(next.get("group")).toBe("kind");
    expect(next.has("since")).toBe(false);
    // The current params are not changed in place.
    expect(current.get("group")).toBe("old");
  });

  it("says what a question still needs", () => {
    expect(incomplete(DEFAULT_QUERY)).toBeNull();
    expect(incomplete({ ...DEFAULT_QUERY, measure: "avg" })).toBe("field");
    expect(incomplete({ ...DEFAULT_QUERY, measure: "avg", field: "score" })).toBeNull();
    expect(incomplete({ ...DEFAULT_QUERY, since: "2026-10-05", until: "2026-10-01" })).toBe("range");
    expect(incomplete({ ...DEFAULT_QUERY, since: "2026-10-05", until: "2026-10-05" })).toBeNull();
  });
});

describe("the builder's answer", () => {
  it("is drawn as what the question asks for", () => {
    expect(shapeOf({})).toBe("number");
    expect(shapeOf({ group: "kind" })).toBe("categories");
    expect(shapeOf({ bucket: "day" })).toBe("time");
    expect(shapeOf({ bucket: "day", group: "kind" })).toBe("time-series");
  });

  it("puts on the axis only rows with a time and a value", () => {
    const rows = [
      { bucket: "2026-10", group: "a", value: 2, rows: 2 },
      { bucket: "2026-10", group: null, value: 1, rows: 1 },
      { bucket: null, group: "a", value: 5, rows: 5 },
      { bucket: "2026-11", group: "a", value: null, rows: 3 },
    ];
    expect(resultPoints(rows, "(not set)")).toEqual([
      { bucket: "2026-10", series: "a", value: 2 },
      { bucket: "2026-10", series: "(not set)", value: 1 },
    ]);
    expect(resultPoints([{ bucket: "2026-10", value: 4, rows: 4 }], "-")).toEqual([{ bucket: "2026-10", series: "", value: 4 }]);
  });

  it("ranks groups by value and folds the tail only when it adds up", () => {
    const rows = Array.from({ length: 5 }, (_, i) => ({ group: `g${i}`, value: i + 1, rows: 10 }));
    const summed = categories(rows, true, "-", 3);
    expect(summed.bars.map((b) => [b.key, b.value])).toEqual([["g4", 5], ["g3", 4], ["g2", 3], [OTHER, 3]]);
    expect(summed.bars[3]?.rows).toBe(20);
    expect(summed.left).toBe(2);
    const averaged = categories(rows, false, "-", 3);
    expect(averaged.bars.map((b) => b.key)).toEqual(["g4", "g3", "g2"]);
    expect(averaged.left).toBe(2);
  });

  it("names the group that has no value, and leaves out a group with no number", () => {
    const { bars } = categories([{ group: null, value: 2, rows: 2 }, { group: "x", value: null, rows: 4 }], false, "(not set)");
    expect(bars).toEqual([{ key: "(not set)", value: 2, rows: 2 }]);
  });
});

describe("CSV", () => {
  const result: AnalysisResult = {
    query: { of: "documents", measure: "avg", field: "score", group: "kind", bucket: "month" },
    rows: [
      { bucket: "2026-10", group: "runbook", value: 0.6, rows: 2 },
      { bucket: "2026-10", group: 'say "hi", twice', value: null, rows: 1, skipped: 1 },
      { bucket: null, group: null, value: -3.5, rows: 1 },
      { bucket: "2026-11", group: "=SUM(A1:A9)", value: 1234.5678, rows: 3 },
    ],
  };

  it("has the table's columns, raw numbers and the server's bucket keys", () => {
    expect(columnsOf(result.query)).toEqual(["month", "kind", "avg_score", "rows", "skipped"]);
    expect(toCSV(result)).toBe(
      [
        "month,kind,avg_score,rows,skipped",
        "2026-10,runbook,0.6,2,0",
        '2026-10,"say ""hi"", twice",,1,1',
        ",,-3.5,1,0",
        "2026-11,'=SUM(A1:A9),1234.5678,3,0",
        "",
      ].join("\r\n"),
    );
  });

  it("has only the columns a count has", () => {
    const count: AnalysisResult = { query: { of: "chunks", measure: "count", bucket: "day", bucket_field: "opened_at" }, rows: [{ bucket: "2026-10-03", value: 4, rows: 4 }] };
    expect(toCSV(count)).toBe("day_opened_at,rows\r\n2026-10-03,4\r\n");
    expect(toCSV({ query: { of: "documents", measure: "count" }, rows: [] })).toBe("rows\r\n");
  });

  it("quotes what would break a row and disarms what a spreadsheet would run", () => {
    expect(csvCell("a,b", true)).toBe('"a,b"');
    expect(csvCell("line\nbreak", true)).toBe('"line\nbreak"');
    expect(csvCell("@cmd", true)).toBe("'@cmd");
    expect(csvCell("+1", true)).toBe("'+1");
    // A negative number is a number, not a formula.
    expect(csvCell(-2)).toBe("-2");
    expect(csvCell(null)).toBe("");
  });

  it("names the file for the question and the day", () => {
    const now = new Date("2026-11-14T03:00:00Z");
    expect(csvFileName(result.query, now)).toBe("cortexdb-documents-avg_score-kind-month-2026-11-14.csv");
    expect(csvFileName({ of: "chunks", measure: "count" }, now)).toBe("cortexdb-chunks-count-2026-11-14.csv");
  });
});
