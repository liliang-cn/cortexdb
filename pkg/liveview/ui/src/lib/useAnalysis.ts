import { useCallback, useEffect, useState } from "react";
import { isAbort } from "@/api";
import { analysisApi, type AnalysisFields, type AnalysisOverview, type AnalysisQuery, type AnalysisResult } from "@/api/analysis";
import { incomplete, type AnalysisState, type RangeDays } from "./analysis";

/** How often the overview is read again while the tab is in view. */
export const OVERVIEW_MS = 60_000;

/** An answer that is read, kept while the next one is on its way. */
export interface Loaded<T> {
  /** The latest answer to the current question, or null until one arrives. */
  data: T | null;
  loading: boolean;
  /** Why the latest read failed, in the server's words; null when it did not. */
  error: string | null;
  reload: () => void;
}

/**
 * One answer per key. A new key drops the old answer, so a chart never shows
 * one question's numbers under another's controls; a re-read of the same key
 * keeps it on screen. A response that arrives for a question since replaced
 * is dropped. Nothing is read while `enabled` is false.
 */
function useLoaded<T>(key: string, enabled: boolean, read: (signal: AbortSignal) => Promise<T>, every = 0): Loaded<T> {
  const [state, setState] = useState<{ key: string; data: T | null; error: string | null }>({ key, data: null, error: null });
  const [loading, setLoading] = useState(false);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    let ctl: AbortController | null = null;
    const load = () => {
      ctl?.abort();
      const mine = new AbortController();
      ctl = mine;
      setLoading(true);
      read(mine.signal)
        .then((data) => setState({ key, data, error: null }))
        .catch((err: unknown) => {
          if (isAbort(err)) return;
          const error = err instanceof Error ? err.message : String(err);
          // The answer on screen stays when a re-read of the same question fails.
          setState((s) => ({ key, data: s.key === key ? s.data : null, error }));
        })
        .finally(() => {
          if (ctl === mine) setLoading(false);
        });
    };
    load();
    const timer = every > 0 ? setInterval(() => !document.hidden && load(), every) : undefined;
    return () => {
      ctl?.abort();
      if (timer) clearInterval(timer);
    };
    // `read` is rebuilt on every render; the key names the question it asks.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, enabled, nonce, every]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  const current = state.key === key;
  return { data: current ? state.data : null, loading: loading || (enabled && !current), error: current ? state.error : null, reload };
}

/** The overview for a range, read again every OVERVIEW_MS while it is shown. */
export function useOverview(range: RangeDays, enabled: boolean): Loaded<AnalysisOverview> {
  return useLoaded(`overview:${range}`, enabled, (signal) => analysisApi.overview(range, { signal }), OVERVIEW_MS);
}

/** The fields the builder offers, for a collection. */
export function useFields(collection: string, enabled: boolean): Loaded<AnalysisFields> {
  return useLoaded(`fields:${collection}`, enabled, (signal) => analysisApi.fields(collection, { signal }));
}

/** The builder's answer. A question that cannot be asked yet is not sent. */
export function useAnswer(query: AnalysisState["query"], enabled: boolean): Loaded<AnalysisResult> {
  const q: AnalysisQuery = query;
  return useLoaded(`query:${JSON.stringify(query)}`, enabled && incomplete(query) === null, (signal) => analysisApi.query(q, { signal }));
}
