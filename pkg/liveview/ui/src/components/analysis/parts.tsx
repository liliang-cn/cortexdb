// The pieces the Analysis page is laid out from: the card a chart sits in,
// the headline number, and the horizontal bars a list of categories is drawn
// as.

import type { ReactNode } from "react";
import { OTHER_COLOR, SERIES_COLORS } from "@/lib/analysis";
import { cn } from "@/lib/utils";

export function ChartCard({ title, note, action, footer, className, children }: { title: string; note?: ReactNode; action?: ReactNode; footer?: ReactNode; className?: string; children: ReactNode }) {
  return (
    <section className={cn("flex min-w-0 flex-col rounded-xl border bg-card p-4 sm:p-5", className)}>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold">{title}</h2>
          {note && <p className="mt-1 text-xs text-muted-foreground">{note}</p>}
        </div>
        {action}
      </div>
      <div className="min-w-0 flex-1">{children}</div>
      {footer && <div className="mt-3 space-y-1 text-xs text-muted-foreground">{footer}</div>}
    </section>
  );
}

/** What a chart says when it has nothing to draw: a reason, never an empty axis. */
export function ChartEmpty({ title, children, className }: { title: string; children?: ReactNode; className?: string }) {
  return (
    <div className={cn("flex min-h-36 flex-col items-center justify-center rounded-lg border border-dashed px-4 py-6 text-center", className)}>
      <p className="text-sm font-medium">{title}</p>
      {children && <p className="mt-1 max-w-sm text-xs text-muted-foreground [overflow-wrap:anywhere]">{children}</p>}
    </div>
  );
}

export function Stat({ label, value, detail }: { label: string; value: ReactNode; detail?: ReactNode }) {
  return (
    <div className="min-w-0 rounded-xl border bg-card px-4 py-3">
      <p className="truncate text-xs text-muted-foreground">{label}</p>
      <p className="mt-1 truncate text-2xl font-semibold tracking-tight tabular-nums">{value}</p>
      <p className="mt-0.5 min-h-4 truncate text-xs text-muted-foreground">{detail}</p>
    </div>
  );
}

export interface Bar {
  key: string;
  label: string;
  value: number;
  /** The value as it is written, unit included. */
  text: string;
  /** A second figure beside the value: a share, a row count. */
  detail?: string;
  muted?: boolean;
}

/**
 * Categories as horizontal bars: a name, a bar to scale and the exact value
 * on every row, so nothing has to be hovered to be read. One colour for all
 * of them — the name says which is which — and grey for the folded rest.
 */
export function BarList({ bars, label, className }: { bars: Bar[]; label: string; className?: string }) {
  const max = Math.max(0, ...bars.map((b) => b.value));
  const detailed = bars.some((b) => b.detail);
  return (
    <ul aria-label={label} className={cn("space-y-2", className)}>
      {bars.map((b) => (
        <li
          key={b.key}
          className={cn(
            "grid items-center gap-x-3 text-sm",
            detailed ? "grid-cols-[minmax(0,7rem)_minmax(0,1fr)_auto_3.25rem] sm:grid-cols-[minmax(0,10rem)_minmax(0,1fr)_auto_3.25rem]" : "grid-cols-[minmax(0,7rem)_minmax(0,1fr)_auto] sm:grid-cols-[minmax(0,10rem)_minmax(0,1fr)_auto]",
          )}
        >
          <span className={cn("truncate", b.muted && "text-muted-foreground")} title={b.label}>
            {b.label}
          </span>
          <span aria-hidden className="h-2.5 overflow-hidden rounded-sm bg-muted">
            <span
              className="block h-full rounded-sm"
              style={{ width: `${max > 0 ? Math.max(b.value > 0 ? 1 : 0, (b.value / max) * 100) : 0}%`, background: b.muted ? OTHER_COLOR : SERIES_COLORS[0] }}
            />
          </span>
          <span className="text-right text-xs font-medium tabular-nums">{b.text}</span>
          {detailed && <span className="truncate text-right text-xs text-muted-foreground tabular-nums">{b.detail}</span>}
        </li>
      ))}
    </ul>
  );
}
