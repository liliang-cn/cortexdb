import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, isAbort } from "@/api";
import { recallsApi, type RecallEntry, type RecallStats } from "@/api/recalls";
import { applyHead, applyOlder, emptyTimeline, filtersKey, joinPending, listQuery, type RecallsOptions, type TimelineData } from "@/lib/recalls";
import { useLive } from "@/live";

export const PAGE_SIZE = 50;

/**
 * How often the head of the timeline is read again while the tab is in view.
 *
 * The live stream does not say when a recall happened. Its activity events
 * are the tool calls the MCP server in this process handled, which carry no
 * recall id and miss every recall another process or machine made on a
 * shared brain; the change feed leaves out the recall bookkeeping on purpose.
 * So the timeline asks the log itself, on a modest interval, and an activity
 * event only makes it ask sooner.
 */
export const HEAD_EVERY_MS = 15_000;
const NUDGE_MS = 600;

export interface Failure {
  message: string;
  /** The HTTP status; 0 when the request never got an answer. */
  status: number;
}

function failure(err: unknown): Failure {
  if (err instanceof ApiError) return { message: err.message, status: err.status };
  return { message: err instanceof Error ? err.message : String(err), status: 0 };
}

export type LoadStatus = "loading" | "ready" | "error";

export interface RecallTimeline extends TimelineData {
  status: LoadStatus;
  error: Failure | null;
  hasMore: boolean;
  loadingMore: boolean;
  /** An older page failed; the list itself is fine. */
  moreError: string | null;
  loadMore: () => void;
  reload: () => void;
  /** Bring the recalls held while scrolled away into the list. */
  release: () => void;
  /** Change one recall in the list, as a mark given in the detail changes it. */
  patch: (id: string, fn: (r: RecallEntry) => RecallEntry) => void;
  dismissRebuilt: () => void;
}

/**
 * The timeline under a set of filters: the first page whenever the filters
 * change, older pages on demand, and new recalls at the top as they happen.
 * `hold` is true while the reader is scrolled away from the top of the list.
 */
