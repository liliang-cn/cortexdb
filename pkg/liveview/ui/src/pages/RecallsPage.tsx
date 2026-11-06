import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowUp, Flame, RefreshCw } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { recallsApi, type RecallHit, type RecallVerdict } from "@/api/recalls";
import { Empty, Page, PageHeader } from "@/components/Page";
import { GoldenExport } from "@/components/recalls/GoldenExport";
import { ReadFailure } from "@/components/recalls/Notices";
import { RecallDetail, type DetailFilter } from "@/components/recalls/RecallDetail";
import { RecallFilters } from "@/components/recalls/RecallFilters";
import { RecallRow } from "@/components/recalls/RecallRow";
import { RecordsView } from "@/components/recalls/RecordsView";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useLang, type Lang } from "@/lib/i18n";
import {
  filtersKey,
  hasFilters,
  heatPath,
  optionsFromParams,
  optionsToParams,
  verdictOf,
  withVerdict,
  withoutFilters,
  type RecallsOptions,
} from "@/lib/recalls";
import { RECALLS_STRINGS, type RecallsText } from "@/lib/recalls-strings";
import { useNow } from "@/lib/useNow";
import { STATS_LIMIT, useMediaQuery, useRecallDetail, useRecallStats, useRecallTimeline, type RecallDetailState } from "@/lib/useRecalls";
import { useLive } from "@/live";

/** The list and the detail sit side by side from here up; below it a recall opens in a sheet over the list. */
const WIDE = "(min-width: 1024px)";

function ListSkeleton({ label }: { label: string }) {
  return (
    <div className="divide-y rounded-xl border bg-card" aria-busy aria-label={label}>
      {Array.from({ length: 7 }, (_, i) => (
        <div key={i} className="space-y-2 px-4 py-3">
          <Skeleton className="h-4 w-4/5" />
          <Skeleton className="h-3 w-1/2" />
        </div>
      ))}
    </div>
  );
}

function DetailSkeleton() {
  return (
    <div className="space-y-4 p-5" aria-busy>
      <Skeleton className="h-5 w-3/4" />
      <Skeleton className="h-8 w-36" />
      <div className="space-y-2 pt-2">
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="h-3.5 w-full" />
        ))}
      </div>
    </div>
  );
}

interface DetailPaneProps {
  detail: RecallDetailState;
  t: RecallsText;
  lang: Lang;
  markError: string;
  onMark: (hit: RecallHit, verdict: RecallVerdict | "clear") => Promise<void>;
  onFilter: (f: DetailFilter) => void;
}

function DetailPane({ detail, t, lang, markError, onMark, onFilter }: DetailPaneProps) {
  if (detail.status === "idle") return <p className="px-6 py-12 text-center text-sm text-muted-foreground">{t.pick}</p>;
  if (detail.status === "loading" && !detail.entry) return <DetailSkeleton />;
  if (detail.status === "error" || !detail.entry) {
    const missing = /not found/i.test(detail.error?.message ?? "");
    return (
      <div className="p-4">
        {missing ? (
          <Empty title={t.detailMissing}>{t.detailMissingHint}</Empty>
        ) : (
          <ReadFailure error={detail.error ?? { message: "", status: 0 }} title={t.detailError} t={t} onRetry={detail.reload} />
        )}
      </div>
    );
  }
  return (
    <>
      {markError && (
        <p role="alert" className="mx-4 mt-4 rounded-lg border border-destructive/40 px-3 py-2 text-sm text-destructive sm:mx-5">
          {markError}
        </p>
      )}
      <RecallDetail entry={detail.entry} lang={lang} t={t} onMark={onMark} onFilter={onFilter} />
    </>
  );
}

/**
 * The recall log, read back: a timeline of what agents asked the brain, each
 * recall opening on why it returned what it did, with every hit markable as
 * useful or noise; and the same log per record. The marks download as
 * recall-eval golden cases.
 */
