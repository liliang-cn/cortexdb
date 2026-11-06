import { CircleSlash, TriangleAlert } from "lucide-react";
import type { RecallEntry } from "@/api/recalls";
import { Badge } from "@/components/ui/badge";
import type { Lang } from "@/lib/i18n";
import { clockTime, markCounts, relativeTime, shortTrace, whoOf } from "@/lib/recalls";
import type { RecallsText } from "@/lib/recalls-strings";
import { cn } from "@/lib/utils";

export interface RecallRowProps {
  recall: RecallEntry;
  selected: boolean;
  /** It arrived while the page was open: flash once. */
  fresh: boolean;
  now: number;
  lang: Lang;
  t: RecallsText;
  onSelect: (id: string) => void;
}

/**
 * One recall in the timeline: the question, then when, how much came back,
 * how long it took and who asked. A recall that came back empty or degraded
 * says so on the row, since those are what a reader scans the list for.
 */
export function RecallRow({ recall: r, selected, fresh, now, lang, t, onSelect }: RecallRowProps) {
  const at = Date.parse(r.at);
  const who = whoOf(r);
  const marks = markCounts(r.hits);
  const marked = t.marks(marks.useful, marks.noise);
  return (
    <button
      type="button"
      onClick={() => onSelect(r.id)}
      aria-current={selected ? "true" : undefined}
      data-recall={r.id}
      className={cn(
        "block w-full border-l-[3px] border-transparent px-4 py-3 text-left outline-none transition-colors",
        "hover:bg-muted/60 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:ring-inset",
        selected && "border-primary bg-accent/70 hover:bg-accent/70",
        fresh && "animate-arrive motion-reduce:animate-none",
      )}
    >
      <span className="line-clamp-3 text-sm leading-6 font-medium [overflow-wrap:anywhere]">{r.query}</span>
      <span className="mt-1 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-muted-foreground">
        <time dateTime={r.at} title={clockTime(lang, at)} className="tabular-nums">
          {relativeTime(lang, at, now)}
        </time>
        <span className="tabular-nums">{t.hits(r.hit_count)}</span>
        <span className="tabular-nums">{t.ms(r.latency_ms)}</span>
        {who && <span className="min-w-0 [overflow-wrap:anywhere]">{who}</span>}
        {r.trace_id && (
          <span className="font-mono text-[0.7rem]" title={`${t.traceId} ${r.trace_id}`}>
            {t.trace} {shortTrace(r.trace_id)}
          </span>
        )}
        {r.hit_count === 0 && (
          <Badge variant="outline" className="border-transparent bg-grade-held-soft font-normal text-grade-held-ink">
            <CircleSlash aria-hidden />
            {t.returnedNothing}
          </Badge>
        )}
        {(r.degraded ?? []).map((d) => (
          <Badge key={d} variant="outline" className="border-transparent bg-kind-failed-soft font-normal text-kind-failed-ink" title={t.degradedWhy[d] ?? d}>
            <TriangleAlert aria-hidden />
            {t.degraded}: {d}
          </Badge>
        ))}
        {marked && <span className="text-foreground/70">{marked}</span>}
      </span>
    </button>
  );
}
