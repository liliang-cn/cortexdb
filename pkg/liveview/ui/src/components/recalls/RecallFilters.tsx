import { useEffect, useRef, useState, type ReactNode } from "react";
import { Search, TriangleAlert, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { hasFilters, withoutFilters, type RecallsOptions } from "@/lib/recalls";
import type { RecallsText } from "@/lib/recalls-strings";

/** Typing settles for this long before the timeline is read again. */
const TYPING_MS = 250;

function FilterChip({ label, value, title, clear, onClear, mono }: { label: string; value: ReactNode; title?: string; clear: string; onClear: () => void; mono?: boolean }) {
  return (
    <span className="inline-flex max-w-full min-w-0 items-center gap-1 rounded-full border bg-accent py-0.5 pr-0.5 pl-2.5 text-xs text-accent-foreground" title={title}>
      <span className="shrink-0 text-accent-foreground/80">{label}</span>
      <span className={mono ? "min-w-0 truncate font-mono font-medium" : "min-w-0 truncate font-medium"}>{value}</span>
      <Button variant="ghost" size="icon-xs" className="shrink-0 rounded-full hover:bg-background/60" aria-label={`${clear}: ${label}`} onClick={onClear}>
        <X aria-hidden />
      </Button>
    </span>
  );
}

export interface RecallFiltersProps {
  opts: RecallsOptions;
  t: RecallsText;
  onChange: (next: RecallsOptions) => void;
}

/**
 * The timeline's filters: query text and "returned nothing" as controls, and
 * the narrowings that arrive from elsewhere (a record, a trace, a key, a
 * project) as chips that say what is in force and take it off.
 */
export function RecallFilters({ opts, t, onChange }: RecallFiltersProps) {
  const [text, setText] = useState(opts.contains);
  // The box follows the URL when the URL changes from elsewhere (clear all,
  // a link), but not while it is the one changing the URL.
  const typed = useRef(opts.contains);
  useEffect(() => {
    if (opts.contains !== typed.current) {
      typed.current = opts.contains;
      setText(opts.contains);
    }
  }, [opts.contains]);

  const latest = useRef(opts);
  latest.current = opts;
  useEffect(() => {
    const v = text.trim();
    if (v === latest.current.contains) return;
    const timer = setTimeout(() => {
      typed.current = v;
      onChange({ ...latest.current, contains: v });
    }, TYPING_MS);
    return () => clearTimeout(timer);
    // onChange is rebuilt every render; only the text decides when to send.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [text]);

  const chips: ReactNode[] = [];
  if (opts.record) chips.push(<FilterChip key="record" label={t.byRecord} value={opts.record} clear={t.clearFilter} onClear={() => onChange({ ...opts, record: "" })} mono />);
  if (opts.trace) {
    chips.push(
      <FilterChip key="trace" label={t.byTrace} value={opts.trace} title={opts.trace} clear={t.clearFilter} onClear={() => onChange({ ...opts, trace: "" })} mono />,
    );
  }
  if (opts.key) chips.push(<FilterChip key="key" label={t.byKey} value={opts.key} clear={t.clearFilter} onClear={() => onChange({ ...opts, key: "" })} />);
  if (opts.project) chips.push(<FilterChip key="project" label={t.byProject} value={opts.project} clear={t.clearFilter} onClear={() => onChange({ ...opts, project: "" })} />);

  return (
    <div className="space-y-2.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
        <div className="relative min-w-0 flex-[1_1_16rem]">
          <Search aria-hidden className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={t.filterPlaceholder}
            aria-label={t.filterLabel}
            className="bg-card pl-8"
          />
        </div>
        <label className="flex shrink-0 cursor-pointer items-center gap-2 text-sm">
          <Switch checked={opts.empty} onCheckedChange={(on) => onChange({ ...opts, empty: on })} />
          {t.emptyOnly}
        </label>
        {hasFilters(opts) && (
          <Button variant="ghost" size="sm" className="shrink-0 text-muted-foreground" onClick={() => onChange(withoutFilters(opts))}>
            {t.clearAll}
          </Button>
        )}
      </div>
      {chips.length > 0 && <div className="flex flex-wrap gap-2">{chips}</div>}
      {opts.badTrace && (
        <p className="flex items-start gap-2 text-sm text-grade-held-ink">
          <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0" />
          <span className="min-w-0 [overflow-wrap:anywhere]">{t.badTrace(opts.badTrace)}</span>
          <button type="button" className="shrink-0 underline underline-offset-4" onClick={() => onChange({ ...opts, badTrace: "" })}>
            {t.clearFilter}
          </button>
        </p>
      )}
    </div>
  );
}
