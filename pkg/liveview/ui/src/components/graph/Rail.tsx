import { useCallback, useMemo, useState } from "react";
import { Radio } from "lucide-react";
import { buildRailItems, railSelection, railTargets, type RailFilter, type RailItem } from "@/lib/graph/rail";
import { useActivity, useCapabilities, useChanges, useLinkView } from "@/live";
import { useNow } from "@/lib/useNow";
import { GRAPH_TEXT } from "@/lib/graphStrings";
import { useText } from "@/lib/i18n";
import { LINK_TONES } from "@/lib/semantics";
import { cn } from "@/lib/utils";
import { selectNode } from "./actions";
import { useMerged } from "./hooks";
import { Hint, Segmented } from "./primitives";
import { RailRow } from "./RailRow";
import { useGraphUI } from "./state";

const SHOWN = 120;
const FRESH_MS = 3000;

export function RailContent() {
  const changes = useChanges();
  const activity = useActivity();
  const caps = useCapabilities();
  const g = useMerged();
  const set = useGraphUI((s) => s.set);
  const now = useNow(1000);
  const [filter, setFilter] = useState<RailFilter>("all");
  const t = useText(GRAPH_TEXT);

  const items = useMemo(() => buildRailItems(changes, activity, filter, SHOWN), [changes, activity, filter]);

  const hover = useCallback(
    (item: RailItem, on: boolean) => {
      if (!on) return set({ preview: null });
      set({ preview: railTargets(item, g.nodes, g.edges, g.adj) });
    },
    [g, set],
  );
  const select = useCallback(
    (item: RailItem) => {
      const t = railTargets(item, g.nodes, g.edges, g.adj);
      const id = railSelection(t, g.edges);
      if (id) selectNode(id);
    },
    [g],
  );

  return (
    <>
      <div className="flex items-center gap-2 px-3 pb-1.5 pt-2.5">
        <Segmented<RailFilter>
          size="sm"
          value={filter}
          onChange={setFilter}
          options={[
            { value: "all", label: t.all },
            { value: "changes", label: t.changes, title: t.changesTitle },
            { value: "calls", label: t.calls, title: t.callsTitle },
          ]}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-1.5 pb-2">
        {items.length === 0 ? (
          <div className="px-2 pt-3">
            <Hint>
              {!caps.changes && !caps.activity
                ? t.railNoSource
                : t.railEmpty}
            </Hint>
          </div>
        ) : (
          <ul>
            {items.map((item) => (
              <RailRow
                key={item.key}
                semantic={item.semantic}
                text={item.text}
                at={item.at}
                now={now}
                by={item.by}
                failed={item.failed}
                fresh={now - item.at < FRESH_MS}
                onHover={(on) => hover(item, on)}
                onSelect={() => select(item)}
              />
            ))}
          </ul>
        )}
      </div>
    </>
  );
}

/** The connection state as a small live dot for the rail's header. */
export function LiveDot({ className }: { className?: string }) {
  const link = useLinkView();
  const t = LINK_TONES[link.kind];
  return (
    <span className={cn("relative inline-flex size-2", className)} title={link.detail}>
      {t.pulse && <span className={cn("absolute inset-0 animate-ping rounded-full opacity-60", t.dot)} />}
      <span className={cn("relative size-2 rounded-full", t.dot)} />
    </span>
  );
}

export const RailIcon = Radio;