export function RecallsPage() {
  const lang = useLang();
  const t = RECALLS_STRINGS[lang];
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const opts = useMemo(() => optionsFromParams(params), [params]);
  const key = filtersKey(opts);
  const wide = useMediaQuery(WIDE);
  const source = useLive((s) => s.source);
  const now = useNow(15_000);

  // Every change goes back into the URL, so the page can be reloaded or
  // linked as it is. Filters replace the entry; opening a recall in the
  // phone's sheet adds one, so Back closes the sheet.
  const pushedRecall = useRef(false);
  const setOpts = useCallback(
    (next: RecallsOptions, push = false) => {
      setParams((current) => optionsToParams(next, current), { replace: !push });
    },
    [setParams],
  );
  useEffect(() => {
    if (!opts.recall) pushedRecall.current = false;
  }, [opts.recall]);

  // New recalls wait above the fold while the reader is scrolled down the list.
  const topRef = useRef<HTMLDivElement>(null);
  const [atTop, setAtTop] = useState(true);
  const timelineShown = opts.view === "timeline";
  const timeline = useRecallTimeline(opts, timelineShown, !atTop);
  const stats = useRecallStats(opts.view === "records");
  const logUnreadable = timeline.status === "error" && timeline.items.length === 0;
  // The marker exists only while the list does, so the watch follows it.
  const listShown = timelineShown && !logUnreadable;
  useEffect(() => {
    const el = topRef.current;
    if (!listShown || !el) {
      setAtTop(true);
      return;
    }
    const io = new IntersectionObserver(([e]) => setAtTop(e ? e.isIntersecting : true), { rootMargin: "0px 0px -20px 0px" });
    io.observe(el);
    return () => io.disconnect();
  }, [listShown]);

  // On a wide screen the newest recall opens by itself when nothing was
  // asked for, once per set of filters, so a recall arriving later never
  // takes the reader's place.
  const [auto, setAuto] = useState("");
  useEffect(() => setAuto(""), [key]);
  const first = timeline.items[0]?.id ?? "";
  useEffect(() => {
    if (wide && !opts.recall && !auto && timeline.status === "ready" && first) setAuto(first);
  }, [wide, opts.recall, auto, timeline.status, first]);
  const selected = opts.recall || (wide ? auto : "");
  const detail = useRecallDetail(timelineShown ? selected : "");

  const select = (id: string) => {
    const push = !wide && !opts.recall;
    if (push) pushedRecall.current = true;
    setOpts({ ...opts, recall: id }, push);
  };
  const closeSheet = () => {
    if (pushedRecall.current) {
      pushedRecall.current = false;
      navigate(-1);
    } else setOpts({ ...opts, recall: "" });
  };

  const [markError, setMarkError] = useState("");
  useEffect(() => setMarkError(""), [selected]);
  const { update: updateDetail } = detail;
  const { patch } = timeline;
  const recallId = detail.entry?.id;
  const onMark = useCallback(
    async (hit: RecallHit, verdict: RecallVerdict | "clear") => {
      if (!recallId) return;
      const before = verdictOf(hit);
      // Shown at once and taken back if the server refuses it.
      updateDetail((e) => withVerdict(e, hit.id, verdict));
      patch(recallId, (e) => withVerdict(e, hit.id, verdict));
      setMarkError("");
      try {
        await recallsApi.feedback({ recall_id: recallId, record_id: hit.id, verdict });
      } catch (err) {
        updateDetail((e) => withVerdict(e, hit.id, before));
        patch(recallId, (e) => withVerdict(e, hit.id, before));
        setMarkError(t.markFailed(err instanceof Error ? err.message : String(err)));
      }
    },
    [recallId, updateDetail, patch, t],
  );

  const onFilter = (f: DetailFilter) => {
    // On a phone the sheet closes, so the narrowed list is what the reader sees.
    setOpts({ ...opts, ...f, recall: wide ? opts.recall : "" });
  };

  const backToTop = () => {
    topRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    timeline.release();
  };

  let list;
  if (timeline.status === "loading" && timeline.items.length === 0) list = <ListSkeleton label={t.loading} />;
  else if (logUnreadable && timeline.error) list = <ReadFailure error={timeline.error} title={t.errorTitle} t={t} onRetry={timeline.reload} />;
  else if (timeline.items.length === 0) {
    list = hasFilters(opts) ? (
      <Empty title={t.noMatch}>
        {t.noMatchHint}{" "}
        <button type="button" className="text-primary underline underline-offset-4" onClick={() => setOpts(withoutFilters(opts))}>
          {t.clearAll}
        </button>
      </Empty>
    ) : (
      <Empty title={t.none}>{t.noneHint}</Empty>
    );
  } else {
    list = (
      <>
        <ol className="divide-y overflow-hidden rounded-xl border bg-card" aria-label={t.timeline}>
          {timeline.items.map((r) => (
            <li key={r.id}>
              <RecallRow recall={r} selected={r.id === selected} fresh={timeline.fresh.has(r.id)} now={now} lang={lang} t={t} onSelect={select} />
            </li>
          ))}
        </ol>
        <div className="flex flex-col items-center gap-2 py-4 text-sm text-muted-foreground">
          {timeline.hasMore ? (
            <Button variant="outline" size="sm" onClick={timeline.loadMore} disabled={timeline.loadingMore}>
              {timeline.loadingMore ? t.loadingOlder : t.older}
            </Button>
          ) : (
            <p>{t.oldest}</p>
          )}
          {timeline.moreError && <p className="text-destructive">{timeline.moreError}</p>}
        </div>
      </>
    );
  }

  const pending = timeline.pending.length;
  const detailPane = <DetailPane detail={detail} t={t} lang={lang} markError={markError} onMark={onMark} onFilter={onFilter} />;

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
          <div className="flex flex-wrap items-center gap-2">
            <GoldenExport source={source} t={t} />
            <Button asChild variant="outline" size="sm">
              <Link to={heatPath}>
                <Flame aria-hidden />
                {t.heat}
              </Link>
            </Button>
          </div>
        }
      />
      <Tabs value={opts.view} onValueChange={(v) => setOpts({ ...opts, view: v === "records" ? "records" : "timeline" })}>
        <TabsList>
          <TabsTrigger value="timeline">{t.timeline}</TabsTrigger>
          <TabsTrigger value="records">{t.records}</TabsTrigger>
        </TabsList>

        <TabsContent value="timeline" className="mt-3">
          {logUnreadable ? (
            list
          ) : (
            <>
              <div className="mb-4">
                <RecallFilters opts={opts} t={t} onChange={(next) => setOpts(next)} />
              </div>
              {timeline.rebuilt && (
                <p className="mb-4 flex items-center gap-2 rounded-lg border border-dashed px-3 py-2 text-sm text-muted-foreground">
                  <RefreshCw aria-hidden className="size-4 shrink-0" />
                  {t.rebuilt}
                  <button type="button" className="ml-auto shrink-0 text-foreground underline underline-offset-4" onClick={timeline.dismissRebuilt}>
                    {t.dismiss}
                  </button>
                </p>
              )}
              <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)] lg:items-start">
                <div className="min-w-0">
                  <div ref={topRef} aria-hidden className="h-px" />
                  {pending > 0 && (
                    <div className="sticky top-3 z-20 mb-3 flex justify-center">
                      <Button size="sm" className="rounded-full shadow-md" onClick={backToTop}>
                        <ArrowUp aria-hidden />
                        {t.newRecalls(pending)}
                      </Button>
                    </div>
                  )}
                  {list}
                </div>
                {wide && (
                  <aside aria-label={t.detailLabel} className="sticky top-4 max-h-[calc(100dvh-5.5rem)] min-w-0 overflow-auto rounded-xl border bg-card">
                    {detailPane}
                  </aside>
                )}
              </div>
              <p className="sr-only" aria-live="polite">
                {pending > 0 ? t.newRecalls(pending) : ""}
              </p>
            </>
          )}
        </TabsContent>

        <TabsContent value="records" className="mt-3">
          {stats.status === "loading" ? (
            <ListSkeleton label={t.loading} />
          ) : stats.status === "error" || !stats.stats ? (
            <ReadFailure error={stats.error ?? { message: "", status: 0 }} title={t.statsError} t={t} onRetry={stats.reload} />
          ) : (
            <RecordsView
              stats={stats.stats}
              limit={STATS_LIMIT}
              now={now}
              lang={lang}
              t={t}
              onRecord={(id) => setOpts({ ...withoutFilters(opts), view: "timeline", record: id, recall: "" })}
            />
          )}
        </TabsContent>
      </Tabs>

      {!wide && (
        <Sheet open={timelineShown && Boolean(opts.recall)} onOpenChange={(open) => !open && closeSheet()}>
          <SheetContent side="right" className="w-full gap-0 overflow-y-auto p-0 sm:max-w-xl">
            <SheetHeader className="sr-only">
              <SheetTitle>{detail.entry?.query ?? t.title}</SheetTitle>
              <SheetDescription>{t.pick}</SheetDescription>
            </SheetHeader>
            <div className="pt-8">{detailPane}</div>
          </SheetContent>
        </Sheet>
      )}
    </Page>
  );
}
