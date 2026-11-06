import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, api, isAbort, type ChangeItem } from "@/api";
import { filtersToQuery, type ChangeFilters } from "@/lib/changes";
import { mergeChangeItems } from "@/live";

export type FeedStatus = "loading" | "ready" | "nofeed" | "error";

export interface ChangeFeed {
  status: FeedStatus;
  error: string | null;
  /** Fetched rows, newest first. */
  items: ReadonlyArray<ChangeItem>;
  /** Newest committed seq when the first page was read. */
  head: number;
  hasMore: boolean;
  loadingMore: boolean;
  loadMore: () => void;
  /** Read the newest page again (after a reset or a missed gap). */
  reload: () => void;
}

/**
 * Pages of /api/changes under a filter set. The first page is fetched whenever
 * the filters change; older pages follow `next_before`. A server without the
 * route (404) is reported as "nofeed" rather than as a failure.
 */
export function useChangeFeed(filters: ChangeFilters, key: string, pageSize = 50): ChangeFeed {
  const [status, setStatus] = useState<FeedStatus>("loading");
  const [error, setError] = useState<string | null>(null);
  const [items, setItems] = useState<ReadonlyArray<ChangeItem>>([]);
  const [head, setHead] = useState(0);
  const [nextBefore, setNextBefore] = useState<number | undefined>(undefined);
  const [loadingMore, setLoadingMore] = useState(false);
  const [nonce, setNonce] = useState(0);
  const filtersRef = useRef(filters);
  filtersRef.current = filters;
  const generation = useRef(0);

  useEffect(() => {
    const ctl = new AbortController();
    const gen = ++generation.current;
    setStatus("loading");
    setError(null);
    api
      .changes({ ...filtersToQuery(filtersRef.current), limit: pageSize }, { signal: ctl.signal })
      .then((p) => {
        setItems(p.items ?? []);
        setHead(p.head ?? 0);
        setNextBefore(p.next_before);
        setStatus("ready");
      })
      .catch((err: unknown) => {
        if (isAbort(err) || gen !== generation.current) return;
        if (err instanceof ApiError && err.status === 404) {
          setItems([]);
          setNextBefore(undefined);
          setStatus("nofeed");
          return;
        }
        setError(err instanceof Error ? err.message : String(err));
        setStatus("error");
      });
    return () => ctl.abort();
  }, [key, nonce, pageSize]);

  const loadMore = useCallback(() => {
    if (nextBefore === undefined || loadingMore) return;
    const gen = generation.current;
    setLoadingMore(true);
    api
      .changes({ ...filtersToQuery(filtersRef.current), before: nextBefore, limit: pageSize })
      .then((p) => {
        if (gen !== generation.current) return;
        setItems((cur) => mergeChangeItems(cur, p.items ?? [], Number.MAX_SAFE_INTEGER));
        setNextBefore(p.next_before);
      })
      .catch((err: unknown) => {
        if (gen === generation.current) setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => setLoadingMore(false));
  }, [nextBefore, loadingMore, pageSize]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);

  return { status, error, items, head, hasMore: nextBefore !== undefined, loadingMore, loadMore, reload };
}
