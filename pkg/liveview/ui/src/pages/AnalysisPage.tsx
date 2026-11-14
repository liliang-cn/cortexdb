// The analysis page: the brain in numbers. An overview of charts that needs
// no configuration, and a builder over aggregate_metadata. Which view, which
// range and the whole of a builder question live in the address, so what is on
// screen is a link.

import { useCallback, useMemo } from "react";
import { RefreshCw } from "lucide-react";
import { useSearchParams } from "react-router";
import { Empty, Page, PageHeader } from "@/components/Page";
import { Explore } from "@/components/analysis/Explore";
import { Overview, OverviewSkeleton, RangeControl } from "@/components/analysis/Overview";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { readState, writeState, type AnalysisState } from "@/lib/analysis";
import { ANALYSIS_TEXT } from "@/lib/analysisStrings";
import { useLang } from "@/lib/i18n";
import { useAnswer, useFields, useOverview } from "@/lib/useAnalysis";
import { cn } from "@/lib/utils";
import { useLive } from "@/live";

export function AnalysisPage() {
  const lang = useLang();
  const t = ANALYSIS_TEXT[lang];
  const [params, setParams] = useSearchParams();
  const state = useMemo(() => readState(params), [params]);
  const source = useLive((s) => s.source);

  const update = useCallback(
    (patch: Partial<AnalysisState>) => setParams((current) => writeState({ ...readState(current), ...patch }, current), { replace: true }),
    [setParams],
  );
  const setQuery = useCallback(
    (patch: Partial<AnalysisState["query"]>) =>
      setParams(
        (current) => {
          const now = readState(current);
          return writeState({ ...now, query: { ...now.query, ...patch } }, current);
        },
        { replace: true },
      ),
    [setParams],
  );

  // The overview is read on either tab: it is what says whether this source
  // can be asked at all. The builder's reads wait until its tab is open.
  const overview = useOverview(state.range, true);
  const exploring = state.view === "explore";
  const usable = overview.data?.explore ?? false;
  const fields = useFields(state.query.collection, exploring && usable);
  const answer = useAnswer(state.query, exploring && usable);

  const o = overview.data;
  let body;
  if (!o && overview.error) {
    body = (
      <Empty title={t.readFailed}>
        <span className="[overflow-wrap:anywhere]">{overview.error}</span>{" "}
        <button type="button" className="text-primary underline underline-offset-4" onClick={overview.reload}>
          {t.retry}
        </button>
      </Empty>
    );
  } else if (!o) body = <OverviewSkeleton label={t.loading} />;
  else if (!o.available) body = <Empty title={t.unavailableTitle}>{o.reason}</Empty>;
  else {
    body = (
      <Tabs value={state.view} onValueChange={(v) => update({ view: v === "explore" ? "explore" : "overview" })}>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <TabsList>
            <TabsTrigger value="overview">{t.overview}</TabsTrigger>
            <TabsTrigger value="explore">{t.explore}</TabsTrigger>
          </TabsList>
          {!exploring && <RangeControl value={state.range} onChange={(range) => update({ range })} t={t} />}
        </div>
        <TabsContent value="overview" className="mt-3 space-y-3">
          <p className="text-xs text-muted-foreground">{t.bucketedBy(t.per[o.unit], o.timezone)}</p>
          <div className={cn("transition-opacity", overview.loading && o.days !== state.range && "opacity-60")}>
            <Overview overview={o} t={t} lang={lang} />
          </div>
        </TabsContent>
        <TabsContent value="explore" className="mt-4">
          <Explore q={state.query} set={setQuery} fields={fields} result={answer} usable={usable} t={t} lang={lang} />
        </TabsContent>
      </Tabs>
    );
  }

  return (
    <Page className="max-w-7xl">
      <PageHeader
        title={t.title}
        description={
          <>
            {t.description}
            {source && (
              <span className="mt-1 block truncate font-mono text-xs" title={source}>
                {t.source}: {source}
              </span>
            )}
          </>
        }
        actions={
          <Button variant="outline" size="sm" onClick={exploring ? answer.reload : overview.reload} disabled={exploring ? answer.loading : overview.loading}>
            <RefreshCw aria-hidden className={cn((exploring ? answer.loading : overview.loading) && "animate-spin")} />
            {t.refresh}
          </Button>
        }
      />
      {body}
    </Page>
  );
}
