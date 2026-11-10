// The ontology page: not what is in the brain, but what the brain is allowed
// to talk about, held against what it actually holds.
//
// Nothing here decides what is true. Which finding the page shows is the
// report's state, computed in Go; the page picks a sentence by it and never
// infers one from an empty list. The drawing is lib/ontologyLayout's lanes,
// for the reasons written at the top of that file, and a draft goes through
// the same renderer as a saved schema because the overlay is what makes a
// draft judgeable.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { History } from "lucide-react";
import { Link, useSearchParams } from "react-router";
import { isDraft, type OntologyAnswer } from "@/api/ontology";
import { Page, PageHeader } from "@/components/Page";
import { Against, Decisions } from "@/components/ontology/Decisions";
import { Detail } from "@/components/ontology/Detail";
import { Diagram } from "@/components/ontology/Diagram";
import { Controls } from "@/components/ontology/Controls";
import { Finding } from "@/components/ontology/Finding";
import { Legend } from "@/components/ontology/Legend";
import { Outside } from "@/components/ontology/Outside";
import { usePalette } from "@/components/ontology/usePalette";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { pageParts, readOntologyOptions, versionsPath, writeOntologyOptions, type OntologyOptions } from "@/lib/ontology";
import { finding, headerOf, outsideTheModel, type Pill } from "@/lib/ontologyFinding";
import { drawsDiagram, type Selection } from "@/lib/ontologyLayout";
import type { OntologyPalette } from "@/lib/ontologyPalette";
import { useOntology } from "@/lib/useOntology";
import { cn } from "@/lib/utils";
import { pageShown } from "@/lib/pages";
import { useLang, type Lang } from "@/lib/i18n";
import { ONTOLOGY_TEXT } from "@/lib/ontologyStrings";

function PillBadge({ p, pal }: { p: Pill; pal: OntologyPalette }) {
  const style =
    p.tone === "draft"
      ? { borderColor: pal.draft, color: pal.draftInk, background: `color-mix(in oklab, ${pal.draft} 12%, var(--card))` }
      : p.tone === "warn"
        ? { borderColor: `color-mix(in oklab, ${pal.status} 50%, var(--border))`, color: pal.status }
        : undefined;
  return (
    <Badge variant="outline" title={p.title} data-pill={p.tone} className={cn("font-medium uppercase tracking-wide text-[10px]", p.tone === "cold" && "text-muted-foreground", p.tone === "on" && "border-foreground/30")} style={style}>
      {p.label}
    </Badge>
  );
}

function HeaderLine({ rep, pal, lang }: { rep: OntologyAnswer | null; pal: OntologyPalette; lang: Lang }) {
  if (!rep) return <span className="text-muted-foreground">{ONTOLOGY_TEXT[lang].reading}</span>;
  const h = headerOf(rep, lang);
  return (
    <span className="flex flex-wrap items-center gap-x-1.5 gap-y-1">
      {h.title && <b className="font-semibold text-foreground">{h.title}</b>}
      {h.title && <span aria-hidden>·</span>}
      <span>{h.detail}</span>
      {h.pills.map((p) => (
        <PillBadge key={p.label} p={p} pal={pal} />
      ))}
    </span>
  );
}

function Skeletons({ label }: { label: string }) {
  return (
    <div className="space-y-4" aria-busy aria-label={label}>
      <Skeleton className="h-16 w-full rounded-xl" />
      <div className="space-y-3 rounded-xl border bg-card p-4">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex gap-4">
            <Skeleton className="hidden h-14 w-36 shrink-0 rounded-lg sm:block" />
            <Skeleton className="h-16 w-44 rounded-lg" />
            <Skeleton className="hidden h-16 w-44 rounded-lg md:block" />
          </div>
        ))}
      </div>
    </div>
  );
}

export function OntologyPage() {
  const [params, setParams] = useSearchParams();
  const opts = useMemo(() => readOntologyOptions(params), [params]);
  const set = useCallback((patch: Partial<OntologyOptions>) => setParams(writeOntologyOptions({ ...opts, ...patch }, params), { replace: true }), [opts, params, setParams]);
  const pal = usePalette();
  const { rep, loading, reload } = useOntology(opts);
  const lang = useLang();
  const t = ONTOLOGY_TEXT[lang];

  // A selection survives a refresh, or every poll would close the panel a
  // reader had open on the type they were studying. It does not survive the
  // draft switch: a draft's schema is a different one, and the panel would
  // reopen on a name that now means something else.
  const [sel, setSel] = useState<Selection | null>(null);
  const draftWas = useRef(opts.draft);
  useEffect(() => {
    if (draftWas.current !== opts.draft) setSel(null);
    draftWas.current = opts.draft;
  }, [opts.draft]);

  const parts = pageParts(opts);
  const f = rep ? finding(rep, lang) : null;
  const band = rep ? outsideTheModel(rep, parts.outside, lang) : null;
  const draft = rep && isDraft(rep) ? rep : null;

  return (
    <div className="min-h-full" style={opts.bg ? { background: opts.bg } : undefined}>
      <Page className="max-w-7xl space-y-4">
        {parts.header && (
          <PageHeader
            title={t.title}
            description={<HeaderLine rep={rep} pal={pal} lang={lang} />}
            actions={
              pageShown("/ontology-history") && (
                <Button variant="outline" size="sm" asChild>
                  <Link to={versionsPath(rep)} title={t.versionsTip}>
                    <History aria-hidden />
                    {t.versions}
                  </Link>
                </Button>
              )
            }
          />
        )}
        {parts.controls && <Controls opts={opts} set={set} rep={rep} loading={loading} reload={reload} pal={pal} />}

        {!rep || !f ? (
          <Skeletons label={t.reading.replace("…", "")} />
        ) : (
          <>
            {parts.finding && <Finding f={f} draftColor={pal.draft} />}
            {parts.diagram && drawsDiagram(rep) && <Diagram rep={rep} pal={pal} sel={sel} onSelect={setSel} />}
            {band && <Outside band={band} pal={pal} />}
            {parts.review && draft && <Decisions key={draft.schema_id ?? "draft"} rep={draft} pal={pal} />}
            {parts.review && draft && <Against rep={draft} pal={pal} />}
            {parts.key && drawsDiagram(rep) && <Legend pal={pal} />}
            {parts.detail && <Detail rep={rep} sel={sel} pal={pal} onClose={() => setSel(null)} />}
          </>
        )}
      </Page>
    </div>
  );
}
