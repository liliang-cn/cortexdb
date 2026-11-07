import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ArrowRight, ArrowUp, Shapes, X } from "lucide-react";
import { Link, useSearchParams } from "react-router";
import { Empty, Page, PageHeader } from "@/components/Page";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { ComparePanel, CompareSummary } from "@/components/versions/ComparePanel";
import { captureAnchor, isWide, restoreAnchor, useWide, type ScrollAnchor } from "@/components/versions/layout";
import { SchemaPicker } from "@/components/versions/SchemaPicker";
import { VersionRow } from "@/components/versions/VersionRow";
import {
  arrivals,
  defaultSchema,
  findVersion,
  newestVersion,
  pairOf,
  pickRole,
  picksFromParams,
  picksKey,
  picksOfPair,
  picksToParams,
  picksWithin,
  previousPair,
  renamedTo,
  samePickSet,
  schemaOptions,
  settleEcho,
  schemaToParams,
  togglePick,
  type Pair,
  type VersionList,
} from "@/lib/ontologyHistory";
import { useSchemaIndex, useVersionDiff, useVersionHistory } from "@/lib/ontologyHistoryHooks";
import { useNow } from "@/lib/useNow";
import { useCapabilities, useLive } from "@/live";
import { pageShown } from "@/lib/pages";

function ListSkeleton() {
  return (
    <div className="divide-y rounded-xl border bg-card" aria-busy aria-label="Loading versions">
      {Array.from({ length: 6 }, (_, i) => (
        <div key={i} className="flex items-start gap-3 px-4 py-3">
          <Skeleton className="h-7 w-16 rounded-md" />
          <div className="flex-1 space-y-2">
            <Skeleton className="h-4 w-1/4" />
            <Skeleton className="h-3 w-2/5" />
            <Skeleton className="h-5 w-3/5 rounded-full" />
          </div>
          <Skeleton className="h-3 w-12" />
        </div>
      ))}
    </div>
  );
}

function TryAgain({ onClick }: { onClick: () => void }) {
  return (
    <button type="button" className="text-primary underline underline-offset-4" onClick={onClick}>
      Try again
    </button>
  );
}

/** One line on where the schema stands: how many versions, which is latest, and whether it validates writes. */
function Standing({ list }: { list: VersionList }) {
  const count = `${list.total} ${list.total === 1 ? "version" : "versions"}`;
  const name = list.versions[0]?.name;
  const named = name && name !== list.schema ? <span className="font-medium text-foreground">{name}. </span> : null;
  if (!list.exists) {
    return (
      <>
        {named}This schema was deleted. Its {count} {list.total === 1 ? "is" : "are"} kept.
      </>
    );
  }
  // Only the current version can be active, and it is always on the first page.
  const active = list.versions.some((v) => v.active && v.version === list.currentVersion);
  return (
    <>
      {named}
      {count}. <span className="font-medium text-foreground">v{list.currentVersion}</span> is the latest
      {active ? " and the active validator." : "; the schema does not validate writes now."}
    </>
  );
}

/**
 * Every saved version of a schema: when and by whom it was saved, what it
 * changed, and when it was the active validator. Pick two and their diff is
 * shown, and the pair goes into the URL (?from=&to=) so a diff can be linked.
 * A new version saved while the page is open is put in at the top and flashes
 * once. The page holds the picks itself and joins the re-read list in place
 * rather than redrawing it, so neither the choice nor the scroll position is
 * lost to the arrival.
 */
