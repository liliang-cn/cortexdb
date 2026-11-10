import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowUp, GitCommitHorizontal, Pause, Play } from "lucide-react";
import { useSearchParams } from "react-router";
import { ChangeFilterBar } from "@/components/feed/ChangeFilterBar";
import { ChangeRow } from "@/components/feed/ChangeRow";
import { Empty, Page, PageHeader } from "@/components/Page";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { filtersFromParams, filtersKey, filtersToParams, groupByDay, hasFilters, mergeFeed, noFilters, producersSeen, type ChangeFilters } from "@/lib/changes";
import { FEED_TEXT } from "@/lib/feedStrings";
import { useLang, useText } from "@/lib/i18n";
import { useChangeFeed } from "@/lib/useChangeFeed";
import { useNow } from "@/lib/useNow";
import { useCapabilities, useChanges, useLive } from "@/live";

function ListSkeleton() {
  const t = useText(FEED_TEXT);
  return (
    <div className="divide-y rounded-xl border bg-card" aria-busy aria-label={t.loadingChanges}>
      {Array.from({ length: 7 }, (_, i) => (
        <div key={i} className="flex items-start gap-3 px-4 py-3">
          <Skeleton className="h-5 w-24 rounded-full" />
          <div className="flex-1 space-y-2">
            <Skeleton className="h-4 w-3/5" />
            <Skeleton className="h-3 w-2/5" />
          </div>
          <Skeleton className="h-3 w-12" />
        </div>
      ))}
    </div>
  );
}

