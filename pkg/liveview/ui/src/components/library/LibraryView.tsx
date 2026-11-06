// The library page: the brain as a reading room, full bleed under the top
// bar, with the app's panels floating over it.
//
// This module is loaded lazily (three.js is most of its weight), so the other
// pages never pay for it. The room itself is scene.ts, drawn from the plan in
// lib/library.ts; this file reads the data, keeps the URL as the record of
// what is open (?book=, ?card=), and lays the panels out: side cards on a
// desktop, a bottom sheet on a phone.
//
// Live: the app keeps one stream, and the library reads it from the store. A
// newer snapshot re-reads the room and rebuilds it; a call that names books
// lights them where they stand.

import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { useSearchParams } from "react-router";
import { House, Library as LibraryIcon, Loader2 } from "lucide-react";
import { api, isAbort, type ActivityEvent, type FindResult } from "@/api";
import { Empty } from "@/components/Page";
import { Button } from "@/components/ui/button";
import { BottomSheet, Card, CardHeader, IconButton, useIsNarrow } from "@/components/graph/primitives";
import { useActivity, useLive } from "@/live";
import { useLang } from "@/lib/i18n";
import { booksRead, findBooks, layoutLibrary, readOpening, shortId, totals, whereOf, withOpening, type BookSpot, type Layout, type Opening, type PlanText } from "@/lib/library";
import { clip } from "@/lib/libraryPages";
import { BOOK_SERIF, roomTone } from "@/lib/libraryPalette";
import { LIBRARY_TEXT, type LibraryText } from "@/lib/libraryStrings";
import { cn } from "@/lib/utils";
import { useDarkClass, useLibrary, useOpened, type Opened } from "./hooks";
import { BookBody, BookTitle, CardBody, CardTitle, Feed, FindBox, FindResults, Pager, Source, Stats, type Found } from "./panels";
import { LibraryScene, type SceneEvents } from "./scene";

const FIND_MS = 220;
const NOTE_MS = 3500;

const planText = (t: LibraryText): PlanText => ({ wingSub: t.wingSub, stacks: t.stacks, stacksSub: t.stacksSub, annex: t.annex, reference: t.reference, docs: t.docs });
const callKey = (e: ActivityEvent) => `${e.seq}:${e.at}`;

type HoverFn = (spot: BookSpot | null, x: number, y: number) => void;

