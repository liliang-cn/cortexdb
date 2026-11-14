// A time chart drawn by hand in SVG: stacked bars for counts, lines for
// measures. It is sized to its container in real pixels, so the text is never
// scaled; the axis is one y scale with round ticks; a pointer or the arrow
// keys pick a bucket, and the tooltip then lists that bucket's exact values.
// Everything it colours comes from the --chart-* tokens, which carry both
// themes.

import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import type { BucketUnit } from "@/api/analysis";
import { bucketLabel, formatTick, labelSlots, niceTicks } from "@/lib/analysis";
import type { Lang } from "@/lib/i18n";
import { cn } from "@/lib/utils";

export interface TimeSeries {
  key: string;
  label: string;
  /** A CSS colour, normally one of the --chart-* tokens. */
  color: string;
  /** One value per bucket; null is "no value here", which a line leaves as a gap. */
  values: (number | null)[];
}

interface Props {
  title: string;
  buckets: string[];
  unit: BucketUnit;
  series: TimeSeries[];
  /** Bars are stacked and are for values that add up; lines are for the rest. */
  mode: "bars" | "lines";
  lang: Lang;
  /** A value as the tooltip writes it, unit included. */
  format: (value: number) => string;
  /** A value on the axis; defaults to a compact number. */
  tick?: (value: number) => string;
  /** The values are counts: the axis takes no fractional step. */
  whole?: boolean;
  /** The name of the stack's total in the tooltip; none leaves the total out. */
  totalLabel?: string;
  noValue: string;
  /** What a screen reader is told the chart shows. */
  summary: string;
  hint: string;
  height?: number;
}

/** The container's width in CSS pixels, kept up to date. 0 until it is measured. */
function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    setWidth(el.clientWidth);
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width;
      if (w !== undefined) setWidth(Math.round(w));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width] as const;
}

const TOP = 10;
const BOTTOM = 24;
const RIGHT = 10;
/** Roughly how wide a character of the 11px axis text is. Chinese glyphs are counted double. */
const CHAR = 6.4;
const textWidth = (s: string) => [...s].reduce((w, ch) => w + (ch.charCodeAt(0) > 0x2e7f ? CHAR * 1.75 : CHAR), 0);

