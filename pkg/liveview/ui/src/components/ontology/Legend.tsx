import { useState, type ReactNode } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useText } from "@/lib/i18n";
import type { OntologyPalette } from "@/lib/ontologyPalette";
import { ONTOLOGY_TEXT } from "@/lib/ontologyStrings";

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
  const t = useText(ONTOLOGY_TEXT);
  const flip = () => {
    setFolded((f) => {
      writeFolded(!f);
      return !f;
    });
  };
  return (
    <section aria-label={t.key} className="rounded-xl border bg-card px-4 py-2.5 text-sm">
      <Button variant="ghost" size="sm" className="-ml-2 px-2 text-xs font-medium uppercase tracking-wide text-muted-foreground" aria-expanded={!folded} onClick={flip}>
        {folded ? <ChevronRight aria-hidden /> : <ChevronDown aria-hidden />}
        {t.key}
      </Button>
      {!folded && (
        <ul className="mt-1 grid gap-x-6 gap-y-2 pb-1.5 text-muted-foreground md:grid-cols-2">
          <Item swatch={<Glyph g="1" pal={pal} />}>{t.keyOne}</Item>
          <Item swatch={<Glyph g="∗" pal={pal} />}>{t.keyMany}</Item>
          <Item swatch={<Glyph g="1" fk pal={pal} />}>{t.keyFk}</Item>
          <Item swatch={<Glyph g="?" pal={pal} />}>{t.keyUndeclared}</Item>
          <Item swatch={<rect x={2} y={3} width={30} height={14} rx={3} className="fill-card/40 stroke-muted-foreground" strokeDasharray="4 3" />}>
            {t.keyDashed}
          </Item>
          <Item mark={<i className="text-[10px] text-muted-foreground">{t.keyNotCountedMark}</i>}>{t.keyNotCounted}</Item>
          <Item swatch={<path d="M2,10 L32,10" stroke={pal.link} strokeWidth={1.6} />}>{t.keyLive}</Item>
          <Item swatch={<path d="M2,10 L32,10" stroke={pal.linkIdle} strokeWidth={1.2} strokeDasharray="5 4" />}>{t.keyIdle}</Item>
          <Item swatch={<rect x={2} y={2} width={30} height={16} rx={4} className="fill-muted/45 stroke-border" />}>{t.keyLane}</Item>
          <Item
            swatch={
              <>
                <rect x={8} y={2} width={24} height={16} rx={3} className="fill-card stroke-border" />
                <circle cx={12} cy={7} r={2.5} fill={pal.lanes[0]} />
                <circle cx={12} cy={13} r={2.5} fill={pal.lanes[1]} />
              </>
            }
          >
            {t.keyDots}
          </Item>
        </ul>
      )}
    </section>
  );
}
