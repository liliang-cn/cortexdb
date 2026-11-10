// What a reader sees after clicking a box, a curve or an interface.
//
// On a wide screen it floats over the right of the diagram rather than taking
// a column beside it: a column would narrow the canvas, the layout would put
// fewer boxes in a row, and every box would move the moment one was clicked.
// On a phone it is a sheet from the bottom, as the graph page's panels are.

import type { ReactNode } from "react";
import { X } from "lucide-react";
import type { OntologyAnswer, OntologyInterfaceNode, OntologyLinkEdge, OntologyLinkEnd, OntologyObjectNode, OntologyProp } from "@/api/ontology";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { useIsNarrow } from "@/components/graph/primitives";
import { cardinalityWord, counted, resolveSelection, type Selection } from "@/lib/ontologyLayout";
import { useLang, type Lang } from "@/lib/i18n";
import type { OntologyPalette } from "@/lib/ontologyPalette";
import { ONTOLOGY_TEXT, type OntologyText } from "@/lib/ontologyStrings";

function K({ children }: { children: ReactNode }) {
  return <dt className="mt-3 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{children}</dt>;
}

function V({ children }: { children: ReactNode }) {
  return <dd className="text-sm [overflow-wrap:anywhere]">{children}</dd>;
}

function Props({ props, t }: { props: OntologyProp[]; t: OntologyText }) {
  if (!props.length) return <V><span className="text-muted-foreground">{t.noProps}</span></V>;
  return (
    <dd>
      <table className="mt-1 w-full border-collapse text-xs">
        <tbody>
          {props.map((p) => {
            const flags = [p.searchable && t.searchable, p.vectorized && t.vectorized].filter(Boolean).join(" · ");
            return (
              <tr key={p.api_name} className="border-b border-border/60 last:border-0">
                <td className="py-1 pr-2 align-top [overflow-wrap:anywhere]">
                  <span className="font-mono">{p.api_name}</span>
                  {p.required && <span className="ml-1.5 text-[11px] font-medium text-foreground">{t.required}</span>}
                  {flags && <span className="ml-1.5 text-[11px] text-muted-foreground">{flags}</span>}
                </td>
                <td className="whitespace-nowrap py-1 text-right align-top text-muted-foreground">{p.kind || "?"}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </dd>
  );
}

function ObjectDetail({ ot, isCounted, t }: { ot: OntologyObjectNode; isCounted: boolean; t: OntologyText }) {
  return (
    <>
      {ot.display_name && ot.display_name !== ot.api_name && <p className="text-sm text-muted-foreground">{ot.display_name}</p>}
      {ot.description && <p className="mt-1 text-sm text-muted-foreground">{ot.description}</p>}
      <dl>
        <K>{t.inStore}</K>
        <V>{isCounted ? (ot.instances ? t.nodesCarry(ot.instances) : t.noNodes) : t.notCountedGap}</V>
        <K>{t.primaryKey}</K>
        <V><span className="font-mono">{ot.primary_key || "—"}</span></V>
        {ot.title_property && (
          <>
            <K>{t.titleProperty}</K>
            <V><span className="font-mono">{ot.title_property}</span></V>
          </>
        )}
        {(ot.implements ?? []).length > 0 && (
          <>
            <K>{t.implementsK}</K>
            <V>{ot.implements.join(", ")}</V>
          </>
        )}
        {(ot.aliases ?? []).length > 0 && (
          <>
            <K>{t.aliases}</K>
            <V>{ot.aliases?.join(", ")}</V>
          </>
        )}
        {ot.status && (
          <>
            <K>{t.status}</K>
            <V>
              {ot.status}
              {ot.visibility ? ` · ${ot.visibility}` : ""}
            </V>
          </>
        )}
        <K>{t.properties(ot.properties.length)}</K>
        <Props props={ot.properties} t={t} />
      </dl>
    </>
  );
}

function Side({ label, s, warn, t, lang }: { label: string; s: OntologyLinkEnd; warn: string; t: OntologyText; lang: Lang }) {
  return (
    <>
      <K>
        {label} · {s.api_name || "—"}
      </K>
      <V>
        {s.object_type} → {t.reaches} <b className="font-semibold">{cardinalityWord(s.cardinality, lang)}</b>
        {s.foreign_key ? (
          <span className="block text-xs text-muted-foreground">
            {t.carriesFk} <span className="font-mono">{s.foreign_key}</span>
          </span>
        ) : s.cardinality === "ONE" ? (
          // A ONE side with no key property named is not an error, but it is
          // the half of the declaration that says where the column lives, and
          // its absence is worth seeing rather than reading as "no key here".
          <span className="block text-xs" style={{ color: warn }}>
            {t.oneNoFk}
          </span>
        ) : null}
      </V>
    </>
  );
}

function LinkDetail({ lt, isCounted, warn, t, lang }: { lt: OntologyLinkEdge; isCounted: boolean; warn: string; t: OntologyText; lang: Lang }) {
  return (
    <>
      {lt.description && <p className="text-sm text-muted-foreground">{lt.description}</p>}
      <dl>
        <K>{t.multiplicity}</K>
        <V>{lt.multiplicity || t.notBothSides}</V>
        <K>{t.inStore}</K>
        <V>{isCounted ? (lt.instances ? t.edgesCarry(lt.instances) : t.noEdges) : t.notCountedGap}</V>
        <Side label={t.sideA} s={lt.a} warn={warn} t={t} lang={lang} />
        <Side label={t.sideB} s={lt.b} warn={warn} t={t} lang={lang} />
        {lt.status && (
          <>
            <K>{t.status}</K>
            <V>{lt.status}</V>
          </>
        )}
      </dl>
    </>
  );
}

function InterfaceDetail({ it, warn, t }: { it: OntologyInterfaceNode; warn: string; t: OntologyText }) {
  const impl = it.implementors ?? [];
  return (
    <>
      {it.description && <p className="text-sm text-muted-foreground">{it.description}</p>}
      <dl>
        {(it.extends ?? []).length > 0 && (
          <>
            <K>{t.extendsK}</K>
            <V>{it.extends?.join(", ")}</V>
          </>
        )}
        <K>{t.implementedBy(impl.length)}</K>
        <V>{impl.length ? impl.join(", ") : <span style={{ color: warn }}>{t.nothingImplements}</span>}</V>
        <K>{t.sharedProps(it.properties.length)}</K>
        <Props props={it.properties} t={t} />
      </dl>
    </>
  );
}

export function Detail({ rep, sel, pal, onClose }: { rep: OntologyAnswer; sel: Selection | null; pal: OntologyPalette; onClose: () => void }) {
  const narrow = useIsNarrow();
  const lang = useLang();
  const t = ONTOLOGY_TEXT[lang];
  const KIND_WORD = { object: t.objectType, link: t.linkType, interface: t.iface } as const;
  const hit = resolveSelection(rep, sel);
  if (!hit) return null;
  const isCounted = counted(rep);
  const title = hit.kind === "object" ? hit.ot.api_name : hit.kind === "link" ? hit.lt.api_name : hit.it.api_name;
  const body =
    hit.kind === "object" ? (
      <ObjectDetail ot={hit.ot} isCounted={isCounted} t={t} />
    ) : hit.kind === "link" ? (
      <LinkDetail lt={hit.lt} isCounted={isCounted} warn={pal.status} t={t} lang={lang} />
    ) : (
      <InterfaceDetail it={hit.it} warn={pal.status} t={t} />
    );

  if (narrow) {
    return (
      <Sheet open onOpenChange={(open) => !open && onClose()}>
        <SheetContent side="bottom" className="max-h-[75dvh] gap-0 overflow-y-auto rounded-t-2xl pb-[env(safe-area-inset-bottom)]">
          <SheetHeader className="pb-1">
            <SheetTitle className="pr-6 [overflow-wrap:anywhere]">{title}</SheetTitle>
            <SheetDescription>{KIND_WORD[hit.kind]}</SheetDescription>
          </SheetHeader>
          <div className="px-4 pb-5">{body}</div>
        </SheetContent>
      </Sheet>
    );
  }
  return (
    <aside
      aria-label={`${KIND_WORD[hit.kind]} ${title}`}
      className="fixed right-4 top-16 z-20 flex max-h-[calc(100dvh-5rem)] w-80 flex-col overflow-hidden rounded-xl border bg-card/95 text-card-foreground shadow-[0_1px_2px_rgb(0_0_0/0.04),0_8px_24px_-12px_rgb(0_0_0/0.25)] backdrop-blur-md"
    >
      <header className="flex items-start gap-2 border-b px-4 py-2.5">
        <div className="min-w-0 flex-1">
          <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{KIND_WORD[hit.kind]}</p>
          <h2 className="text-sm font-semibold [overflow-wrap:anywhere]">{title}</h2>
        </div>
        <Button variant="ghost" size="icon-sm" className="-mr-2 size-7" onClick={onClose} aria-label={t.close}>
          <X aria-hidden />
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4 pt-1">{body}</div>
    </aside>
  );
}
