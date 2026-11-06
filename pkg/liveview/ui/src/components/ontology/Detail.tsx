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
import { num } from "@/lib/ontologyFinding";
import { cardinalityWord, counted, resolveSelection, type Selection } from "@/lib/ontologyLayout";
import type { OntologyPalette } from "@/lib/ontologyPalette";

function K({ children }: { children: ReactNode }) {
  return <dt className="mt-3 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{children}</dt>;
}

function V({ children }: { children: ReactNode }) {
  return <dd className="text-sm [overflow-wrap:anywhere]">{children}</dd>;
}

function Props({ props }: { props: OntologyProp[] }) {
  if (!props.length) return <V><span className="text-muted-foreground">no properties declared</span></V>;
  return (
    <dd>
      <table className="mt-1 w-full border-collapse text-xs">
        <tbody>
          {props.map((p) => {
            const flags = [p.searchable && "searchable", p.vectorized && "vectorized"].filter(Boolean).join(" · ");
            return (
              <tr key={p.api_name} className="border-b border-border/60 last:border-0">
                <td className="py-1 pr-2 align-top [overflow-wrap:anywhere]">
                  <span className="font-mono">{p.api_name}</span>
                  {p.required && <span className="ml-1.5 text-[11px] font-medium text-foreground">required</span>}
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

function ObjectDetail({ ot, isCounted }: { ot: OntologyObjectNode; isCounted: boolean }) {
  return (
    <>
      {ot.display_name && ot.display_name !== ot.api_name && <p className="text-sm text-muted-foreground">{ot.display_name}</p>}
      {ot.description && <p className="mt-1 text-sm text-muted-foreground">{ot.description}</p>}
      <dl>
        <K>In the store</K>
        <V>
          {isCounted
            ? ot.instances
              ? `${num(ot.instances)} ${ot.instances === 1 ? "node carries" : "nodes carry"} this type`
              : "nothing in the store is of this type"
            : "not counted — the gap was not measured"}
        </V>
        <K>Primary key</K>
        <V><span className="font-mono">{ot.primary_key || "—"}</span></V>
        {ot.title_property && (
          <>
            <K>Title property</K>
            <V><span className="font-mono">{ot.title_property}</span></V>
          </>
        )}
        {(ot.implements ?? []).length > 0 && (
          <>
            <K>Implements</K>
            <V>{ot.implements.join(", ")}</V>
          </>
        )}
        {(ot.aliases ?? []).length > 0 && (
          <>
            <K>Aliases</K>
            <V>{ot.aliases?.join(", ")}</V>
          </>
        )}
        {ot.status && (
          <>
            <K>Status</K>
            <V>
              {ot.status}
              {ot.visibility ? ` · ${ot.visibility}` : ""}
            </V>
          </>
        )}
        <K>Properties ({ot.properties.length})</K>
        <Props props={ot.properties} />
      </dl>
    </>
  );
}

function Side({ label, s, warn }: { label: string; s: OntologyLinkEnd; warn: string }) {
  return (
    <>
      <K>
        {label} · {s.api_name || "—"}
      </K>
      <V>
        {s.object_type} → reaches <b className="font-semibold">{cardinalityWord(s.cardinality)}</b>
        {s.foreign_key ? (
          <span className="block text-xs text-muted-foreground">
            carries the foreign key: <span className="font-mono">{s.foreign_key}</span>
          </span>
        ) : s.cardinality === "ONE" ? (
          // A ONE side with no key property named is not an error, but it is
          // the half of the declaration that says where the column lives, and
          // its absence is worth seeing rather than reading as "no key here".
          <span className="block text-xs" style={{ color: warn }}>
            single-valued, but no foreign key property is declared
          </span>
        ) : null}
      </V>
    </>
  );
}

function LinkDetail({ lt, isCounted, warn }: { lt: OntologyLinkEdge; isCounted: boolean; warn: string }) {
  return (
    <>
      {lt.description && <p className="text-sm text-muted-foreground">{lt.description}</p>}
      <dl>
        <K>Multiplicity</K>
        <V>{lt.multiplicity || "not declared on both sides"}</V>
        <K>In the store</K>
        <V>
          {isCounted
            ? lt.instances
              ? `${num(lt.instances)} ${lt.instances === 1 ? "edge carries" : "edges carry"} this type`
              : "no edge in the store carries this type"
            : "not counted — the gap was not measured"}
        </V>
        <Side label="Side A" s={lt.a} warn={warn} />
        <Side label="Side B" s={lt.b} warn={warn} />
        {lt.status && (
          <>
            <K>Status</K>
            <V>{lt.status}</V>
          </>
        )}
      </dl>
    </>
  );
}

function InterfaceDetail({ it, warn }: { it: OntologyInterfaceNode; warn: string }) {
  const impl = it.implementors ?? [];
  return (
    <>
      {it.description && <p className="text-sm text-muted-foreground">{it.description}</p>}
      <dl>
        {(it.extends ?? []).length > 0 && (
          <>
            <K>Extends</K>
            <V>{it.extends?.join(", ")}</V>
          </>
        )}
        <K>Implemented by ({impl.length})</K>
        <V>{impl.length ? impl.join(", ") : <span style={{ color: warn }}>nothing implements this interface</span>}</V>
        <K>Shared properties ({it.properties.length})</K>
        <Props props={it.properties} />
      </dl>
    </>
  );
}

const KIND_WORD = { object: "Object type", link: "Link type", interface: "Interface" } as const;

export function Detail({ rep, sel, pal, onClose }: { rep: OntologyAnswer; sel: Selection | null; pal: OntologyPalette; onClose: () => void }) {
  const narrow = useIsNarrow();
  const hit = resolveSelection(rep, sel);
  if (!hit) return null;
  const isCounted = counted(rep);
  const title = hit.kind === "object" ? hit.ot.api_name : hit.kind === "link" ? hit.lt.api_name : hit.it.api_name;
  const body =
    hit.kind === "object" ? <ObjectDetail ot={hit.ot} isCounted={isCounted} /> : hit.kind === "link" ? <LinkDetail lt={hit.lt} isCounted={isCounted} warn={pal.status} /> : <InterfaceDetail it={hit.it} warn={pal.status} />;

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
        <Button variant="ghost" size="icon-sm" className="-mr-2 size-7" onClick={onClose} aria-label="Close">
          <X aria-hidden />
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4 pt-1">{body}</div>
    </aside>
  );
}
