import { lazy, Suspense, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { BrowserRouter, Route, Routes } from "react-router";
import { routerBasename } from "@/api";
import { AppShell } from "@/components/shell/AppShell";
import { ActivityPage } from "@/pages/ActivityPage";
import { ChangesPage } from "@/pages/ChangesPage";
import { EntityPage } from "@/pages/EntityPage";
import { GraphPage } from "@/pages/GraphPage";
import { LibraryPage } from "@/pages/LibraryPage";
import { NotFoundPage } from "@/pages/NotFoundPage";

// The recalls, ontology and versions pages are each a few hundred lines that
// most visits never open, so they load when they are first visited rather
// than with every page. (The graph and the library lazy-load their own scenes,
// and three with them.)
const RecallsPage = lazy(() => import("@/pages/RecallsPage").then((m) => ({ default: m.RecallsPage })));
const OntologyPage = lazy(() => import("@/pages/OntologyPage").then((m) => ({ default: m.OntologyPage })));
const OntologyHistoryPage = lazy(() =>
  import("@/pages/OntologyHistoryPage").then((m) => ({ default: m.OntologyHistoryPage })),
);

function Loading({ children }: { children: ReactNode }) {
  return (
    <Suspense
      fallback={
        <div className="flex h-full min-h-40 items-center justify-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          Loading
        </div>
      }
    >
      {children}
    </Suspense>
  );
}

export function App() {
  // The page may be mounted under a prefix of another mux; the server writes it
  // into <base href>, and the router follows it.
  return (
    <BrowserRouter basename={routerBasename()}>
      <Routes>
        <Route element={<AppShell />}>
          <Route index element={<GraphPage />} />
          <Route path="changes" element={<ChangesPage />} />
          <Route path="entity" element={<EntityPage />} />
          <Route path="activity" element={<ActivityPage />} />
          <Route path="recalls" element={<Loading><RecallsPage /></Loading>} />
          <Route path="library" element={<LibraryPage />} />
          <Route path="ontology" element={<Loading><OntologyPage /></Loading>} />
          <Route path="ontology-history" element={<Loading><OntologyHistoryPage /></Loading>} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
