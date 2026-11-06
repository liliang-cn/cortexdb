import { useState, type ReactNode } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { OntologyPalette } from "@/lib/ontologyPalette";

// Folded or not is remembered under the key the Go-served page used, so a
// reader who folded the key there finds it folded here.
const FOLD_KEY = "cortexdb.ontology.folded";

function readFolded(): boolean {
  try {
    return (localStorage.getItem(FOLD_KEY) ?? "").split(",").includes("legend");
  } catch {
    return false;
  }
}

function writeFolded(folded: boolean) {
  try {
    const ids = (localStorage.getItem(FOLD_KEY) ?? "").split(",").filter((id) => id && id !== "legend");
    if (folded) ids.push("legend");
    localStorage.setItem(FOLD_KEY, ids.join(","));
  } catch {
    // storage can be blocked; the fold still applies for this visit
  }
}

function Item({ swatch, mark, children }: { swatch?: ReactNode; mark?: ReactNode; children: ReactNode }) {
  return (
    <li className="flex items-center gap-3">
      <span aria-hidden className="flex w-16 shrink-0 items-center">
        {mark ?? (
          <svg width={34} height={20} viewBox="0 0 34 20">
            {swatch}
          </svg>
        )}
      </span>
      <span>{children}</span>
    </li>
  );
}

function Glyph({ g, fk, pal }: { g: string; fk?: boolean; pal: OntologyPalette }) {
  return (
    <>
      <circle cx={17} cy={10} r={8} stroke={pal.link} fill={fk ? pal.fk : undefined} className={fk ? undefined : "fill-card"} />
      <text x={17} y={14} fontSize={11} textAnchor="middle" fill={fk ? pal.fkInk : undefined} className={fk ? undefined : "fill-muted-foreground"}>
        {g}
      </text>
    </>
  );
}

/** The key: what each mark on the diagram means. */
export function Legend({ pal }: { pal: OntologyPalette }) {
  const [folded, setFolded] = useState(readFolded);
  const flip = () => {
    setFolded((f) => {
      writeFolded(!f);
      return !f;
    });
  };
  return (
    <section aria-label="Key" className="rounded-xl border bg-card px-4 py-2.5 text-sm">
      <Button variant="ghost" size="sm" className="-ml-2 px-2 text-xs font-medium uppercase tracking-wide text-muted-foreground" aria-expanded={!folded} onClick={flip}>
        {folded ? <ChevronRight aria-hidden /> : <ChevronDown aria-hidden />}
        Key
      </Button>
      {!folded && (
        <ul className="mt-1 grid gap-x-6 gap-y-2 pb-1.5 text-muted-foreground md:grid-cols-2">
          <Item swatch={<Glyph g="1" pal={pal} />}>This end reaches exactly one object, and it is the end that carries the foreign key.</Item>
          <Item swatch={<Glyph g="∗" pal={pal} />}>This end reaches many.</Item>
          <Item swatch={<Glyph g="1" fk pal={pal} />}>A foreign key property is declared on this side.</Item>
          <Item swatch={<Glyph g="?" pal={pal} />}>The schema leaves this side's cardinality undeclared.</Item>
          <Item swatch={<rect x={2} y={3} width={30} height={14} rx={3} className="fill-card/40 stroke-muted-foreground" strokeDasharray="4 3" />}>
            Dashed box: declared, and nothing in the store is of this type.
          </Item>
          <Item mark={<i className="text-[10px] text-muted-foreground">not counted</i>}>The gap was not measured, so the box has no number, not even zero.</Item>
          <Item swatch={<path d="M2,10 L32,10" stroke={pal.link} strokeWidth={1.6} />}>A link type edges in the store carry.</Item>
          <Item swatch={<path d="M2,10 L32,10" stroke={pal.linkIdle} strokeWidth={1.2} strokeDasharray="5 4" />}>A link type nothing uses, or one whose count was not taken.</Item>
          <Item swatch={<rect x={2} y={2} width={30} height={16} rx={4} className="fill-muted/45 stroke-border" />}>A lane groups the object types implementing one interface.</Item>
          <Item
            swatch={
              <>
                <rect x={8} y={2} width={24} height={16} rx={3} className="fill-card stroke-border" />
                <circle cx={12} cy={7} r={2.5} fill={pal.lanes[0]} />
                <circle cx={12} cy={13} r={2.5} fill={pal.lanes[1]} />
              </>
            }
          >
            Dots down a box's edge: the interfaces it implements, in their lanes' colours.
          </Item>
        </ul>
      )}
    </section>
  );
}