export default function LibraryView() {
  const lang = useLang();
  const t = LIBRARY_TEXT[lang];
  const narrow = useIsNarrow();
  const dark = useDarkClass();
  const [params, setParams] = useSearchParams();
  const opening = useMemo(() => readOpening(params), [params]);
  const { lib, error, reload } = useLibrary();
  const layout = useMemo(() => (lib ? layoutLibrary(lib, planText(t)) : null), [lib, t]);
  const { opened, loading: openLoading, error: openError } = useOpened(opening, lib?.version);

  const stage = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLDivElement>(null);
  const labels = useRef<HTMLDivElement>(null);
  const findRef = useRef<HTMLInputElement>(null);
  const sceneRef = useRef<LibraryScene | null>(null);
  const hoverRef = useRef<HoverFn>(() => {});
  const [built, setBuilt] = useState(0);
  const [page, setPage] = useState(0);
  const [pages, setPages] = useState(0);
  // What the room shows now, as "kind:id": a new one is flown to, the same
  // one laid out again after a rebuild is not.
  const shown = useRef("");

  // ---- notes ---------------------------------------------------------
  const [note, setNote] = useState("");
  const noteTimer = useRef(0);
  const say = useCallback((text: string) => {
    setNote(text);
    clearTimeout(noteTimer.current);
    noteTimer.current = window.setTimeout(() => setNote(""), NOTE_MS);
  }, []);
  useEffect(() => () => clearTimeout(noteTimer.current), []);

  // ---- what is open: the URL says, so a link, Back and a reload all agree ----
  const openIn = useCallback(
    (o: Opening) => {
      setParams((prev) => new URLSearchParams(withOpening(prev, o)));
    },
    [setParams],
  );
  const close = useCallback(() => openIn(null), [openIn]);

  // The handlers the scene and the stream call are made once; they read the
  // page's latest state through these, kept current after every render.
  const pageRef = useRef(page);
  const now = useRef({ layout, opening, q: "", t });
  const keys = useRef({ opening, opened, page, pages, query: "" });

  // ---- finding -------------------------------------------------------
  const [query, setQuery] = useState("");
  const [q, setQ] = useState("");
  const [resultsOpen, setResultsOpen] = useState(false);
  useEffect(() => {
    const v = query.trim();
    const timer = setTimeout(() => setQ(v), v ? FIND_MS : 0);
    if (v) setResultsOpen(true);
    return () => clearTimeout(timer);
  }, [query]);
  const foundBooks = useMemo(() => (layout && q ? findBooks(layout.books, q) : []), [layout, q]);
  const [cardHits, setCardHits] = useState<{ q: string; cards: FindResult[]; off: boolean }>({ q: "", cards: [], off: false });
  useEffect(() => {
    if (!q) return;
    const ctl = new AbortController();
    api
      .find(q, undefined, { signal: ctl.signal })
      .then((r) => setCardHits({ q, cards: (r.results ?? []).filter((n) => n.type !== "memory").slice(0, 8), off: false }))
      .catch((err) => {
        if (!isAbort(err)) setCardHits({ q, cards: [], off: true });
      });
    return () => ctl.abort();
  }, [q]);
  const found: Found = useMemo(
    () => ({ books: foundBooks, cards: cardHits.q === q ? cardHits.cards : [], off: cardHits.q === q && cardHits.off, done: cardHits.q === q }),
    [foundBooks, cardHits, q],
  );

  // ---- the scene: made once, rebuilt whenever the room or the light changes ----
  const events = useRef<SceneEvents | null>(null);
  useEffect(() => {
    pageRef.current = page;
    now.current = { layout, opening, q, t };
    keys.current = { opening, opened, page, pages, query };
    events.current = {
      onBook: (id) => openIn({ kind: "book", id }),
      onCard: (id) => openIn({ kind: "card", id }),
      onDoc: (b) => say(`${b.label} · ${t.document(b.chunks)}`),
      onCatalogue: () => {
        findRef.current?.focus();
        if (query.trim()) setResultsOpen(true);
      },
      onHover: (spot, x, y) => hoverRef.current(spot, x, y),
    };
  });

  useEffect(() => {
    const c = canvas.current;
    const l = labels.current;
    const s = stage.current;
    if (!c || !l || !s) return;
    const scene = new LibraryScene(c, l, s, {
      onBook: (id) => events.current?.onBook(id),
      onCard: (id) => events.current?.onCard(id),
      onDoc: (b) => events.current?.onDoc(b),
      onCatalogue: () => events.current?.onCatalogue(),
      onHover: (b, x, y) => events.current?.onHover(b, x, y),
    });
    sceneRef.current = scene;
    // A handle for poking at the room from the console while developing; it
    // goes when the room does, so a disposed scene is never left reachable.
    const dev = window as unknown as Record<string, unknown>;
    if (import.meta.env.DEV) dev.__library = { scene, live: useLive };
    return () => {
      sceneRef.current = null;
      shown.current = "";
      if (import.meta.env.DEV) delete dev.__library;
      scene.dispose();
    };
  }, []);

  useEffect(() => {
    const scene = sceneRef.current;
    if (!scene || !layout) return;
    scene.build({ layout, night: dark, text: t, compact: narrow });
    setBuilt((n) => n + 1);
  }, [layout, dark, narrow, t]);

  // ---- putting what is open into the room ----------------------------
  useEffect(() => {
    const scene = sceneRef.current;
    if (!scene || !layout || !built) return;
    if (!opened) {
      // While the next thing is being read the room keeps the last one.
      if (opening && !openError) return;
      if (shown.current) {
        const wasBook = shown.current.startsWith("book:");
        scene.clearFx();
        if (wasBook) scene.home(true);
      }
      shown.current = "";
      return;
    }
    const key = `${opened.kind}:${opened.id}`;
    const fresh = key !== shown.current;
    shown.current = key;
    if (opened.kind === "book") {
      const at = fresh ? 0 : pageRef.current;
      const n = scene.openBook(opened.book, at, fresh);
      setPages(n);
      if (fresh) setPage(0);
      else if (at > n - 1) setPage(Math.max(0, n - 1));
    } else {
      scene.showCard(opened.card, fresh);
    }
  }, [opened, opening, openError, layout, built]);

  useEffect(() => {
    if (opened?.kind === "book") sceneRef.current?.setPage(page);
  }, [page, opened]);

  useEffect(() => {
    if (openError) say(t.notFound);
  }, [openError, say, t]);

  // What a search found lights up, unless something is open: an open book or
  // card keeps the room until it is closed.
  useEffect(() => {
    const scene = sceneRef.current;
    if (!scene || !built || opening) return;
    if (q) scene.showFound(foundBooks.map((b) => b.id));
    else scene.clearFx();
  }, [foundBooks, q, built, opening]);

  // ---- calls that read books -----------------------------------------
  useEffect(() => {
    const seen = new Set<string>();
    let last = useLive.getState().activity;
    for (const e of last) seen.add(callKey(e));
    return useLive.subscribe((s) => {
      if (s.activity === last) return;
      last = s.activity;
      const fresh: ActivityEvent[] = [];
      for (const e of s.activity) {
        const k = callKey(e);
        if (seen.has(k)) break;
        seen.add(k);
        fresh.push(e);
      }
      if (seen.size > 2000) {
        seen.clear();
        for (const e of s.activity) seen.add(callKey(e));
      }
      const cur = now.current;
      const scene = sceneRef.current;
      // Nothing is lit over an open book or card, or over what a search found.
      if (!scene || !cur.layout || cur.opening || cur.q) return;
      // Newest first: the latest call that read anything is the one shown.
      for (const e of fresh) {
        if (e.failed) continue;
        const spots = booksRead(cur.layout.books, e);
        if (spots.length) {
          scene.readInPlace(spots, `${cur.t.reading} · ${e.text || e.tool}`);
          break;
        }
      }
    });
  }, []);

  // ---- keys ----------------------------------------------------------
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null;
      const typing = !!el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable);
      const k = keys.current;
      if (e.key === "Escape") {
        if (typing) return;
        if (k.opening) close();
        else if (k.query) setQuery("");
        setResultsOpen(false);
        return;
      }
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === "/") {
        e.preventDefault();
        findRef.current?.focus();
      } else if (k.opened?.kind === "book" && e.key === "ArrowLeft" && k.page > 0) setPage(k.page - 1);
      else if (k.opened?.kind === "book" && e.key === "ArrowRight" && k.page < k.pages - 1) setPage(k.page + 1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [close]);

  // ---- the panels ----------------------------------------------------
  const activity = useActivity();
  const liveLink = useLive((s) => s.link === "open");
  const showResults = resultsOpen && !!q;
  const pick = () => {
    setResultsOpen(false);
    findRef.current?.blur();
  };
  const home = () => {
    if (opening) close();
    else sceneRef.current?.home(true);
  };

  const panel = layout && opening ? <OpenPanel opening={opening} opened={opened} loading={openLoading} layout={layout} t={t} /> : null;
  const panelTitle = opened?.kind === "book" ? <BookTitle id={opened.id} /> : opened?.kind === "card" ? <CardTitle opened={opened} /> : opening ? shortId(opening.id) : "";
  const reading = opened?.kind === "book" && opening?.kind === "book";
  const pager = (closeable: boolean) => <Pager page={page} pages={pages} onPage={setPage} onClose={close} t={t} closeable={closeable} />;

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <div ref={stage} className="relative min-h-0 flex-1 overflow-hidden" style={{ background: roomTone(dark).background }}>
        <div ref={canvas} className="absolute inset-0" />
        {/* isolate: the label renderer gives every label a z-index to sort them
            by depth, and without a stacking context of their own they would
            sort above the panels too. */}
        <div ref={labels} className="pointer-events-none absolute inset-0 isolate overflow-hidden" aria-hidden />
        <HoverTip register={hoverRef} stage={stage} t={t} />
        <Status lib={!!lib} error={error} empty={!!layout && layout.books.length === 0} reload={reload} t={t} />

        {layout && !narrow && (
          <div className="pointer-events-none absolute inset-3 flex min-h-0 gap-3">
            <div className="flex min-h-0 w-80 shrink-0 flex-col gap-3">
              <Card className="shrink-0">
                <div className="px-3 pb-3 pt-2.5">
                  <h1 className="flex items-center gap-2 text-sm font-semibold">
                    <LibraryIcon aria-hidden className="size-4 text-muted-foreground" />
                    {t.title}
                  </h1>
                  <Source layout={layout} t={t} />
                  <Stats layout={layout} t={t} className="mt-2.5" />
                </div>
              </Card>
              <Card className={cn("min-h-0", showResults ? "shrink" : "shrink-0")}>
                <FindBox ref={findRef} value={query} onChange={setQuery} onFocus={() => setResultsOpen(true)} t={t} />
                {showResults && (
                  <div className="flex min-h-0 flex-col border-t">
                    <FindResults found={found} t={t} onPick={pick} />
                  </div>
                )}
              </Card>
            </div>

            <div className="relative min-w-0 flex-1">
              <Toast text={note} className="top-0" />
              <div className="absolute inset-x-0 bottom-0 flex justify-center">
                <Card occludes="bottom" className="flex-row items-center gap-0.5 rounded-full p-1">
                  <IconButton label={t.home} icon={<House />} onClick={home} className="rounded-full" />
                  {reading && (
                    <>
                      <span aria-hidden className="mx-1 h-5 w-px bg-border" />
                      {pager(true)}
                    </>
                  )}
                </Card>
              </div>
            </div>

            <div className="flex min-h-0 w-[22rem] shrink-0 flex-col gap-3">
              {panel ? (
                <Card occludes="right" className="min-h-0 flex-1">
                  <CardHeader title={panelTitle} onClose={close} />
                  {panel}
                </Card>
              ) : (
                <div className="min-h-0 flex-1" />
              )}
              <Card className="shrink-0">
                <Feed events={activity} live={liveLink} t={t} />
              </Card>
            </div>
          </div>
        )}

        {layout && narrow && (
          <div className="pointer-events-none absolute inset-0">
            <div data-occludes="top" className="absolute inset-x-3 top-3 z-10 flex flex-col gap-2">
              <Card>
                <FindBox ref={findRef} value={query} onChange={setQuery} onFocus={() => setResultsOpen(true)} t={t} />
                {!showResults && <MobileStats layout={layout} t={t} />}
              </Card>
              {showResults && (
                <Card className="max-h-[55dvh]">
                  <FindResults found={found} t={t} onPick={pick} />
                </Card>
              )}
            </div>
            <Toast text={note} className="top-[4.75rem] px-3" />
            {!panel && (
              <Card occludes="bottom" className="absolute bottom-3 left-1/2 z-10 -translate-x-1/2 flex-row rounded-full p-1">
                <IconButton label={t.home} icon={<House />} onClick={home} className="rounded-full" />
              </Card>
            )}
            <BottomSheet open={!!panel} onClose={close} title={panelTitle}>
              {reading && <div className="flex shrink-0 justify-center border-b px-2 py-1">{pager(false)}</div>}
              {panel}
            </BottomSheet>
          </div>
        )}
      </div>
    </div>
  );
}

