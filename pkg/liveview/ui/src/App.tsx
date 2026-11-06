import { BrowserRouter, Route, Routes } from "react-router";
import { routerBasename } from "@/api";
import { AppShell } from "@/components/shell/AppShell";
import { ActivityPage } from "@/pages/ActivityPage";
import { ChangesPage } from "@/pages/ChangesPage";
import { EntityPage } from "@/pages/EntityPage";
import { GraphPage } from "@/pages/GraphPage";
import { LibraryPage } from "@/pages/LibraryPage";
import { NotFoundPage } from "@/pages/NotFoundPage";
import { OntologyHistoryPage } from "@/pages/OntologyHistoryPage";
import { OntologyPage } from "@/pages/OntologyPage";
import { RecallsPage } from "@/pages/RecallsPage";

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
          <Route path="recalls" element={<RecallsPage />} />
          <Route path="library" element={<LibraryPage />} />
          <Route path="ontology" element={<OntologyPage />} />
          <Route path="ontology-history" element={<OntologyHistoryPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
