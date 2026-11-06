import { useMemo, useState } from "react";
import { Eye, EyeOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { CO_OCCURS, heatColor, predicateColor, typeColor } from "@/lib/graph/colors";
import { predicateCounts, predicateShown, typeCounts } from "@/lib/graph/model";
import { GRADES, GRADE_ORDER, grade } from "@/lib/semantics";
import { useCapabilities } from "@/live";
import { cn } from "@/lib/utils";
import { useDark, useMerged } from "./hooks";
import { useGraphUI } from "./state";
import { Hint, SectionTitle } from "./primitives";
import { useRecallHeat } from "./useRecallHeat";

const COLLAPSED_ROWS = 7;

function Row({ on, onClick, swatch, label, count, title }: { on: boolean; onClick: () => void; swatch: React.ReactNode; label: string; count: number; title?: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={on}
      title={title ?? (on ? `Hide ${label}` : `Show ${label}`)}
      className={cn(
        "group flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left text-[13px] outline-none transition-colors hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50",
        !on && "text-muted-foreground",
      )}
    >
      <span className={cn("flex w-5 shrink-0 items-center justify-center", !on && "opacity-35")}>{swatch}</span>
      <span className={cn("min-w-0 flex-1 truncate", !on && "line-through decoration-muted-foreground/60")}>{label}</span>
      <span className="font-mono text-[11px] tabular-nums text-muted-foreground">{count}</span>
      {on ? (
        <Eye aria-hidden className="size-3.5 text-muted-foreground/0 group-hover:text-muted-foreground group-focus-visible:text-muted-foreground" />
      ) : (
        <EyeOff aria-hidden className="size-3.5 text-muted-foreground" />
      )}
    </button>
  );
}

function Expander({ shown, total, open, onToggle }: { shown: number; total: number; open: boolean; onToggle: () => void }) {
  if (total <= shown) return null;
  return (
    <Button variant="ghost" size="sm" className="mt-0.5 h-7 w-full justify-start px-1.5 text-xs text-muted-foreground" onClick={onToggle}>
      {open ? "Show fewer" : `Show all ${total}`}
    </Button>
  );
}

function EntityKey() {
  const g = useMerged();
  const mode = useGraphUI((s) => s.colorMode);
  const filters = useGraphUI((s) => s.filters);
  const toggleType = useGraphUI((s) => s.toggleType);
  const caps = useCapabilities();
  const dark = useDark();
  const [all, setAll] = useState(false);
  const types = useMemo(() => typeCounts(g.nodes.values()), [g.nodes]);
  const byGrade = useMemo(() => {
    const m = new Map<string, number>();
    for (const n of g.nodes.values()) m.set(grade(n.grade).key, (m.get(grade(n.grade).key) ?? 0) + 1);
    return m;
  }, [g.nodes]);
  const heat = useRecallHeat(mode === "recalls");

  if (mode === "grade") {
    if (!caps.grades) {
      return <Hint>This source does not report grades, so nothing here can be coloured by one. That is not the same as a store where nothing is graded.</Hint>;
    }
    return (
      <ul className="space-y-1">
        {GRADE_ORDER.map((k) => {
          const s = GRADES[k];
          return (
            <li key={k} className="flex items-start gap-2 px-1.5 py-0.5 text-[13px]">
              <span className={cn("mt-1.5 size-2.5 shrink-0 rounded-full", s.dot)} />
              <span className="min-w-0 flex-1">
                <span className="block leading-snug">{s.label}</span>
                <span className="block text-xs leading-snug text-muted-foreground">{s.means}</span>
              </span>
              <span className="font-mono text-[11px] tabular-nums text-muted-foreground">{byGrade.get(k) ?? 0}</span>
            </li>
          );
        })}
      </ul>
    );
  }

  if (mode === "recalls") {
    if (heat.error) return <Hint tone="bad">{/recall log is off/i.test(heat.error) ? "The recall log is off on this brain. Start it with CORTEXDB_RECALL_LOG=1." : heat.error}</Hint>;
    if (!heat.data) return <Hint>Reading the recall log...</Hint>;
    const max = heat.data.max;
    const steps = max > 1 ? [max, Math.max(2, Math.round(Math.sqrt(max))), 1] : [1];
    return (
      <div className="space-y-1.5 px-1.5">
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          {[...new Set(steps)].map((c) => (
            <span key={c} className="inline-flex items-center gap-1.5">
              <span className="size-2.5 rounded-full" style={{ background: heatColor(c, max, dark) }} />
              {c}x
            </span>
          ))}
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-full" style={{ background: heatColor(0, max, dark) }} />
            never
          </span>
        </div>
        <Hint>{Object.keys(heat.data.nodes).length} nodes reached by recalls. Grey was never recalled.</Hint>
      </div>
    );
  }

  const rows = all ? types : types.slice(0, COLLAPSED_ROWS);
  return (
    <div>
      {rows.map((t) => (
        <Row
          key={t.key}
          on={!filters.hiddenTypes.has(t.key)}
          onClick={() => toggleType(t.key)}
          label={t.key}
          count={t.count}
          swatch={<span className="size-2.5 rounded-full" style={{ background: typeColor(t.key === "(untyped)" ? "" : t.key, dark) }} />}
        />
      ))}
      <Expander shown={COLLAPSED_ROWS} total={types.length} open={all} onToggle={() => setAll(!all)} />
    </div>
  );
}

function RelationKey() {
  const g = useMerged();
  const filters = useGraphUI((s) => s.filters);
  const togglePredicate = useGraphUI((s) => s.togglePredicate);
  const setCoOccurs = useGraphUI((s) => s.setCoOccurs);
  const mode = useGraphUI((s) => s.colorMode);
  const [all, setAll] = useState(false);
  const preds = useMemo(() => predicateCounts(g.edges.values()), [g.edges]);
  // co_occurs sits last whatever its count: it says least.
  const ordered = useMemo(() => [...preds.filter((p) => p.key !== CO_OCCURS), ...preds.filter((p) => p.key === CO_OCCURS)], [preds]);
  if (!ordered.length) return <Hint>No relations yet.</Hint>;
  const rows = all ? ordered : ordered.slice(0, COLLAPSED_ROWS);
  // Make sure co_occurs is reachable even when the list is folded.
  const co = ordered.find((p) => p.key === CO_OCCURS);
  const shown = co && !rows.includes(co) ? [...rows, co] : rows;
  return (
    <div>
      {shown.map((p) => {
        const isCo = p.key === CO_OCCURS;
        const on = predicateShown(filters, p.key);
        const color = mode === "grade" ? undefined : predicateColor(p.key);
        return (
          <Row
            key={p.key}
            on={on}
            onClick={() => (isCo ? setCoOccurs(!filters.showCoOccurs) : togglePredicate(p.key))}
            label={isCo ? "co_occurs" : p.key}
            count={p.count}
            title={isCo ? (on ? "Hide co_occurs" : "Show co_occurs: two names appeared together, nothing more") : undefined}
            swatch={
              <span className="flex w-5 items-center">
                <span className="h-0.5 flex-1 rounded-full" style={{ background: color ?? "var(--muted-foreground)" }} />
                <span className="-ml-px border-y-[3px] border-l-[5px] border-y-transparent" style={{ borderLeftColor: color ?? "var(--muted-foreground)" }} />
              </span>
            }
          />
        );
      })}
      {co && !filters.showCoOccurs && <p className="px-1.5 pt-1 text-[11px] leading-snug text-muted-foreground">co_occurs only says two names appeared together, so it is hidden.</p>}
      <Expander shown={COLLAPSED_ROWS} total={ordered.length} open={all} onToggle={() => setAll(!all)} />
    </div>
  );
}

export function LegendContent() {
  const mode = useGraphUI((s) => s.colorMode);
  const filters = useGraphUI((s) => s.filters);
  const showAll = useGraphUI((s) => s.showAll);
  const anyHidden = filters.hiddenTypes.size > 0 || filters.hiddenPredicates.size > 0;
  return (
    <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2.5 py-2.5">
      <SectionTitle aside={anyHidden && <button type="button" onClick={showAll} className="text-xs text-primary hover:underline">Show all</button>}>
        {mode === "grade" ? "How well established" : mode === "recalls" ? "Reached by recalls" : "Entities"}
      </SectionTitle>
      <EntityKey />
      <SectionTitle>Relations</SectionTitle>
      <RelationKey />
    </div>
  );
}

