// The overview: the charts that need no configuration. Each one draws from
// its own part of one answer and has its own way of saying it has nothing —
// that the brain keeps no such log, that this source cannot be asked, or that
// the range simply holds none — so one chart failing never blanks the rest.

import { useMemo, type ReactNode } from "react";
import type { AnalysisGraph, AnalysisOverview, AnalysisPoint, AnalysisRecalls, AnalysisSeries, AnalysisTypeCount } from "@/api/analysis";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Skeleton } from "@/components/ui/skeleton";
import {
  OTHER,
  RANGES,
  axisBuckets,
  bucketLabel,
  foldSeries,
  formatMs,
  formatNumber,
  formatShare,
  formatTick,
  layOut,
  seriesColors,
  type RangeDays,
} from "@/lib/analysis";
import type { AnalysisText } from "@/lib/analysisStrings";
import { localeOf, type Lang } from "@/lib/i18n";
import { BarList, ChartCard, ChartEmpty, Stat, type Bar } from "./parts";
import { ChartLegend, TimeChart, type TimeSeries } from "./TimeChart";

/** The slot each write kind and scope keeps, whatever else a range holds. */
const KIND_SLOTS = { memory: 0, knowledge: 1, node: 2, edge: 3, triple: 4, ontology_schema: 5 };
const SCOPE_SLOTS = { global: 0, user: 1, session: 2 };

