// The builder: one question to aggregate_metadata, put together from a few
// controls and kept in the page's address. The answer is drawn as what it is
// — one number, bars for groups, lines for time — and always also as the
// table it came from, which copies and downloads as CSV.

import { useEffect, useId, useMemo, useState, type ReactNode } from "react";
import { Check, Copy, Download, Loader2, RotateCcw } from "lucide-react";
import type { AnalysisField, AnalysisFields, AnalysisResult, BucketUnit, Measure, RowKind } from "@/api/analysis";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
  DEFAULT_QUERY,
  FIELD_NAME,
  MEASURES,
  OTHER,
  UNITS,
  additive,
  axisBuckets,
  bucketLabel,
  categories,
  columnsOf,
  csvFileName,
  foldSeries,
  formatNumber,
  incomplete,
  layOut,
  resultPoints,
  seriesColors,
  shapeOf,
  toCSV,
  type AnalysisState,
} from "@/lib/analysis";
import type { AnalysisText } from "@/lib/analysisStrings";
import type { Lang } from "@/lib/i18n";
import type { Loaded } from "@/lib/useAnalysis";
import { cn } from "@/lib/utils";
import { BarList, ChartCard, ChartEmpty } from "./parts";
import { ChartLegend, TimeChart, type TimeSeries } from "./TimeChart";

type Query = AnalysisState["query"];

/** Radix's Select has no empty value; this stands for "none". */
const NONE = "-";
/** How many table rows are drawn before "Show all". */
const TABLE_PAGE = 30;

function Labeled({ label, help, children, className }: { label: string; help?: string; children: (id: string) => ReactNode; className?: string }) {
  const id = useId();
  return (
    <div className={cn("min-w-0", className)}>
      <label htmlFor={id} className="mb-1 block text-xs font-medium text-muted-foreground">
        {label}
      </label>
      {children(id)}
      {help && <p className="mt-1 text-xs text-muted-foreground">{help}</p>}
    </div>
  );
}

interface FieldInputProps {
  id: string;
  value: string;
  options: AnalysisField[];
  placeholder: string;
  t: AnalysisText;
  onCommit: (name: string) => void;
}

/**
 * A field name: picked from the sampled fields or typed. What is typed is
 * taken when it is finished (Enter, leaving the box, or a listed name), so a
 * half-typed name never becomes a question.
 */
function FieldInput({ id, value, options, placeholder, t, onCommit }: FieldInputProps) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const listId = `${id}-fields`;
  const bad = draft !== "" && !FIELD_NAME.test(draft);
  const commit = (next: string) => {
    const name = next.trim();
    if (name === value || (name !== "" && !FIELD_NAME.test(name))) return;
    onCommit(name);
  };
  return (
    <>
      <Input
        id={id}
        list={listId}
        value={draft}
        placeholder={placeholder}
        autoComplete="off"
        autoCapitalize="off"
        spellCheck={false}
        aria-invalid={bad || undefined}
        aria-describedby={bad ? `${id}-bad` : undefined}
        className="font-mono"
        onChange={(e) => {
          const next = e.target.value;
          setDraft(next);
          if (next === "" || options.some((o) => o.name === next)) commit(next);
        }}
        onBlur={() => commit(draft)}
        onKeyDown={(e) => {
          if (e.key === "Enter") commit(draft);
        }}
      />
      <datalist id={listId}>
        {options.map((o) => (
          <option key={o.name} value={o.name}>
            {`${t.kinds[o.kind]}${o.examples?.length ? ` · ${o.examples.slice(0, 3).join(", ")}` : ""}`}
          </option>
        ))}
      </datalist>
      {bad && (
        <p id={`${id}-bad`} className="mt-1 text-xs text-destructive">
          {t.badName}
        </p>
      )}
    </>
  );
}