function OpenPanel({ opening, opened, loading, layout, t }: { opening: NonNullable<Opening>; opened: Opened | null; loading: boolean; layout: Layout; t: LibraryText }) {
  if (!opened || opened.kind !== opening.kind || opened.id !== opening.id) {
    return (
      <div className="flex flex-1 items-center justify-center gap-2 p-6 text-sm text-muted-foreground">
        {loading ? <Loader2 className="size-4 animate-spin" /> : null}
        {loading ? shortId(opening.id) : t.notFound}
      </div>
    );
  }
  return opened.kind === "book" ? <BookBody opened={opened} layout={layout} t={t} records={layout.lib.records} /> : <CardBody opened={opened} layout={layout} t={t} />;
}

function MobileStats({ layout, t }: { layout: Layout; t: LibraryText }) {
  const n = totals(layout.lib);
  return (
    <p className="truncate border-t px-3 py-1 text-[11px] text-muted-foreground">
      {[`${n.books.toLocaleString()} ${t.books}`, `${n.shelves.toLocaleString()} ${t.shelves}`, `${layout.lib.cards.toLocaleString()} ${t.cards}`].join(" · ")}
    </p>
  );
}

function Status({ lib, error, empty, reload, t }: { lib: boolean; error: string; empty: boolean; reload: () => void; t: LibraryText }) {
  if (!lib && !error) {
    return (
      <div className="pointer-events-none absolute inset-0 flex items-center justify-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        {t.loading}
      </div>
    );
  }
  if (!lib && error) {
    return (
      <div className="absolute inset-0 flex items-center justify-center p-6">
        <div className="max-w-sm rounded-xl border bg-card p-5 text-center shadow-sm">
          <p className="text-sm text-destructive">{t.failed(error)}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={reload}>
            {t.retry}
          </Button>
        </div>
      </div>
    );
  }
  if (empty) {
    return (
      <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-6">
        <div className="max-w-sm bg-card/90">
          <Empty title={t.empty}>{t.emptyHint}</Empty>
        </div>
      </div>
    );
  }
  return null;
}

