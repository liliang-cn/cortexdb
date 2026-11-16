// The ontology page: the declared model drawn, and its saved versions, as two
// tabs of one page. The tab and each tab's own parameters live in the URL (see
// lib/ontologyTabs). A host that leaves one of the two out gets the other
// alone, with no tabs.

import { useCallback, useMemo, useRef } from "react";
import { useSearchParams } from "react-router";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useLang } from "@/lib/i18n";
import { ONTOLOGY_TEXT } from "@/lib/ontologyStrings";
import { ontologyTabs, readTab, switchTab, type OntologyTab } from "@/lib/ontologyTabs";
import { OntologyPanel } from "@/pages/OntologyPanel";
import { VersionsPanel } from "@/pages/VersionsPanel";

export function OntologyPage() {
  const [params, setParams] = useSearchParams();
  const tabs = useMemo(() => ontologyTabs(), []);
  const tab = readTab(params, tabs);
  const t = ONTOLOGY_TEXT[useLang()];

  // The schema the ontology tab last drew, for the versions tab to open on.
  const schemaOnScreen = useRef("");
  const onVersionsSchema = useCallback((s: string) => {
    schemaOnScreen.current = s;
  }, []);

  if (tabs.length < 2) return tab === "versions" ? <VersionsPanel /> : <OntologyPanel />;

  const list = (
    <TabsList>
      <TabsTrigger value="ontology">{t.title}</TabsTrigger>
      <TabsTrigger value="versions">{t.versions}</TabsTrigger>
    </TabsList>
  );
  return (
    <Tabs
      value={tab}
      onValueChange={(v) => {
        const next: OntologyTab = v === "versions" ? "versions" : "ontology";
        // A tab is not a place Back should walk through.
        setParams((p) => switchTab(p, next, next === "versions" ? schemaOnScreen.current : ""), { replace: true });
      }}
      className="gap-0"
    >
      <TabsContent value="ontology">
        <OntologyPanel tabs={list} onVersionsSchema={onVersionsSchema} />
      </TabsContent>
      <TabsContent value="versions">
        <VersionsPanel tabs={list} />
      </TabsContent>
    </Tabs>
  );
}
