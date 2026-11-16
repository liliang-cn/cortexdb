// The graph page: the graph, and the same brain as a library, as two views of
// one page. The view lives in the URL (see lib/graphViews). A host that leaves
// the library out gets the graph alone, with no switch.

import { lazy, Suspense, useMemo, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { useSearchParams } from "react-router";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useText } from "@/lib/i18n";
import { GRAPH_TEXT } from "@/lib/graphStrings";
import { graphViews, readView, switchView } from "@/lib/graphViews";

// three.js is most of the app, so each view loads on its own and the other
// pages never pay for it; the library's room loads only once it is opened.
// Switching views unmounts the one left, which disposes its scene.
const GraphView = lazy(() => import("@/components/graph/GraphView"));
const LibraryView = lazy(() => import("@/components/library/LibraryView"));

export function GraphPage() {
  const t = useText(GRAPH_TEXT);
  const [params, setParams] = useSearchParams();
  const views = useMemo(() => graphViews(), []);
  const view = readView(params, views);

  const loading = (
    <div className="flex h-full items-center justify-center gap-2 text-sm text-muted-foreground">
      <Loader2 className="size-4 animate-spin" />
      {view === "library" ? t.loadingLibrary : t.loadingGraph}
    </div>
  );
  const body = (switcher?: ReactNode) => (
    <Suspense fallback={loading}>{view === "library" ? <LibraryView switcher={switcher} /> : <GraphView switcher={switcher} />}</Suspense>
  );

  if (views.length < 2) return body();

  const switcher = (
    <TabsList aria-label={t.view} className="pointer-events-auto h-8 border bg-card/92 shadow-sm backdrop-blur-md">
      <TabsTrigger value="graph" className="px-3 text-xs">{t.modeGraph}</TabsTrigger>
      <TabsTrigger value="library" className="px-3 text-xs">{t.modeLibrary}</TabsTrigger>
    </TabsList>
  );
  return (
    <Tabs
      value={view}
      // A view is not a place Back should walk through.
      onValueChange={(v) => setParams((p) => switchView(p, v === "library" ? "library" : "graph"), { replace: true })}
      className="h-full gap-0"
    >
      {body(switcher)}
    </Tabs>
  );
}