function Toast({ text, className }: { text: string; className?: string }) {
  return (
    <div role="status" aria-live="polite" className={cn("pointer-events-none absolute inset-x-0 flex justify-center transition-opacity duration-200", text ? "opacity-100" : "opacity-0", className)}>
      {text && <div className="max-w-full truncate rounded-full bg-foreground px-4 py-1.5 text-sm text-background shadow-lg">{text}</div>}
    </div>
  );
}

/** The book under the mouse: its id, where it stands and how it begins. Follows the pointer; a touch has no hover. */
function HoverTip({ register, stage, t }: { register: RefObject<HoverFn>; stage: RefObject<HTMLDivElement | null>; t: LibraryText }) {
  const [tip, setTip] = useState<{ spot: BookSpot; x: number; y: number } | null>(null);
  useEffect(() => {
    register.current = (spot, x, y) => setTip(spot ? { spot, x, y } : null);
    return () => {
      register.current = () => {};
    };
  }, [register]);
  const el = stage.current;
  if (!tip || !el) return null;
  const r = el.getBoundingClientRect();
  const left = Math.max(8, Math.min(tip.x - r.left + 14, r.width - 328));
  const top = Math.max(8, Math.min(tip.y - r.top + 16, r.height - 96));
  const b = tip.spot;
  return (
    <div className="pointer-events-none absolute z-20 max-w-80 rounded-lg border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md" style={{ left, top }}>
      <p className="break-all text-[13px] font-medium" style={{ fontFamily: BOOK_SERIF }}>
        {b.doc ? b.label : shortId(b.id)}
      </p>
      <p className="text-muted-foreground">{b.doc ? t.document(b.chunks) : whereOf(b, t)}</p>
      {!b.doc && b.label && <p className="mt-0.5">{clip(b.label, 140)}</p>}
    </div>
  );
}
