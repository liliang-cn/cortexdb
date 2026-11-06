import type { Finding as FindingT, Run } from "@/lib/ontologyFinding";
import { cn } from "@/lib/utils";

/** A sentence from lib/ontologyFinding, with its set-off runs drawn as such. */
export function Runs({ runs }: { runs: ReadonlyArray<Run> }) {
  return (
    <>
      {runs.map((r, i) =>
        typeof r === "string" ? (
          <span key={i}>{r}</span>
        ) : "b" in r ? (
          <b key={i} className="font-semibold text-foreground">
            {r.b}
          </b>
        ) : (
          <em key={i}>{r.em}</em>
        ),
      )}
    </>
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
  return (
    <section
      aria-label="What this page found"
      className={cn("rounded-xl border bg-card px-4 py-3 text-sm leading-relaxed text-muted-foreground", TONE[f.tone])}
      style={f.tone === "warn" ? { borderLeftColor: draftColor } : undefined}
    >
      <p>
        <Runs runs={f.runs} />
      </p>
      {f.foot.map((line, i) => (
        <p key={i} className="mt-2 text-xs leading-relaxed text-muted-foreground/90 [overflow-wrap:anywhere]">
          <Runs runs={line} />
        </p>
      ))}
      {f.notes.length > 0 && (
        <ul className="mt-2 space-y-0.5 text-xs leading-relaxed text-muted-foreground/90">
          {f.notes.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      )}
    </section>
  );
}
