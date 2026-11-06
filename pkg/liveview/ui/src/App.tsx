import { BrowserRouter, Route, Routes } from "react-router";
import { routerBasename } from "@/api";
import { AppShell } from "@/components/shell/AppShell";
import { ActivityPage } from "@/pages/ActivityPage";
import { ChangesPage } from "@/pages/ChangesPage";
import { EntityPage } from "@/pages/EntityPage";
import { GraphPage } from "@/pages/GraphPage";
import { NotFoundPage } from "@/pages/NotFoundPage";

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
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
