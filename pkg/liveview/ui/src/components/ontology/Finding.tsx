import type { CSSProperties } from "react";
import { Badge } from "@/components/ui/badge";
import type { Field, Finding as FindingT } from "@/lib/ontologyFinding";
import { useText } from "@/lib/i18n";
import { ONTOLOGY_TEXT } from "@/lib/ontologyStrings";
import { cn } from "@/lib/utils";

/** Headline counts: a small label over each number. */
export function Stats({ fields, warnColor, className }: { fields: ReadonlyArray<Field>; warnColor?: string; className?: string }) {
  if (!fields.length) return null;
  return (
    <dl className={cn("grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))]", className)}>
      {fields.map((f) => (
        <div key={f.label} className="min-w-0" data-warn={f.warn ? "1" : undefined}>
          <dt className="truncate text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{f.label}</dt>
          <dd className="truncate text-lg font-semibold tracking-tight text-foreground tabular-nums" style={f.warn && warnColor ? { color: warnColor } : undefined}>
            {f.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/** Smaller label → value lines under the counts. */
export function Facts({ fields, className }: { fields: ReadonlyArray<Field>; className?: string }) {
  if (!fields.length) return null;
  return (
    <dl className={cn("grid gap-x-3 gap-y-0.5 text-xs sm:grid-cols-[9rem_minmax(0,1fr)]", className)}>
      {fields.map((f) => (
        <div key={f.label} className="contents">
          <dt className="text-muted-foreground">{f.label}</dt>
          <dd className="font-mono text-foreground tabular-nums [overflow-wrap:anywhere]">{f.value}</dd>
        </div>
      ))}
    </dl>
  );
}

const TONE: Record<FindingT["tone"], string> = {
  neutral: "border-border",
  warn: "border-l-4",
  bad: "border-l-4 border-l-destructive",
};

/**
 * The finding, before the picture. On most states this is the whole answer
 * and the diagram under it is empty or beside the point, so it comes first on
 * the page rather than as a footnote under a drawing.
 */
export function Finding({ f, draftColor }: { f: FindingT; draftColor: string }) {
  const t = useText(ONTOLOGY_TEXT);
  const badge: CSSProperties | undefined =
    f.tone === "warn" ? { borderColor: draftColor, color: draftColor } : undefined;
  return (
    <section
      aria-label={t.found}
      className={cn("space-y-3 rounded-xl border bg-card px-4 py-3 text-sm", TONE[f.tone])}
      style={f.tone === "warn" ? { borderLeftColor: draftColor } : undefined}
      data-finding={f.tone}
    >
      {f.status && (
        <Badge variant={f.tone === "bad" ? "destructive" : "outline"} className="font-medium uppercase tracking-wide text-[10px]" style={badge}>
          {f.status}
        </Badge>
      )}
      <Stats fields={f.stats} warnColor={draftColor} />
      <Facts fields={f.facts} />
      {f.error && <p className="text-xs text-destructive [overflow-wrap:anywhere]">{f.error}</p>}
    </section>
  );
}
