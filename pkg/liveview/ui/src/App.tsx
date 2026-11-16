import { lazy, Suspense, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router";
import { routerBasename } from "@/api";
import { AppShell } from "@/components/shell/AppShell";
import { ActivityPage } from "@/pages/ActivityPage";
import { ChangesPage } from "@/pages/ChangesPage";
import { EntityPage } from "@/pages/EntityPage";
import { GraphPage } from "@/pages/GraphPage";
import { LibraryPage } from "@/pages/LibraryPage";
import { NotFoundPage } from "@/pages/NotFoundPage";
import { useText } from "@/lib/i18n";
import { routeShown } from "@/lib/pages";
import { legacyVersionsSearch } from "@/lib/ontologyTabs";
import { SHELL_TEXT } from "@/lib/shellStrings";

// The recalls, ontology (with its versions tab) and analysis pages are each a few hundred lines that
// most visits never open, so they load when they are first visited rather
// than with every page. (The graph and the library lazy-load their own scenes,
// and three with them.)
const RecallsPage = lazy(() => import("@/pages/RecallsPage").then((m) => ({ default: m.RecallsPage })));
const OntologyPage = lazy(() => import("@/pages/OntologyPage").then((m) => ({ default: m.OntologyPage })));

const AnalysisPage = lazy(() => import("@/pages/AnalysisPage").then((m) => ({ default: m.AnalysisPage })));

function Loading({ children }: { children: ReactNode }) {
  const t = useText(SHELL_TEXT);
  return (
    <Suspense
      fallback={
        <div className="flex h-full min-h-40 items-center justify-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          {t.loading}
        </div>
      }
    >
      {children}
    </Suspense>
  );
}

/** /ontology-history was the versions page; it is the ontology page's versions tab now. */
function ToVersionsTab() {
  const { search, hash } = useLocation();
  return <Navigate replace to={{ pathname: "/ontology", search: legacyVersionsSearch(search), hash }} />;
}

export function App() {
  // The page may be mounted under a prefix of another mux; the server writes it
  // into <base href>, and the router follows it. A page the host left out has
  // no route, so a link that still reaches it lands on the not-found page.
  const shown = (path: string) => routeShown(path);
  return (
    <BrowserRouter basename={routerBasename()}>
      <Routes>
        <Route element={<AppShell />}>
          <Route index element={<GraphPage />} />
          {shown("/changes") && <Route path="changes" element={<ChangesPage />} />}
          <Route path="entity" element={<EntityPage />} />
          {shown("/activity") && <Route path="activity" element={<ActivityPage />} />}
          {shown("/recalls") && <Route path="recalls" element={<Loading><RecallsPage /></Loading>} />}
          {shown("/library") && <Route path="library" element={<LibraryPage />} />}
          {shown("/ontology") && <Route path="ontology" element={<Loading><OntologyPage /></Loading>} />}
          {shown("/ontology-history") && <Route path="ontology-history" element={<ToVersionsTab />} />}
          {shown("/analysis") && <Route path="analysis" element={<Loading><AnalysisPage /></Loading>} />}
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
