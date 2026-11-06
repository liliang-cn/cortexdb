import { History } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useLive } from "@/live";
import { formatClock } from "@/lib/time";

/**
 * Said before anything else on the page: a graph quietly showing last week is
 * worse than one that cannot show it at all. It sits in the page's flow above
 * the canvas and has no way to be folded away while it is still true.
 */
export function PinBanner() {
  const pinned = useLive((s) => s.pinned);
  const pinnedAt = useLive((s) => s.pinnedAt);
  const unpin = useLive((s) => s.unpin);
  if (!pinned) return null;
  return (
    <div role="status" className="flex shrink-0 flex-wrap items-center justify-center gap-x-3 gap-y-1 border-b border-link-wait/30 bg-link-wait/15 px-3 py-1.5 text-sm text-foreground">
      <History aria-hidden className="size-4 text-link-wait" />
      <span>
        Showing the graph as it stood at <b className="font-semibold tabular-nums">{pinnedAt ? formatClock(pinnedAt) : "a past instant"}</b>. This is the past, and it is not updating.
      </span>
      <Button size="sm" variant="outline" className="h-7" onClick={unpin}>Back to now</Button>
    </div>
  );
}
