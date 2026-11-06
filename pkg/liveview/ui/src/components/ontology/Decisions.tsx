// The review half of a draft.
//
// A draft drawn as a picture alone is the picture with the work hidden behind
// it. Every decision here is a question the data cannot answer, which is why
// they are a list beside the draft rather than a value inside it, and nothing
// here answers one: this page reviews, and a person saves.
//
// Two readings, in this order. The tally is the glance: how much of each kind
// of question there is, because seven merge candidates and one guessed key are
// two different afternoons. The groups under it are the reading, open by
// default and foldable, because a review that starts folded is a review
// somebody skips.

import { useRef, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import type { OntologyDraftView } from "@/api/ontology";
import { Button } from "@/components/ui/button";
import { againstSummary, num } from "@/lib/ontologyFinding";
import type { OntologyPalette } from "@/lib/ontologyPalette";
import { cn } from "@/lib/utils";
import { Runs } from "./Finding";

function frame(pal: OntologyPalette) {
  return { borderColor: `color-mix(in oklab, ${pal.draft} 35%, var(--border))`, background: `color-mix(in oklab, ${pal.draft} 4%, var(--card))` };
}

export function Decisions({ rep, pal }: { rep: OntologyDraftView; pal: OntologyPalette }) {
  const groups = rep.decisions ?? [];
  const [shut, setShut] = useState<ReadonlySet<string>>(() => new Set());
  const refs = useRef(new Map<string, HTMLElement>());
  if (!groups.length) return null;

  const toggle = (kind: string) =>
    setShut((s) => {
      const next = new Set(s);
      if (next.has(kind)) next.delete(kind);
      else next.add(kind);
      return next;
    });
  const jump = (kind: string) => {
    setShut((s) => {
      const next = new Set(s);
      next.delete(kind);
      return next;
    });
    refs.current.get(kind)?.scrollIntoView({ block: "start", behavior: "smooth" });
  };

  return (
    <section aria-label="To decide" className="rounded-xl border px-4 py-3" style={frame(pal)} data-decisions>
      <h2 className="text-[11px] font-semibold uppercase tracking-wide" style={{ color: pal.draftInk }}>
        To decide · {num(rep.decisions_total)}
      </h2>
      <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
        The half of a draft that is not a picture. Nothing here is decided by this page or by the deriver that raised it — a schema is saved by a person, through{" "}
        <b className="font-semibold text-foreground">ontology_save</b>.
      </p>
      <div className="mt-2.5 flex flex-wrap gap-1.5" aria-label="How many of each kind">
        {groups.map((g) => (
          <Button key={g.kind} variant="outline" size="xs" className="h-auto min-h-6 whitespace-normal py-0.5 text-left font-normal" onClick={() => jump(g.kind)}>
            {g.title}
            <b className="font-semibold tabular-nums" style={{ color: pal.draftInk }}>
              {num(g.count)}
            </b>
          </Button>
        ))}
      </div>
      {groups.map((g) => {
        const open = !shut.has(g.kind);
        const shown = g.decisions ?? [];
        return (
          <div
            key={g.kind}
            ref={(el) => {
              if (el) refs.current.set(g.kind, el);
              else refs.current.delete(g.kind);
            }}
            className="mt-3 scroll-mt-16 border-t pt-2.5"
          >
            <Button variant="ghost" size="sm" className="-ml-2 h-auto w-[calc(100%+1rem)] justify-start whitespace-normal px-2 py-1 text-left" aria-expanded={open} onClick={() => toggle(g.kind)}>
              {open ? <ChevronDown aria-hidden /> : <ChevronRight aria-hidden />}
              <span className="font-semibold" style={{ color: pal.draftInk }}>
                {g.title}
              </span>
              <span className="rounded-full px-1.5 text-[11px] font-semibold tabular-nums" style={{ background: pal.draft, color: "var(--card)" }}>
                {num(g.count)}
              </span>
              <span className="ml-auto hidden font-mono text-[11px] font-normal text-muted-foreground sm:inline">{g.kind}</span>
            </Button>
            {open && (
              <div className="pl-6">
                <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{g.question}</p>
                <ul className="mt-2 space-y-2">
                  {shown.map((d, i) => (
                    <li key={`${d.target}-${i}`} className="border-l-2 pl-2.5">
                      <p className="font-mono text-xs font-semibold [overflow-wrap:anywhere]">{d.target}</p>
                      <p className="text-xs leading-relaxed text-muted-foreground">{d.detail}</p>
                      {d.evidence && <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground/80 tabular-nums">{d.evidence}</p>}
                    </li>
                  ))}
                </ul>
                {/* The cap is on the list, never on the count: a reader is told
                    how many there are and shown as many as a page can hold. */}
                {g.truncated && <p className="mt-2 text-xs text-muted-foreground">and {num(g.count - shown.length)} more of this kind, not listed</p>}
              </div>
            )}
          </div>
        );
      })}
    </section>
  );
}

/**
 * The draft held against a schema somebody did save. The vocabulary here is
 * ontology_diff's, and it is right exactly here and nowhere else on the page:
 * both sides are schemas, so "added", "removed" and "breaking" describe what
 * they say. Held against data instead, which is what the rest of the page
 * does, those words would be claiming somebody changed something.
 */
export function Against({ rep, pal }: { rep: OntologyDraftView; pal: OntologyPalette }) {
  const runs = againstSummary(rep);
  if (!runs) return null;
  const changes = rep.changes ?? [];
  const total = rep.changes_total ?? changes.length;
  return (
    <section aria-label="Against the saved schema" className="rounded-xl border px-4 py-3" style={frame(pal)} data-against>
      <h2 className="text-[11px] font-semibold uppercase tracking-wide" style={{ color: pal.draftInk }}>
        Against the saved schema · {rep.against} v{num(rep.against_version)}
      </h2>
      <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
        <Runs runs={runs} />
      </p>
      <ul className="mt-2 space-y-1.5">
        {changes.map((c, i) => (
          <li key={`${c.kind}-${c.target}-${i}`} className="grid gap-x-3 text-xs sm:grid-cols-[11rem_minmax(0,1fr)]">
            <span className={cn("font-mono text-[10.5px] uppercase tracking-wide", c.breaking ? "text-destructive" : "text-muted-foreground")}>
              {c.kind.replaceAll("_", " ")}
              {c.breaking && <span className="sr-only"> (breaking)</span>}
            </span>
            <span className="[overflow-wrap:anywhere]">
              <b className="font-semibold">{c.target}</b> <span className="text-muted-foreground">{c.detail}</span>
            </span>
          </li>
        ))}
      </ul>
      {total > changes.length && <p className="mt-2 text-xs text-muted-foreground">and {num(total - changes.length)} more, not listed</p>}
    </section>
  );
}