export function useRecallTimeline(opts: RecallsOptions, enabled: boolean, hold: boolean): RecallTimeline {
  const key = filtersKey(opts);
  const [status, setStatus] = useState<LoadStatus>("loading");
  const [error, setError] = useState<Failure | null>(null);
  const [data, setData] = useState<TimelineData>(emptyTimeline);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);

  const optsRef = useRef(opts);
  optsRef.current = opts;
  const holdRef = useRef(hold);
  holdRef.current = hold;
  const statusRef = useRef(status);
  statusRef.current = status;
  // A response for filters that have since changed is dropped, never shown.
  const generation = useRef(0);

  useEffect(() => {
    if (!enabled) return;
    const ctl = new AbortController();
    const gen = ++generation.current;
    setStatus("loading");
    setError(null);
    setMoreError(null);
    setLoadingMore(false);
    setData(emptyTimeline);
    recallsApi
      .list(listQuery(optsRef.current, 0, PAGE_SIZE), { signal: ctl.signal })
      .then((page) => {
        if (gen !== generation.current) return;
        setData({ ...emptyTimeline, items: page.recalls ?? [], nextBefore: page.next_before || undefined });
        setStatus("ready");
      })
      .catch((err: unknown) => {
        if (isAbort(err) || gen !== generation.current) return;
        setError(failure(err));
        setStatus("error");
      });
    return () => ctl.abort();
  }, [key, nonce, enabled]);

  const inFlight = useRef(false);
  const refreshHead = useCallback(() => {
    if (statusRef.current !== "ready" || inFlight.current) return;
    inFlight.current = true;
    const gen = generation.current;
    recallsApi
      .list(listQuery(optsRef.current, 0, PAGE_SIZE))
      .then((page) => {
        if (gen !== generation.current) return;
        // A functional update, so an older page that landed a moment before is kept.
        setData((d) => applyHead(d, page.recalls ?? [], page.next_before, holdRef.current));
      })
      // A head that cannot be read now is read again on the next tick; the
      // list already shown stays as it is.
      .catch(() => undefined)
      .finally(() => {
        inFlight.current = false;
      });
  }, []);

  // The interval, and a read the moment the tab comes back into view.
  useEffect(() => {
    if (!enabled) return;
    const tick = () => {
      if (document.visibilityState === "visible") refreshHead();
    };
    const timer = setInterval(tick, HEAD_EVERY_MS);
    document.addEventListener("visibilitychange", tick);
    return () => {
      clearInterval(timer);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [enabled, refreshHead]);

  // A tool call this process just handled may have been a recall: look now
  // rather than at the next tick. The stream is the app's one EventSource;
  // this only listens to what it already delivers.
  useEffect(() => {
    if (!enabled) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = useLive.subscribe((s, prev) => {
      if (s.activity === prev.activity || s.activity[0] === prev.activity[0]) return;
      clearTimeout(timer);
      timer = setTimeout(refreshHead, NUDGE_MS);
    });
    return () => {
      clearTimeout(timer);
      unsubscribe();
    };
  }, [enabled, refreshHead]);

  // Back at the top: what waited joins the list.
  useEffect(() => {
    if (!hold) setData((d) => joinPending(d));
  }, [hold]);

  const { nextBefore } = data;
  const loadMore = useCallback(() => {
    if (!nextBefore || loadingMore) return;
    const gen = generation.current;
    setLoadingMore(true);
    setMoreError(null);
    recallsApi
      .list(listQuery(optsRef.current, nextBefore, PAGE_SIZE))
      .then((page) => {
        if (gen !== generation.current) return;
        setData((d) => applyOlder(d, page.recalls ?? [], page.next_before));
      })
      .catch((err: unknown) => {
        if (gen === generation.current) setMoreError(failure(err).message);
      })
      .finally(() => {
        if (gen === generation.current) setLoadingMore(false);
      });
  }, [nextBefore, loadingMore]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  const release = useCallback(() => setData((d) => joinPending(d)), []);
  const dismissRebuilt = useCallback(() => setData((d) => ({ ...d, rebuilt: false })), []);
  const patch = useCallback(
    (id: string, fn: (r: RecallEntry) => RecallEntry) => setData((d) => ({ ...d, items: d.items.map((r) => (r.id === id ? fn(r) : r)) })),
    [],
  );

  return { ...data, status, error, hasMore: Boolean(nextBefore), loadingMore, moreError, loadMore, reload, release, patch, dismissRebuilt };
}

export interface RecallDetailState {
  status: LoadStatus | "idle";
  entry: RecallEntry | null;
  error: Failure | null;
  /** Replace the shown recall, as a mark changes it. */
  update: (fn: (e: RecallEntry) => RecallEntry) => void;
  reload: () => void;
}

/** One recall with its explain record. */
export function useRecallDetail(id: string): RecallDetailState {
  const [status, setStatus] = useState<RecallDetailState["status"]>("idle");
  const [entry, setEntry] = useState<RecallEntry | null>(null);
  const [error, setError] = useState<Failure | null>(null);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    if (!id) {
      setStatus("idle");
      setEntry(null);
      setError(null);
      return;
    }
    const ctl = new AbortController();
    setStatus("loading");
    setError(null);
    recallsApi
      .get(id, { signal: ctl.signal })
      .then((e) => {
        setEntry(e);
        setStatus("ready");
      })
      .catch((err: unknown) => {
        if (isAbort(err)) return;
        setEntry(null);
        setError(failure(err));
        setStatus("error");
      });
    return () => ctl.abort();
  }, [id, nonce]);

  const update = useCallback((fn: (e: RecallEntry) => RecallEntry) => setEntry((e) => (e ? fn(e) : e)), []);
  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { status, entry: entry && entry.id === id ? entry : null, error, update, reload };
}

export const STATS_LIMIT = 50;

export interface RecallStatsState {
  status: LoadStatus;
  stats: RecallStats | null;
  error: Failure | null;
  reload: () => void;
}

/** The per-record view of the log, read each time it is opened. */
export function useRecallStats(enabled: boolean): RecallStatsState {
  const [status, setStatus] = useState<LoadStatus>("loading");
  const [stats, setStats] = useState<RecallStats | null>(null);
  const [error, setError] = useState<Failure | null>(null);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    const ctl = new AbortController();
    setStatus("loading");
    setError(null);
    recallsApi
      .stats(STATS_LIMIT, { signal: ctl.signal })
      .then((s) => {
        setStats(s);
        setStatus("ready");
      })
      .catch((err: unknown) => {
        if (isAbort(err)) return;
        setError(failure(err));
        setStatus("error");
      });
    return () => ctl.abort();
  }, [enabled, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { status, stats, error, reload };
}

/** Whether a media query matches, kept up to date. */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => typeof window !== "undefined" && window.matchMedia(query).matches);
  useEffect(() => {
    const mq = window.matchMedia(query);
    const on = () => setMatches(mq.matches);
    on();
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [query]);
  return matches;
}