export function RangeControl({ value, onChange, t }: { value: RangeDays; onChange: (next: RangeDays) => void; t: AnalysisText }) {
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      aria-label={t.range}
      value={String(value)}
      onValueChange={(v) => {
        // Radix reports "" when the pressed item is pressed again; a range is never unset.
        if (v !== "") onChange(Number(v) as RangeDays);
      }}
    >
      {RANGES.map((days) => (
        <ToggleGroupItem key={days} value={String(days)} className="px-3">
          {days === 0 ? t.allTime : t.days(days)}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}

function coveredNote(covered: string | undefined, truncated: boolean | undefined, t: AnalysisText, lang: Lang): string | null {
  if (!truncated || !covered) return null;
  const when = new Intl.DateTimeFormat(localeOf(lang), { year: "numeric", month: "short", day: "numeric" }).format(new Date(covered));
  return t.coveredSince(when);
}

interface CountChartProps {
  title: string;
  note: string;
  emptyText: string;
  data: AnalysisSeries;
  overview: AnalysisOverview;
  /** The name a series key is shown under. */
  nameOf: (key: string) => string;
  slots?: Readonly<Record<string, number>>;
  className?: string;
  /** The axis starts at the first bucket that has data, not at the start of the range. */
  fromData?: boolean;
  t: AnalysisText;
  lang: Lang;
}

/** A stacked bar chart of counts per bucket. An empty bucket is a real zero and is drawn as one. */
function CountChart({ title, note, emptyText, data, overview, nameOf, slots, className, fromData, t, lang }: CountChartProps) {
  // A chart bucketed apart from the overview has no place on the overview's axis: it runs over its own data.
  const unit = data.unit ?? overview.unit;
  const own = fromData || unit !== overview.unit;
  const chart = useMemo(() => {
    const buckets = axisBuckets(data.points.map((p) => p.bucket), unit, own ? undefined : overview.since, own ? undefined : overview.until);
    const folded = foldSeries(layOut(data.points, buckets, 0), true);
    const colors = seriesColors(folded.series.map((s) => s.key), slots);
    const series: TimeSeries[] = folded.series.map((s) => ({
      key: s.key,
      label: s.key === OTHER ? t.otherOf(folded.folded) : nameOf(s.key),
      color: colors.get(s.key) as string,
      values: s.values,
    }));
    return { buckets, series };
    // nameOf and slots are fixed per chart; the data and the range are what change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, unit, own, overview.since, overview.until, t]);

  let body: ReactNode;
  if (!data.available) body = <ChartEmpty title={t.unavailableChart}>{data.reason}</ChartEmpty>;
  else if (data.total === 0 || chart.buckets.length === 0) body = <ChartEmpty title={emptyText} />;
  else {
    const first = chart.buckets[0] as string;
    const last = chart.buckets[chart.buckets.length - 1] as string;
    body = (
      <>
        <TimeChart
          title={title}
          buckets={chart.buckets}
          unit={unit}
          series={chart.series}
          mode="bars"
          lang={lang}
          whole
          format={(v) => formatNumber(v, lang)}
          totalLabel={t.total}
          noValue={t.noValue}
          summary={t.chartSummary(title, chart.buckets.length, bucketLabel(first, unit, lang, true), bucketLabel(last, unit, lang, true), formatNumber(data.total, lang))}
          hint={t.chartHint}
        />
        <ChartLegend series={chart.series} />
      </>
    );
  }
  return (
    <ChartCard title={title} note={unit === overview.unit ? note : `${note} ${t.barPer(t.per[unit])}`} className={className} footer={coveredNote(data.covered_since, data.truncated, t, lang)}>
      {body}
    </ChartCard>
  );
}

function recallPoints(r: AnalysisRecalls): AnalysisPoint[] {
  return r.points.flatMap((p) => [
    { bucket: p.bucket, series: "hits", value: p.recalls - p.empty },
    { bucket: p.bucket, series: "empty", value: p.empty },
  ]);
}

function RecallCharts({ overview, t, lang }: { overview: AnalysisOverview; t: AnalysisText; lang: Lang }) {
  const r = overview.recalls;
  const chart = useMemo(() => {
    const buckets = axisBuckets(r.points.map((p) => p.bucket), overview.unit, overview.since, overview.until);
    const counts = layOut(recallPoints(r), buckets, 0);
    const value = (key: string) => counts.find((s) => s.key === key)?.values ?? buckets.map(() => 0);
    const volume: TimeSeries[] = [
      { key: "hits", label: t.withHits, color: "var(--chart-1)", values: value("hits") },
      { key: "empty", label: t.empty, color: "var(--chart-2)", values: value("empty") },
    ];
    // A latency exists only where a recall ran: the other buckets stay null and the line breaks there.
    const at = new Map(r.points.map((p) => [p.bucket, p]));
    const latency: TimeSeries[] = [
      { key: "p50", label: t.median, color: "var(--chart-1)", values: buckets.map((b) => at.get(b)?.p50_ms ?? null) },
      { key: "p95", label: t.p95, color: "var(--chart-2)", values: buckets.map((b) => at.get(b)?.p95_ms ?? null) },
    ];
    return { buckets, volume, latency };
  }, [r, overview.unit, overview.since, overview.until, t]);

  const blank = !r.available ? (
    r.off ? (
      <ChartEmpty title={t.recallsOffTitle}>{t.recallsOffBody}</ChartEmpty>
    ) : (
      <ChartEmpty title={t.unavailableChart}>{r.reason}</ChartEmpty>
    )
  ) : r.total === 0 || chart.buckets.length === 0 ? (
    <ChartEmpty title={t.recallsEmpty} />
  ) : null;
  const first = chart.buckets[0] ?? "";
  const last = chart.buckets[chart.buckets.length - 1] ?? "";
  const span = [bucketLabel(first, overview.unit, lang, true), bucketLabel(last, overview.unit, lang, true)] as const;
  const footer = coveredNote(r.covered_since, r.truncated, t, lang);

  return (
    <>
      <ChartCard title={t.recallsTitle} note={t.recallsNote} footer={footer}>
        {blank ?? (
          <>
            <TimeChart
              title={t.recallsTitle}
              buckets={chart.buckets}
              unit={overview.unit}
              series={chart.volume}
              mode="bars"
              lang={lang}
              whole
              format={(v) => formatNumber(v, lang)}
              totalLabel={t.total}
              noValue={t.noValue}
              summary={t.chartSummary(t.recallsTitle, chart.buckets.length, span[0], span[1], formatNumber(r.total, lang))}
              hint={t.chartHint}
            />
            <ChartLegend series={chart.volume} />
          </>
        )}
      </ChartCard>
      <ChartCard title={t.latencyTitle} note={t.latencyNote} footer={footer}>
        {blank ?? (
          <>
            <TimeChart
              title={t.latencyTitle}
              buckets={chart.buckets}
              unit={overview.unit}
              series={chart.latency}
              mode="lines"
              lang={lang}
              format={(v) => formatMs(v, lang)}
              tick={(v) => formatTick(v, lang)}
              noValue={t.noValue}
              summary={`${t.chartSummary(t.latencyTitle, r.points.length, span[0], span[1], r.p50_ms === undefined ? t.noValue : formatMs(r.p50_ms, lang))}`}
              hint={t.chartHint}
            />
            <ChartLegend series={chart.latency} />
          </>
        )}
      </ChartCard>
    </>
  );
}

function typeBars(types: AnalysisTypeCount[], other: number, otherTypes: number, total: number, t: AnalysisText, lang: Lang): Bar[] {
  const bars: Bar[] = types.map((ty) => ({
    key: ty.name,
    label: ty.name || t.missingGroup,
    value: ty.count,
    text: formatNumber(ty.count, lang),
    detail: formatShare(ty.count, total, lang),
  }));
  if (other > 0) bars.push({ key: OTHER, label: t.otherOf(otherTypes), value: other, text: formatNumber(other, lang), detail: formatShare(other, total, lang), muted: true });
  return bars;
}

function GraphCard({ g, t, lang }: { g: AnalysisGraph; t: AnalysisText; lang: Lang }) {
  let body: ReactNode;
  if (!g.available) body = <ChartEmpty title={t.unavailableChart}>{g.reason}</ChartEmpty>;
  else if (g.nodes === 0 && g.edges === 0) body = <ChartEmpty title={t.graphEmpty} />;
  else {
    const nodes = typeBars(g.node_types, g.other_nodes, g.other_node_types, g.nodes, t, lang);
    const edges = typeBars(g.edge_types, g.other_edges, g.other_edge_types, g.edges, t, lang);
    body = (
      <div className="grid gap-x-10 gap-y-6 md:grid-cols-2">
        <div className="min-w-0">
          <h3 className="mb-2 text-xs font-medium text-muted-foreground">
            {t.nodeTypes} · <span className="tabular-nums">{formatNumber(g.nodes, lang)}</span>
          </h3>
          {nodes.length > 0 ? <BarList bars={nodes} label={t.nodeTypes} /> : <p className="text-sm text-muted-foreground">{t.noNodes}</p>}
        </div>
        <div className="min-w-0">
          <h3 className="mb-2 text-xs font-medium text-muted-foreground">
            {t.edgeTypes} · <span className="tabular-nums">{formatNumber(g.edges, lang)}</span>
          </h3>
          {edges.length > 0 ? <BarList bars={edges} label={t.edgeTypes} /> : <p className="text-sm text-muted-foreground">{t.noEdges}</p>}
        </div>
      </div>
    );
  }
  return (
    <ChartCard title={t.graphTitle} note={t.graphNote} className="lg:col-span-2">
      {body}
    </ChartCard>
  );
}

/** A count for a headline: the range's, and under it everything stored when the read learned that. */
function countStat(label: string, s: AnalysisSeries, overview: AnalysisOverview, t: AnalysisText, lang: Lang) {
  if (!s.available) return <Stat label={label} value="—" detail={t.notCounted} />;
  const ranged = formatNumber(s.total, lang);
  const value = s.truncated ? t.atLeast(ranged) : ranged;
  if (overview.days === 0) return <Stat label={label} value={value} detail={t.stored} />;
  return <Stat label={label} value={value} detail={s.all === undefined ? t.inRange : `${t.inRange} · ${t.storedAll(formatNumber(s.all, lang))}`} />;
}

export function OverviewSkeleton({ label }: { label: string }) {
  return (
    <div aria-busy aria-label={label} className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="h-[5.25rem] rounded-xl" />
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} className="h-72 rounded-xl" />
        ))}
      </div>
    </div>
  );
}

export function Overview({ overview, t, lang }: { overview: AnalysisOverview; t: AnalysisText; lang: Lang }) {
  const r = overview.recalls;
  const g = overview.graph;
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
        {countStat(t.statMemories, overview.memories, overview, t, lang)}
        {countStat(t.statDocuments, overview.documents, overview, t, lang)}
        {r.available ? (
          <Stat
            label={t.statRecalls}
            value={r.truncated ? t.atLeast(formatNumber(r.total, lang)) : formatNumber(r.total, lang)}
            detail={r.total > 0 ? t.emptyShare(formatShare(r.empty, r.total, lang)) : t.inRange}
          />
        ) : (
          <Stat label={t.statRecalls} value="—" detail={r.off ? t.recallsOffTitle : t.notCounted} />
        )}
        {g.available ? (
          <>
            <Stat label={t.statNodes} value={formatNumber(g.nodes, lang)} detail={t.typesCount(formatNumber(g.node_types.length + g.other_node_types, lang))} />
            <Stat label={t.statEdges} value={formatNumber(g.edges, lang)} detail={t.typesCount(formatNumber(g.edge_types.length + g.other_edge_types, lang))} />
          </>
        ) : (
          <>
            <Stat label={t.statNodes} value="—" detail={t.notCounted} />
            <Stat label={t.statEdges} value="—" detail={t.notCounted} />
          </>
        )}
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <CountChart
          title={t.memoriesTitle}
          note={t.memoriesNote}
          emptyText={t.memoriesEmpty}
          data={overview.memories}
          overview={overview}
          nameOf={(k) => (k === "" ? t.noScope : (t.scopes[k] ?? k))}
          slots={SCOPE_SLOTS}
          fromData={overview.days === 0}
          t={t}
          lang={lang}
        />
        <CountChart
          title={t.documentsTitle}
          note={t.documentsNote}
          emptyText={t.documentsEmpty}
          data={overview.documents}
          overview={overview}
          nameOf={() => t.statDocuments}
          fromData={overview.days === 0}
          t={t}
          lang={lang}
        />
        <RecallCharts overview={overview} t={t} lang={lang} />
        <CountChart
          title={t.writesTitle}
          note={t.writesNote}
          emptyText={t.writesEmpty}
          data={overview.writes}
          overview={overview}
          nameOf={(k) => t.writeKinds[k] ?? k}
          slots={KIND_SLOTS}
          className="lg:col-span-2"
          // The feed keeps about a week: a longer range would be mostly a bare axis before it.
          fromData
          t={t}
          lang={lang}
        />
        <GraphCard g={g} t={t} lang={lang} />
      </div>
    </div>
  );
}