export function OntologyHistoryPage() {
  const [params, setParams] = useSearchParams();
  const urlSchema = (params.get("schema") ?? "").trim();
  const index = useSchemaIndex();
  const schema = urlSchema || (index.index ? defaultSchema(index.index.schemas) : "");
  const options = useMemo(() => schemaOptions(index.index?.schemas ?? [], schema), [index.index, schema]);
  // The default is the active schema, and activating another one is exactly
  // what this page watches happen: left unpinned, the page would switch
  // schemas under the reader. So the first default is written into the URL,
  // next to whatever picks a link brought.
  useEffect(() => {
    if (urlSchema || !schema) return;
    setParams(
      (p) => {
        const n = new URLSearchParams(p);
        n.set("schema", schema);
        return n;
      },
      { replace: true },
    );
  }, [urlSchema, schema, setParams]);
  // The diagram draws a saved schema; a deleted one keeps its versions here and has nothing to draw there.
  const saved = index.index?.schemas.some((s) => s.schema_id === schema) ?? false;

  const now = useNow(5000);
  const wide = useWide();
  const caps = useCapabilities();
  const ready = useLive((s) => s.ready);

  // ---- the picks: the page holds them, the URL is told (see settleEcho) ---
  const urlPicks = useMemo(() => picksFromParams(params), [params]);
  const [picks, setPickState] = useState<number[]>(urlPicks);
  const written = useRef<string[]>([]);
  const urlKey = picksKey(urlPicks);
  useEffect(() => {
    const settled = settleEcho(written.current, urlKey);
    written.current = settled.written;
    if (!settled.echo) setPickState((cur) => (samePickSet(cur, urlPicks) ? cur : urlPicks));
    // urlPicks is read through urlKey, which names it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlKey]);
  const picksRef = useRef(picks);
  picksRef.current = picks;
  const pair = pairOf(picks);

  // Picking writes with replace: it is not a place Back should walk through.
  const setPicks = useCallback(
    (next: number[]) => {
      picksRef.current = next;
      setPickState(next);
      written.current = [...written.current, picksKey(next)];
      setParams(
        (p) => {
          const n = picksToParams(next, p);
          if (schema && next.length > 0) n.set("schema", schema);
          return n;
        },
        { replace: true },
      );
    },
    [setParams, schema],
  );

  const [sheetOpen, setSheetOpen] = useState(() => pairOf(urlPicks) !== null && !isWide());
  const showPair = useCallback(
    (p: Pair) => {
      setPicks(picksOfPair(p));
      // An explicit "show me this diff": on a phone, open it.
      if (!isWide()) setSheetOpen(true);
    },
    [setPicks],
  );
  const onToggle = useCallback((v: number) => setPicks(togglePick(picksRef.current, v)), [setPicks]);
  const onPrevious = useCallback(
    (v: number) => {
      const p = previousPair(v);
      if (p) showPair(p);
    },
    [showPair],
  );
  const clear = useCallback(() => {
    setPicks([]);
    setSheetOpen(false);
  }, [setPicks]);

  const changeSchema = (next: string) => {
    if (next === schema) return;
    setPickState([]);
    setSheetOpen(false);
    setParams(schemaToParams(next, params));
  };

  // ---- the versions, and keeping the reader's place when one arrives -------
  const listRef = useRef<HTMLOListElement>(null);
  const anchor = useRef<ScrollAnchor | null>(null);
  const history = useVersionHistory(schema, () => {
    anchor.current = captureAnchor(listRef.current);
  });
  const list = history.list;
  const diff = useVersionDiff(schema, pair);

  useLayoutEffect(() => {
    if (!anchor.current) return;
    restoreAnchor(listRef.current, anchor.current);
    anchor.current = null;
  }, [list]);

  // A link may name a version the schema never had; once the count is known, that pick goes.
  const total = list?.total ?? 0;
  useEffect(() => {
    if (total === 0) return;
    const valid = picksWithin(picks, total);
    if (valid.length !== picks.length) setPicks(valid);
  }, [total, picks, setPicks]);

  // Versions that arrive while the top of the list is scrolled away are
  // counted on a pill that leads back up; at the top they are seen arriving.
  const topRef = useRef<HTMLDivElement>(null);
  const [atTop, setAtTop] = useState(true);
  const atTopRef = useRef(true);
  atTopRef.current = atTop;
  const hasList = list !== null;
  useEffect(() => {
    const el = topRef.current;
    if (!el) return;
    const io = new IntersectionObserver(([e]) => setAtTop(e ? e.isIntersecting : true));
    io.observe(el);
    return () => io.disconnect();
  }, [hasList]);

  const [unseen, setUnseen] = useState<number[]>([]);
  const [said, setSaid] = useState("");
  const prevList = useRef<VersionList | null>(null);
  useEffect(() => {
    const came = arrivals(prevList.current, list);
    prevList.current = list;
    const first = came[0];
    if (first === undefined) return;
    const by = findVersion(list, first)?.saved_by;
    setSaid(`${came.length === 1 ? `Version ${first} was` : `${came.length} versions were`} just saved${by ? ` by ${by}` : ""}.`);
    if (!atTopRef.current) setUnseen((u) => [...came, ...u]);
  }, [list]);
  useEffect(() => {
    if (atTop) setUnseen([]);
  }, [atTop]);
  useEffect(() => setUnseen([]), [schema]);

  const infoOf = useCallback((v: number) => findVersion(list, v), [list]);
  const panel = (inSheet: boolean) => (
    <ComparePanel
      picks={picks}
      pair={pair}
      diff={diff}
      latest={newestVersion(list)}
      infoOf={infoOf}
      onPair={showPair}
      onClear={clear}
      inSheet={inSheet}
    />
  );

  // ---- what the page says --------------------------------------------------
  let body: ReactNode;
  if (!schema) {
    if (index.status === "loading") body = <ListSkeleton />;
    else if (index.status === "error") {
      body = (
        <Empty title="The schemas could not be listed">
          {index.error} <TryAgain onClick={index.reload} />
        </Empty>
      );
    } else if (index.index && !index.index.available) {
      body = <Empty title="This source cannot be asked for its ontology">{index.index.reason}</Empty>;
    } else {
      body = (
        <Empty title="No schema saved yet">
          Save one with ontology_save and every save is listed here as a version: who saved it, what it changed, and when it validated writes.
        </Empty>
      );
    }
  } else if (history.status === "loading" || history.status === "idle") {
    body = <ListSkeleton />;
  } else if (history.status === "missing") {
    body = (
      <Empty title={`No versions stored for “${schema}”`}>
        Each save of a schema with this id is listed here once it has one.{options.length > 1 ? " Pick another schema above." : ""}
      </Empty>
    );
  } else if (history.status === "error" || !list) {
    body = (
      <Empty title="The versions could not be read">
        {history.error} <TryAgain onClick={history.reload} />
      </Empty>
    );
  } else {
    const oldest = list.versions.at(-1)?.version ?? 1;
    body = (
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)] lg:items-start">
        <section aria-label="Versions" className="min-w-0">
          <div ref={topRef} aria-hidden className="h-px" />
          {/* No height of its own: appearing must not push the list down
              under the reader, which is what the scroll anchor just held. */}
          {unseen.length > 0 && (
            <div className="sticky top-3 z-20 flex h-0 items-start justify-center overflow-visible">
              <Button size="sm" className="rounded-full shadow-md" onClick={() => topRef.current?.scrollIntoView({ behavior: "smooth", block: "start" })}>
                <ArrowUp aria-hidden />
                {unseen.length === 1 ? `v${unseen[0]} just saved` : `${unseen.length} new versions`}
              </Button>
            </div>
          )}
          <ol ref={listRef} className="divide-y overflow-hidden rounded-xl border bg-card">
            {list.versions.map((v, i) => (
              <li key={v.version} data-version={v.version}>
                <VersionRow
                  v={v}
                  now={now}
                  latest={list.exists ? list.currentVersion : 0}
                  role={pickRole(picks, v.version)}
                  renamed={renamedTo(list.versions, i)}
                  fresh={v.version > history.baseline}
                  onToggle={onToggle}
                  onPrevious={onPrevious}
                />
              </li>
            ))}
          </ol>
          <div className="flex flex-col items-center gap-2 py-5 text-center text-sm text-muted-foreground">
            {list.hasMore ? (
              <>
                <Button variant="outline" size="sm" onClick={history.loadOlder} disabled={history.loadingOlder}>
                  {history.loadingOlder ? "Loading older versions" : "Load older versions"}
                </Button>
                <p>
                  The newest {list.versions.length} of {list.total}.
                </p>
              </>
            ) : (
              <p>{list.total === 1 ? "That is the only version." : `That is every version, back to v${oldest}.`}</p>
            )}
            {history.olderError && <p className="text-destructive">{history.olderError}</p>}
          </div>
        </section>
        <aside
          aria-label="Comparison"
          className="hidden rounded-xl border bg-card lg:sticky lg:top-4 lg:block lg:max-h-[calc(100dvh-5rem)] lg:overflow-y-auto"
        >
          {panel(false)}
        </aside>
      </div>
    );
  }

  return (
    // overflow-anchor: none, because the page keeps the reader's place itself
    // when a version is put in above it (see captureAnchor), and the browser's
    // own anchoring, where it exists, would move the list a second time.
    <Page className="max-w-6xl [overflow-anchor:none]">
      <PageHeader
        title="Ontology versions"
        description="Every saved version of a schema: when and by whom it was saved, what it changed, and when it was the active validator. Pick two to see what changed between them."
        actions={
          schema ? (
            <div className="flex flex-wrap items-center gap-2">
              <SchemaPicker value={schema} options={options} onChange={changeSchema} />
              {saved && pageShown("/ontology") && (
                <Button asChild variant="outline" size="sm">
                  <Link to={{ pathname: "/ontology", search: `?schema=${encodeURIComponent(schema)}` }}>
                    <Shapes aria-hidden />
                    Diagram
                  </Link>
                </Button>
              )}
            </div>
          ) : undefined
        }
      />
      {list && history.status === "ready" && (
        <p className="-mt-2 mb-4 text-sm text-muted-foreground">
          <Standing list={list} />
          {ready && !caps.changes && " This source sends no change events, so versions saved from now on show after a reload."}
        </p>
      )}
      {body}

      {/* Below lg the comparison has no room beside the list: a bar says what
          is picked, and the diff opens as a sheet over the list, which keeps
          its scroll position underneath. */}
      {!wide && list && picks.length > 0 && (
        <div className="sticky bottom-3 z-20 mt-2 flex items-center gap-2 rounded-xl border bg-card/95 px-3 py-2 shadow-lg backdrop-blur lg:hidden">
          {pair ? (
            <CompareSummary pair={pair} diff={diff} />
          ) : (
            <span className="text-sm">
              <span className="font-semibold tabular-nums">v{picks[0]}</span> picked. Pick one more.
            </span>
          )}
          <div className="ml-auto flex shrink-0 items-center gap-1">
            {pair && (
              <Button size="sm" onClick={() => setSheetOpen(true)}>
                Show diff
              </Button>
            )}
            <Button size="icon-sm" variant="ghost" aria-label="Clear the picks" onClick={clear}>
              <X aria-hidden />
            </Button>
          </div>
        </div>
      )}
      <Sheet open={sheetOpen && !wide && pair !== null} onOpenChange={setSheetOpen}>
        <SheetContent side="bottom" className="max-h-[88dvh] gap-0 overflow-y-auto rounded-t-xl lg:hidden">
          <SheetHeader className="pr-12 pb-0">
            <SheetTitle className="flex items-center gap-1.5 tabular-nums">
              {pair && (
                <>
                  v{pair.from} <ArrowRight aria-hidden className="size-4" />
                  <span className="sr-only">to</span> v{pair.to}
                </>
              )}
            </SheetTitle>
            <SheetDescription>What changed in {schema} between the two versions.</SheetDescription>
          </SheetHeader>
          {panel(true)}
        </SheetContent>
      </Sheet>
      <p className="sr-only" aria-live="polite">
        {said}
      </p>
    </Page>
  );
}
