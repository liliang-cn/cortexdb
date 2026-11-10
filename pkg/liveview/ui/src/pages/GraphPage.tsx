import { lazy, Suspense } from "react";
import { Loader2 } from "lucide-react";
import { useText } from "@/lib/i18n";
import { GRAPH_TEXT } from "@/lib/graphStrings";

// three.js is most of the app, so the graph loads on its own and the other
// pages never pay for it.
const GraphView = lazy(() => import("@/components/graph/GraphView"));

export function GraphPage() {
  const t = useText(GRAPH_TEXT);
  return (
    <Suspense
      fallback={
        <div className="flex h-full items-center justify-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />{t.loadingGraph}
        </div>
      }
    >
      <GraphView />
    </Suspense>
  );
}
