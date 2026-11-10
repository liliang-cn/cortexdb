import type { ActivityEvent } from "@/api";
import { KindChip } from "@/components/semantic";
import { openPalette } from "@/components/shell/palette";
import { FEED_TEXT } from "@/lib/feedStrings";
import { useLang } from "@/lib/i18n";
import { activityTone, semLabel, semMeans } from "@/lib/semantics";
import { formatClock, formatRelative } from "@/lib/time";
import { cn } from "@/lib/utils";

/** One tool call: what kind, which tool, what it said, which terms it named. */
export function ActivityRow({ event, now, compact, fresh, className }: { event: ActivityEvent; now: number; compact?: boolean; fresh?: boolean; className?: string }) {
  const sem = activityTone(event);
  const terms = event.terms ?? [];
  const lang = useLang();
  const t = FEED_TEXT[lang];
  const word = semLabel(sem, lang);
  const time = (
    <time dateTime={new Date(event.at).toISOString()} title={formatClock(lang, event.at)} className="shrink-0 text-xs tabular-nums text-muted-foreground">
      {formatRelative(lang, event.at, now)}
    </time>
  );

  if (compact) {
    return (
      <div className={cn("flex items-start gap-2 px-2 py-1.5 text-sm", fresh && "animate-arrive motion-reduce:animate-none", className)}>
        <span className={cn("mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full", sem.chip)} title={`${word}: ${semMeans(sem, lang)}`}>
          <sem.icon aria-hidden className="size-3" />
          <span className="sr-only">{word}</span>
        </span>
        <p className="min-w-0 flex-1 leading-snug [overflow-wrap:anywhere]">{event.text}</p>
        {time}
      </div>
    );
  }

  return (
    <div className={cn("flex items-start gap-3 border-l-[3px] px-3 py-2.5 sm:px-4", sem.rail, fresh && "animate-arrive motion-reduce:animate-none", className)}>
      <KindChip event={event} className="mt-px w-[5.5rem] shrink-0 justify-start max-sm:w-auto" />
      <div className="min-w-0 flex-1">
        <p className="text-sm leading-6 [overflow-wrap:anywhere]">{event.text}</p>
        <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
          <span className="font-mono">{event.tool}</span>
          {terms.slice(0, 12).map((term) => (
            <button
              key={term}
              type="button"
              onClick={() => openPalette(term)}
              title={t.findInGraph(term)}
              className="rounded-full border bg-card px-2 py-px text-foreground outline-none hover:border-primary hover:text-primary focus-visible:ring-[3px] focus-visible:ring-ring/50"
            >
              {term}
            </button>
          ))}
          {terms.length > 12 && <span>{t.more(terms.length - 12)}</span>}
        </div>
      </div>
      {time}
    </div>
  );
}