export function TimeChart({ title, buckets, unit, series, mode, lang, format, tick, whole, totalLabel, noValue, summary, hint, height = 220 }: Props) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [active, setActive] = useState<number | null>(null);
  const summaryId = useId();
  const n = buckets.length;

  // A new set of buckets is a new chart: the old index names another bucket.
  useEffect(() => setActive(null), [n, buckets[0], buckets[n - 1]]);

  const stacked = mode === "bars";
  const max = useMemo(() => {
    let top = 0;
    for (let i = 0; i < n; i++) {
      let at = 0;
      for (const s of series) {
        const v = s.values[i] ?? 0;
        at = stacked ? at + v : Math.max(at, v);
      }
      top = Math.max(top, at);
    }
    return top;
  }, [series, n, stacked]);

  const ticks = niceTicks(max, 4, whole);
  const yMax = ticks[ticks.length - 1] ?? 1;
  const tickText = ticks.map((v) => (tick ? tick(v) : formatTick(v, lang)));
  const left = Math.ceil(Math.max(...tickText.map(textWidth))) + 10;
  const plotW = Math.max(0, width - left - RIGHT);
  const plotH = height - TOP - BOTTOM;
  const band = n > 0 ? plotW / n : 0;
  const x = (i: number) => left + band * (i + 0.5);
  const y = (v: number) => TOP + plotH - (v / yMax) * plotH;

  const shortLabels = useMemo(() => buckets.map((b) => bucketLabel(b, unit, lang)), [buckets, unit, lang]);
  const labelW = Math.max(0, ...shortLabels.map(textWidth));
  const slots = labelSlots(n, Math.floor(plotW / (labelW + 14)));

  const pick = (e: PointerEvent<SVGRectElement>) => {
    if (band <= 0) return;
    const box = e.currentTarget.getBoundingClientRect();
    setActive(Math.min(n - 1, Math.max(0, Math.floor((e.clientX - box.left) / band))));
  };
  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (n === 0) return;
    const at = active ?? (e.key === "ArrowLeft" ? n : -1);
    let next: number | null;
    if (e.key === "ArrowRight") next = Math.min(n - 1, at + 1);
    else if (e.key === "ArrowLeft") next = Math.max(0, at - 1);
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = n - 1;
    else if (e.key === "Escape") next = null;
    else return;
    e.preventDefault();
    setActive(next);
  };

  // Few buckets get wider bars, so a week of them does not look like a fence.
  const barW = Math.min(n <= 12 ? 56 : 28, Math.max(1.5, band * 0.72));
  const marks = [];
  if (width > 0 && stacked) {
    for (let i = 0; i < n; i++) {
      let base = 0;
      // The last series with a value here is the top of the stack and gets the rounded end.
      let topIndex = -1;
      series.forEach((s, k) => {
        if ((s.values[i] ?? 0) > 0) topIndex = k;
      });
      series.forEach((s, k) => {
        const v = s.values[i] ?? 0;
        if (v <= 0) return;
        const y0 = y(base);
        const y1 = y(base + v);
        base += v;
        // A hairline of the surface between segments, so two colours never touch.
        const h = Math.max(1, y0 - y1 - (k === topIndex ? 0 : 1));
        const r = k === topIndex ? Math.min(3, barW / 2, h) : 0;
        const x0 = x(i) - barW / 2;
        marks.push(
          <path
            key={`${s.key}-${i}`}
            d={`M${x0},${y1 + h}V${y1 + r}q0,${-r} ${r},${-r}h${barW - 2 * r}q${r},0 ${r},${r}V${y1 + h}Z`}
            fill={s.color}
            opacity={active === null || active === i ? 1 : 0.55}
          />,
        );
      });
    }
  }
  if (width > 0 && !stacked) {
    for (const s of series) {
      let d = "";
      let pen = false;
      s.values.forEach((v, i) => {
        if (v === null) {
          pen = false;
          return;
        }
        d += `${pen ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`;
        pen = true;
      });
      marks.push(<path key={`${s.key}-line`} d={d} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />);
      s.values.forEach((v, i) => {
        if (v === null) return;
        // A point with no neighbour draws no line, so it always gets its dot.
        const alone = (s.values[i - 1] ?? null) === null && (s.values[i + 1] ?? null) === null;
        if (!alone && n > 45 && active !== i) return;
        marks.push(
          <circle key={`${s.key}-${i}`} cx={x(i)} cy={y(v)} r={active === i ? 4 : 2.5} fill={s.color} stroke="var(--card)" strokeWidth={active === i ? 2 : 1} />,
        );
      });
    }
  }

  const activeBucket = active !== null ? buckets[active] : undefined;
  const rows =
    active === null
      ? []
      : series.map((s) => ({ key: s.key, label: s.label, color: s.color, value: s.values[active] ?? null })).filter((r) => !stacked || series.length === 1 || (r.value ?? 0) > 0);
  const total = active === null ? 0 : series.reduce((a, s) => a + (s.values[active] ?? 0), 0);
  const heading = activeBucket ? bucketLabel(activeBucket, unit, lang, true) : "";
  const spoken = activeBucket
    ? `${heading}: ${rows.length === 0 ? format(0) : rows.map((r) => `${series.length > 1 ? `${r.label} ` : ""}${r.value === null ? noValue : format(r.value)}`).join(", ")}`
    : "";
  // The tooltip sits beside the bucket it describes, on whichever side has room.
  const onRight = active !== null && x(active) < width / 2;

  return (
    <div
      ref={ref}
      role="group"
      tabIndex={0}
      aria-label={title}
      aria-describedby={summaryId}
      onKeyDown={onKey}
      onBlur={() => setActive(null)}
      className="relative rounded-md outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
      style={{ height }}
    >
      <p id={summaryId} className="sr-only">
        {summary} {hint}
      </p>
      <p className="sr-only" aria-live="polite">
        {spoken}
      </p>
      {width > 0 && (
        <svg width={width} height={height} aria-hidden className="block select-none text-[11px] tabular-nums">
          {ticks.map((v, i) => (
            <g key={v}>
              <line x1={left} x2={width - RIGHT} y1={y(v)} y2={y(v)} className={i === 0 ? "stroke-input" : "stroke-border"} strokeDasharray={i === 0 ? undefined : "2 3"} />
              <text x={left - 6} y={y(v)} dy="0.32em" textAnchor="end" className="fill-muted-foreground">
                {tickText[i]}
              </text>
            </g>
          ))}
          {active !== null && (
            <rect x={left + band * active} y={TOP} width={band} height={plotH} className="fill-foreground" opacity={stacked ? 0.05 : 0.04} />
          )}
          {marks}
          {slots.map((i) => (
            <text
              key={i}
              x={Math.min(width - labelW / 2 - 2, Math.max(labelW / 2 + 2, x(i)))}
              y={height - 7}
              textAnchor="middle"
              className={cn("fill-muted-foreground", active === i && "fill-foreground font-medium")}
            >
              {shortLabels[i]}
            </text>
          ))}
          <rect x={left} y={TOP} width={plotW} height={plotH + BOTTOM} fill="transparent" onPointerMove={pick} onPointerDown={pick} onPointerLeave={() => setActive(null)} />
        </svg>
      )}
      {active !== null && (
        <div
          role="presentation"
          className="pointer-events-none absolute top-1 z-10 max-w-[70%] min-w-32 rounded-lg border bg-popover px-2.5 py-2 text-xs text-popover-foreground shadow-md"
          style={onRight ? { left: Math.min(width - 140, x(active) + band / 2 + 8) } : { right: Math.min(width - 140, width - x(active) + band / 2 + 8) }}
        >
          <p className="mb-1 font-medium">{heading}</p>
          {rows.length === 0 && <p className="tabular-nums text-muted-foreground">{format(0)}</p>}
          {rows.map((r) => (
            <p key={r.key} className="flex items-center gap-1.5 tabular-nums">
              <span aria-hidden className="size-2 shrink-0 rounded-[2px]" style={{ background: r.color }} />
              {series.length > 1 && <span className="min-w-0 truncate text-muted-foreground">{r.label}</span>}
              <span className="ml-auto pl-3 font-medium">{r.value === null ? noValue : format(r.value)}</span>
            </p>
          ))}
          {stacked && totalLabel && rows.length > 1 && (
            <p className="mt-1 flex border-t pt-1 tabular-nums">
              <span className="text-muted-foreground">{totalLabel}</span>
              <span className="ml-auto pl-3 font-medium">{format(total)}</span>
            </p>
          )}
        </div>
      )}
    </div>
  );
}

/** The key under a chart with more than one series: a swatch and a name each. */
export function ChartLegend({ series }: { series: Pick<TimeSeries, "key" | "label" | "color">[] }) {
  if (series.length < 2) return null;
  return (
    <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
      {series.map((s) => (
        <li key={s.key} className="flex min-w-0 items-center gap-1.5">
          <span aria-hidden className="size-2.5 shrink-0 rounded-[3px]" style={{ background: s.color }} />
          <span className="truncate">{s.label}</span>
        </li>
      ))}
    </ul>
  );
}
