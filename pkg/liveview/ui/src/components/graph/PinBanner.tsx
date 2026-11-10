import { History } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useLive } from "@/live";
import { GRAPH_TEXT } from "@/lib/graphStrings";
import { useLang } from "@/lib/i18n";
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
  const lang = useLang();
  const t = GRAPH_TEXT[lang];
  if (!pinned) return null;
  return (
    <div role="status" className="flex shrink-0 flex-wrap items-center justify-center gap-x-3 gap-y-1 border-b border-link-wait/30 bg-link-wait/15 px-3 py-1.5 text-sm text-foreground">
      <History aria-hidden className="size-4 text-link-wait" />
      <span>
        {t.pinnedBanner[0]}<b className="font-semibold tabular-nums">{pinnedAt ? formatClock(lang, pinnedAt) : t.aPastInstant}</b>{t.pinnedBanner[1]}
      </span>
      <Button size="sm" variant="outline" className="h-7" onClick={unpin}>{t.backToNow}</Button>
    </div>
  );
}