function Builder({ q, set, fields, t }: { q: Query; set: (patch: Partial<Query>) => void; fields: Loaded<AnalysisFields>; t: AnalysisText }) {
  const sample = fields.data?.fields ?? [];
  const numeric = sample.filter((f) => f.kind === "number");
  const dated = sample.filter((f) => f.kind === "date").map((f) => f.name);
  if (q.bucket_field && !dated.includes(q.bucket_field)) dated.unshift(q.bucket_field);
  const collections = [...(fields.data?.collections ?? [])];
  if (q.collection && !collections.includes(q.collection)) collections.unshift(q.collection);
  const changed = JSON.stringify(q) !== JSON.stringify(DEFAULT_QUERY);

  return (
    <section className="rounded-xl border bg-card p-4 sm:p-5">
      <div className="grid gap-x-3 gap-y-4 sm:grid-cols-2 lg:grid-cols-4">
        <Labeled label={t.of} help={t.ofHelp[q.of]}>
          {(id) => (
            <Select value={q.of} onValueChange={(v) => set({ of: v as RowKind })}>
              <SelectTrigger id={id} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(["documents", "chunks"] as const).map((k) => (
                  <SelectItem key={k} value={k}>
                    {t.ofKinds[k]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </Labeled>

        <Labeled label={t.measure}>
          {(id) => (
            <Select value={q.measure} onValueChange={(v) => set({ measure: v as Measure })}>
              <SelectTrigger id={id} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {MEASURES.map((m) => (
                  <SelectItem key={m} value={m}>
                    {t.measures[m]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </Labeled>

        {q.measure !== "count" && (
          <Labeled label={t.field}>{(id) => <FieldInput id={id} value={q.field} options={numeric} placeholder={t.fieldPlaceholder} t={t} onCommit={(field) => set({ field })} />}</Labeled>
        )}

        <Labeled label={t.groupBy}>{(id) => <FieldInput id={id} value={q.group} options={sample} placeholder={t.groupPlaceholder} t={t} onCommit={(group) => set({ group })} />}</Labeled>

        <Labeled label={t.bucket}>
          {(id) => (
            <Select value={q.bucket || NONE} onValueChange={(v) => set({ bucket: v === NONE ? "" : (v as BucketUnit) })}>
              <SelectTrigger id={id} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>{t.noBucket}</SelectItem>
                {UNITS.map((u) => (
                  <SelectItem key={u} value={u}>
                    {t.units[u]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </Labeled>

        {q.bucket && (
          <Labeled label={t.bucketOn}>
            {(id) => (
              <Select value={q.bucket_field || NONE} onValueChange={(v) => set({ bucket_field: v === NONE ? "" : v })}>
                <SelectTrigger id={id} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>{t.storedTime}</SelectItem>
                  {dated.map((name) => (
                    <SelectItem key={name} value={name} className="font-mono">
                      {name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </Labeled>
        )}

        <Labeled label={t.collection}>
          {(id) => (
            <Select value={q.collection || NONE} onValueChange={(v) => set({ collection: v === NONE ? "" : v })}>
              <SelectTrigger id={id} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>{t.anyCollection}</SelectItem>
                {collections.map((name) => (
                  <SelectItem key={name} value={name}>
                    {name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </Labeled>

        <div className="grid min-w-0 grid-cols-2 gap-3">
          <Labeled label={t.since}>{(id) => <Input id={id} type="date" value={q.since} max={q.until || undefined} onChange={(e) => set({ since: e.target.value })} />}</Labeled>
          <Labeled label={t.until}>{(id) => <Input id={id} type="date" value={q.until} min={q.since || undefined} onChange={(e) => set({ until: e.target.value })} />}</Labeled>
        </div>
      </div>

      <div className="mt-4 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t pt-3">
        <p className="min-w-0 text-xs text-muted-foreground">
          {fields.error ? t.sampleFailed : !fields.data ? t.loading : fields.data.sampled === 0 ? t.sampleEmpty : t.sampled(fields.data.sampled)}
        </p>
        <Button variant="ghost" size="sm" disabled={!changed} onClick={() => set(DEFAULT_QUERY)}>
          <RotateCcw aria-hidden />
          {t.reset}
        </Button>
      </div>
    </section>
  );
}

function CsvActions({ result, t }: { result: AnalysisResult; t: AnalysisText }) {
  const [said, setSaid] = useState<{ text: string; bad?: boolean } | null>(null);
  useEffect(() => {
    if (!said) return;
    const timer = setTimeout(() => setSaid(null), 4000);
    return () => clearTimeout(timer);
  }, [said]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(toCSV(result));
      setSaid({ text: t.copied });
    } catch {
      setSaid({ text: t.copyFailed, bad: true });
    }
  };
  const download = () => {
    const url = URL.createObjectURL(new Blob([toCSV(result)], { type: "text/csv;charset=utf-8" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = csvFileName(result.query, new Date());
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };
  return (
    <span className="flex flex-wrap items-center justify-end gap-2">
      <span role="status" className={cn("text-xs", said?.bad ? "text-destructive" : "text-muted-foreground")}>
        {said?.text ?? ""}
      </span>
      <Button variant="outline" size="sm" onClick={copy}>
        {said && !said.bad ? <Check aria-hidden /> : <Copy aria-hidden />}
        {t.copyCsv}
      </Button>
      <Button variant="outline" size="sm" onClick={download}>
        <Download aria-hidden />
        {t.downloadCsv}
      </Button>
    </span>
  );
}

function ResultTable({ result, t, lang }: { result: AnalysisResult; t: AnalysisText; lang: Lang }) {
  const q = result.query;
  const [all, setAll] = useState(false);
  useEffect(() => setAll(false), [result]);
  const rows = all ? result.rows : result.rows.slice(0, TABLE_PAGE);
  const measured = q.measure !== "count";
  const text = "px-3 py-1.5 text-left";
  const num = "px-3 py-1.5 text-right tabular-nums";
  const columns = columnsOf(q);
  const textColumns = (q.bucket ? 1 : 0) + (q.group ? 1 : 0);
  return (
    <ChartCard title={t.table} action={<CsvActions result={result} t={t} />}>
      <div className="-mx-1 overflow-x-auto">
        <table className="w-full min-w-max border-collapse text-sm">
          <thead>
            <tr className="border-b text-xs text-muted-foreground">
              {columns.map((c, i) => (
                <th key={c} scope="col" className={cn("font-mono font-medium", i < textColumns ? text : num)}>
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i} className="border-b last:border-0">
                {q.bucket && (
                  <td className={cn(text, "whitespace-nowrap", typeof r.bucket !== "string" && "text-muted-foreground")}>
                    {typeof r.bucket === "string" ? bucketLabel(r.bucket, q.bucket as BucketUnit, lang, true) : t.missingGroup}
                  </td>
                )}
                {q.group && <td className={cn(text, "max-w-64 truncate", r.group == null && "text-muted-foreground")}>{r.group ?? t.missingGroup}</td>}
                {measured && <td className={cn(num, r.value === null && "text-muted-foreground")}>{r.value === null ? t.noNumber : formatNumber(r.value, lang)}</td>}
                <td className={num}>{formatNumber(r.rows, lang)}</td>
                {measured && <td className={cn(num, !r.skipped && "text-muted-foreground")}>{formatNumber(r.skipped ?? 0, lang)}</td>}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {result.rows.length > TABLE_PAGE && !all && (
        <p className="mt-3 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          {t.tableRows(formatNumber(rows.length, lang), formatNumber(result.rows.length, lang))}
          <button type="button" className="text-primary underline underline-offset-4" onClick={() => setAll(true)}>
            {t.showAll}
          </button>
        </p>
      )}
    </ChartCard>
  );
}

function ResultChart({ result, t, lang }: { result: AnalysisResult; t: AnalysisText; lang: Lang }) {
  const q = result.query;
  const shape = shapeOf(q);
  const adds = additive(q.measure);
  const measureName = q.measure === "count" ? t.measures.count : `${t.measures[q.measure]} · ${q.field ?? ""}`;
  const title = [measureName, q.group, q.bucket ? t.units[q.bucket] : ""].filter(Boolean).join(" · ");
  const fmt = (v: number) => formatNumber(v, lang);

  const drawn = useMemo(() => {
    if (shape === "categories") return { bars: categories(result.rows, adds, t.missingGroup) };
    if (shape === "time" || shape === "time-series") {
      const unit = q.bucket as BucketUnit;
      const points = resultPoints(result.rows, t.missingGroup);
      const buckets = axisBuckets(points.map((p) => p.bucket), unit);
      // A bucket nothing was counted in is a zero; one nothing was measured in has no value.
      const folded = foldSeries(layOut(points, buckets, q.measure === "count" ? 0 : null), adds);
      const colors = seriesColors(folded.series.map((s) => s.key));
      const series: TimeSeries[] = folded.series.map((s) => ({
        key: s.key,
        label: s.key === OTHER ? t.otherOf(folded.folded) : s.key === "" ? measureName : s.key,
        color: colors.get(s.key) as string,
        values: s.values,
      }));
      return { time: { unit, buckets, series, left: adds ? 0 : folded.folded } };
    }
    return {};
  }, [result, shape, adds, q.bucket, q.measure, measureName, t]);

  const skipped = result.rows.reduce((a, r) => a + (r.skipped ?? 0), 0);
  const undated = q.bucket ? result.rows.filter((r) => typeof r.bucket !== "string").reduce((a, r) => a + r.rows, 0) : 0;
  const left = drawn.bars?.left && !adds ? drawn.bars.left : (drawn.time?.left ?? 0);
  const notes = [
    skipped > 0 && t.skippedNote(formatNumber(skipped, lang)),
    undated > 0 && t.undated(formatNumber(undated, lang)),
    left > 0 && t.leftOut(left),
    result.truncated && t.truncated(formatNumber(result.rows.length, lang)),
  ].filter((n): n is string => Boolean(n));

  let body: ReactNode;
  if (shape === "number") {
    const row = result.rows[0];
    body = (
      <div className="py-4">
        <p className="text-4xl font-semibold tracking-tight tabular-nums">{!row || row.value === null ? t.noNumber : fmt(row.value)}</p>
        {row && q.measure !== "count" && (
          <p className="mt-1 text-sm text-muted-foreground">
            {t.rows}: <span className="tabular-nums">{fmt(row.rows)}</span>
          </p>
        )}
      </div>
    );
  } else if (drawn.bars) {
    body =
      drawn.bars.bars.length === 0 ? (
        <ChartEmpty title={t.noNumber} />
      ) : (
        <BarList
          label={title}
          bars={drawn.bars.bars.map((b) => ({
            key: b.key,
            label: b.key === OTHER ? t.otherOf(drawn.bars.left) : b.key,
            value: b.value,
            text: fmt(b.value),
            detail: undefined,
            muted: b.key === OTHER,
          }))}
        />
      );
  } else if (drawn.time && drawn.time.buckets.length > 0) {
    const { unit, buckets, series } = drawn.time;
    const total = adds ? fmt(result.rows.reduce((a, r) => a + (typeof r.bucket === "string" ? (r.value ?? 0) : 0), 0)) : t.measures[q.measure];
    body = (
      <>
        <TimeChart
          title={title}
          buckets={buckets}
          unit={unit}
          series={series}
          mode="lines"
          lang={lang}
          whole={q.measure === "count"}
          format={fmt}
          noValue={t.noValue}
          summary={t.chartSummary(title, buckets.length, bucketLabel(buckets[0] as string, unit, lang, true), bucketLabel(buckets[buckets.length - 1] as string, unit, lang, true), total)}
          hint={t.chartHint}
          height={260}
        />
        <ChartLegend series={series} />
      </>
    );
  } else body = <ChartEmpty title={t.noNumber} />;

  return (
    <ChartCard
      title={title}
      note={`${t.resultOne(t.ofKinds[q.of])}${q.collection ? ` · ${q.collection}` : ""}${q.since || q.until ? ` · ${q.since || "…"} – ${q.until || "…"}` : ""}`}
      footer={notes.length > 0 ? notes.map((n) => <p key={n}>{n}</p>) : undefined}
    >
      {body}
    </ChartCard>
  );
}

interface Props {
  q: Query;
  set: (patch: Partial<Query>) => void;
  fields: Loaded<AnalysisFields>;
  result: Loaded<AnalysisResult>;
  /** The source runs tools; without them there is nothing to ask. */
  usable: boolean;
  t: AnalysisText;
  lang: Lang;
}

export function Explore({ q, set, fields, result, usable, t, lang }: Props) {
  if (!usable) return <ChartEmpty title={t.unavailableTitle}>{t.noTools}</ChartEmpty>;
  const missing = incomplete(q);

  let answer: ReactNode;
  if (missing === "field") answer = <ChartEmpty title={t.needField(t.measures[q.measure])} />;
  else if (missing === "range") answer = <ChartEmpty title={t.badRange} />;
  else if (result.error && !result.loading)
    answer = (
      <ChartEmpty title={t.askFailed}>
        {result.error}{" "}
        <button type="button" className="text-primary underline underline-offset-4" onClick={result.reload}>
          {t.retry}
        </button>
      </ChartEmpty>
    );
  else if (!result.data)
    answer = (
      <div aria-busy aria-label={t.asking} className="space-y-4">
        <Skeleton className="h-72 rounded-xl" />
        <Skeleton className="h-40 rounded-xl" />
      </div>
    );
  else if (result.data.rows.length === 0 || (shapeOf(result.data.query) === "number" && result.data.rows[0]?.rows === 0))
    answer = <ChartEmpty title={t.noRows}>{t.noRowsHint}</ChartEmpty>;
  else
    answer = (
      <div className={cn("space-y-4 transition-opacity", result.loading && "opacity-60")} aria-busy={result.loading || undefined}>
        <ResultChart result={result.data} t={t} lang={lang} />
        <ResultTable result={result.data} t={t} lang={lang} />
      </div>
    );

  return (
    <div className="space-y-4">
      <div className="max-w-3xl space-y-1 text-sm text-muted-foreground">
        <p>{t.exploreIntro}</p>
        <p>{t.exploreScope}</p>
      </div>
      <Builder q={q} set={set} fields={fields} t={t} />
      {result.loading && result.data && (
        <p className="flex items-center gap-2 text-xs text-muted-foreground" role="status">
          <Loader2 aria-hidden className="size-3.5 animate-spin" />
          {t.asking}
        </p>
      )}
      {answer}
      <p className="text-xs text-muted-foreground">{t.linkNote}</p>
    </div>
  );
}
