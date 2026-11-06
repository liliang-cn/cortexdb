import { useEffect, useRef } from "react";
import { predicateColor } from "@/lib/graph/colors";
import { useDark, useMerged } from "./hooks";
import { useGraphUI } from "./state";
import { typeColor } from "@/lib/graph/colors";

/** What is under the pointer: a node's name and type, or a relation as source, predicate, target. Follows the pointer without re-rendering. */
export function HoverTip({ stage }: { stage: React.RefObject<HTMLElement | null> }) {
  const hovered = useGraphUI((s) => s.hovered);
  const hoverEdge = useGraphUI((s) => s.hoverEdge);
  const g = useMerged();
  const dark = useDark();
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = stage.current;
    if (!el) return;
    const move = (e: PointerEvent) => {
      const tip = ref.current;
      if (!tip) return;
      const r = el.getBoundingClientRect();
      const x = Math.min(e.clientX - r.left + 14, r.width - tip.offsetWidth - 8);
      const y = Math.min(e.clientY - r.top + 16, r.height - tip.offsetHeight - 8);
      tip.style.transform = `translate(${Math.max(8, x)}px, ${Math.max(8, y)}px)`;
    };
    el.addEventListener("pointermove", move);
    return () => el.removeEventListener("pointermove", move);
  }, [stage]);

  const edge = hoverEdge ? g.edges.get(hoverEdge) : undefined;
  const node = !edge && hovered ? g.nodes.get(hovered) : undefined;
  if (!edge && !node) return null;
  const name = (id: string) => g.nodes.get(id)?.label ?? id;
  return (
    <div ref={ref} className="pointer-events-none absolute left-0 top-0 z-20 max-w-80 rounded-lg border bg-popover px-2.5 py-1.5 text-[13px] text-popover-foreground shadow-md">
      {edge ? (
        <span className="flex flex-wrap items-center gap-x-1.5">
          <span className="font-medium">{name(edge.source)}</span>
          <span className="inline-flex items-center gap-1 font-mono text-xs" style={{ color: `color-mix(in oklab, ${predicateColor(edge.label)} 60%, var(--foreground))` }}>
            <span aria-hidden className="h-0.5 w-3 rounded-full" style={{ background: predicateColor(edge.label) }} />
            {edge.label || "related"}
            <span aria-hidden>→</span>
          </span>
          <span className="font-medium">{name(edge.target)}</span>
        </span>
      ) : node ? (
        <span className="flex items-center gap-2">
          <span className="size-2.5 rounded-full" style={{ background: typeColor(node.type, dark) }} />
          <span className="font-medium">{node.label}</span>
          {node.type && <span className="text-xs text-muted-foreground">{node.type}</span>}
        </span>
      ) : null}
    </div>
  );
}