/** Git log for the brain: every write as a readable line, live at the top, older ones on demand. */
export function ChangesPage() {
  const [params, setParams] = useSearchParams();
  const filters = useMemo(() => filtersFromParams(params), [params]);
  const key = filtersKey(filters);
  const setFilters = (f: ChangeFilters) => setParams(filtersToParams(f, params), { replace: true });

  const caps = useCapabilities();
  const ready = useLive((s) => s.ready);
  const resets = useLive((s) => s.resets);
  const live = useChanges();
  const now = useNow(5000);
  const lang = useLang();
  const t = FEED_TEXT[lang];

  const feed = useChangeFeed(filters, key);
  const { reload } = feed;

  // The cursor fell behind retention: what the list held is stale, so read it again.
  const resetsAtMount = useRef(resets);
  const [rebuilt, setRebuilt] = useState(false);
  useEffect(() => {
    if (resets === resetsAtMount.current) return;
    resetsAtMount.current = resets;
    setRebuilt(true);
    reload();
  }, [resets, reload]);

  const all = useMemo(() => mergeFeed(feed.items, live, filters, feed.hasMore), [feed.items, live, filters, feed.hasMore]);

  // Rows newer than this arrived while the page was open and flash once.
  const baseline = useRef(Number.MAX_SAFE_INTEGER);
  useEffect(() => {
    if (feed.status === "ready") baseline.current = Math.max(feed.head, live[0]?.seq ?? 0);
    // Only a new first page moves the baseline, never the live tail itself.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [feed.status, feed.head, key]);

  // Pausing, or being scrolled away from the top, holds the newest row where it is.
  const topRef = useRef<HTMLDivElement>(null);
  const [atTop, setAtTop] = useState(true);
  const [paused, setPaused] = useState(false);
  const [frozen, setFrozen] = useState<number | null>(null);
  useEffect(() => {
    const el = topRef.current;
    if (!el) return;
    const io = new IntersectionObserver(([e]) => setAtTop(e ? e.isIntersecting : true), { rootMargin: "0px 0px -20px 0px" });
    io.observe(el);
    return () => io.disconnect();
  }, []);
  const held = paused || !atTop;
  const newest = all[0]?.seq ?? 0;
  useEffect(() => {
    if (!held) setFrozen(null);
    else setFrozen((f) => f ?? newest);
  }, [held, newest]);

  const visible = useMemo(() => (frozen === null ? all : all.filter((c) => c.seq <= frozen)), [all, frozen]);
  const waiting = all.length - visible.length;
  const groups = useMemo(() => groupByDay(visible, now, lang), [visible, now, lang]);

  const backToTop = () => {
    topRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    if (paused) setPaused(false);
  };

  // Older pages load when the list's end comes into view.
  const endRef = useRef<HTMLDivElement>(null);
  const { loadMore, hasMore } = feed;
  useEffect(() => {
    const el = endRef.current;
    if (!el || !hasMore) return;
    const io = new IntersectionObserver(([e]) => e?.isIntersecting && loadMore(), { rootMargin: "400px" });
    io.observe(el);
    return () => io.disconnect();
  }, [hasMore, loadMore, visible.length]);

  const producers = useMemo(() => producersSeen([...feed.items, ...live], filters.producer), [feed.items, live, filters.producer]);

  const noFeed = feed.status === "nofeed" || (feed.status === "ready" && ready && !caps.changes && feed.head === 0 && all.length === 0 && !hasFilters(filters));

  let body;
  if (feed.status === "loading" && all.length === 0) body = <ListSkeleton />;
  else if (noFeed) {
    body = (
      <Empty title={t.noFeedTitle}>{t.noFeedBody}</Empty>
    );
  } else if (feed.status === "error" && all.length === 0) {
    body = (
      <Empty title={t.readErrorTitle}>
        {feed.error}{" "}
        <button type="button" className="text-primary underline underline-offset-4" onClick={feed.reload}>
          {t.tryAgain}
        </button>
      </Empty>
    );
  } else if (all.length === 0) {
    body = hasFilters(filters) ? (
      <Empty title={t.noMatchTitle}>
        {t.noMatchLead}{" "}
        <button type="button" className="text-primary underline underline-offset-4" onClick={() => setFilters(noFilters)}>
          {t.clearThemAll}
        </button>
        {t.noMatchTail}
      </Empty>
    ) : (
      <Empty title={t.noChangesTitle}>{t.noChangesBody}</Empty>
    );
  } else {
    body = (
      <div className="space-y-6">
        {groups.map((g) => (
          <section key={g.key} aria-label={g.label}>
            <h2 className="sticky top-0 z-10 -mx-1 mb-2 bg-background/95 px-1 py-1.5 text-sm font-semibold backdrop-blur">
              {g.label} <span className="ml-1 font-normal text-muted-foreground">{g.items.length}</span>
            </h2>
            <ol className="divide-y overflow-hidden rounded-xl border bg-card">
              {g.items.map((c) => (
                <li key={c.seq}>
                  <ChangeRow item={c} now={now} fresh={c.seq > baseline.current} />
                </li>
              ))}
            </ol>
          </section>
        ))}
        <div ref={endRef} className="flex flex-col items-center gap-2 pb-8 text-sm text-muted-foreground">
          {feed.hasMore ? (
            <Button variant="outline" size="sm" onClick={feed.loadMore} disabled={feed.loadingMore}>
              {feed.loadingMore ? t.loadingOlder : t.loadOlder}
            </Button>
          ) : (
            <p>{t.oldest}</p>
          )}
          {feed.error && feed.status === "ready" && <p className="text-destructive">{feed.error}</p>}
        </div>
      </div>
    );
  }

  return (
    <Page>
      <PageHeader
        title={t.changesTitle}
        description={t.changesDescription}
        actions={
          <Button variant="outline" size="sm" onClick={() => setPaused((p) => !p)} aria-pressed={paused}>
            {paused ? <Play aria-hidden /> : <Pause aria-hidden />}
            {paused ? t.resumeLive : t.pauseLive}
          </Button>
        }
      />
      <div className="mb-5">
        <ChangeFilterBar filters={filters} producers={producers} onChange={setFilters} />
      </div>
      {rebuilt && (
        <p className="mb-4 flex items-center gap-2 rounded-lg border border-dashed px-3 py-2 text-sm text-muted-foreground">
          <GitCommitHorizontal aria-hidden className="size-4 shrink-0" />
          {t.rebuilt}
          <button type="button" className="ml-auto shrink-0 text-foreground underline underline-offset-4" onClick={() => setRebuilt(false)}>
            {t.dismiss}
          </button>
        </p>
      )}
      <div ref={topRef} aria-hidden className="h-px" />
      {(waiting > 0 || paused) && (
        <div className="sticky top-3 z-20 mb-3 flex justify-center">
          <Button size="sm" className="rounded-full shadow-md" onClick={backToTop}>
            <ArrowUp aria-hidden />
            {paused ? (waiting > 0 ? t.pausedNew(waiting) : t.pausedResume) : t.newChanges(waiting)}
          </Button>
        </div>
      )}
      {body}
      <p className="sr-only" aria-live="polite">{waiting > 0 ? t.newChanges(waiting) : ""}</p>
    </Page>
  );
}
