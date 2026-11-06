// The Versions page's reads: the saved schemas, one schema's versions page by
// page, and the diff between two of them. The joining rules live in
// ontologyHistory.ts; these hooks only fetch, follow the live store, and hand
// the answers to those rules.

import { useCallback, useEffect, useRef, useState } from "react";
import { isAbort } from "@/api";
import { ontologyHistoryApi, type OntologyDiff, type OntologySchemaIndex } from "@/api/ontologyHistory";
import { useLive } from "@/live";
import {
  appendOlder,
  applyRefresh,
  isNoHistory,
  listFromPage,
  newestSchemaChange,
  newestVersion,
  olderOffset,
  PAGE_SIZE,
  refreshLimit,
  type Pair,
  type VersionList,
} from "./ontologyHistory";

/** A burst of change rows (one save writes several) is one re-read. */
const REFRESH_DEBOUNCE_MS = 400;

const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

/**
 * A number that moves whenever the history may have: the newest live change
 * to the schema's row, plus the times the change cursor fell behind and the
 * ring was emptied, after which nothing in it can be trusted to be complete.
 */
function useSchemaPulse(schema: string | null): string {
  const seq = useLive((s) => newestSchemaChange(s.changes, schema));
  const resets = useLive((s) => s.resets);
  return `${resets}:${seq}`;
}

// ---- the saved schemas ------------------------------------------------------

export interface SchemaIndexState {
  status: "loading" | "ready" | "error";
  index: OntologySchemaIndex | null;
  error: string | null;
  reload: () => void;
}

/** The saved schemas, read again when any schema is saved, activated or deleted. */
export function useSchemaIndex(): SchemaIndexState {
  const [state, setState] = useState<Omit<SchemaIndexState, "reload">>({ status: "loading", index: null, error: null });
  const [nonce, setNonce] = useState(0);
  const pulse = useSchemaPulse(null);
  const read = useRef(false);

  useEffect(() => {
    const ctl = new AbortController();
    const first = !read.current;
    const run = () =>
      ontologyHistoryApi
        .schemas({ signal: ctl.signal })
        .then((index) => {
          read.current = true;
          setState({ status: "ready", index, error: null });
        })
        .catch((err: unknown) => {
          if (isAbort(err)) return;
          // A failed re-read leaves the list it already had: still right as of its read.
          setState((s) => (s.index ? s : { status: "error", index: null, error: message(err) }));
        });
    const t = setTimeout(run, first ? 0 : REFRESH_DEBOUNCE_MS);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [pulse, nonce]);

  const reload = useCallback(() => {
    setState({ status: "loading", index: null, error: null });
    setNonce((n) => n + 1);
  }, []);
  return { ...state, reload };
}

// ---- one schema's versions ---------------------------------------------------

export type HistoryStatus = "idle" | "loading" | "ready" | "missing" | "error";

export interface VersionHistory {
  status: HistoryStatus;
  list: VersionList | null;
  error: string | null;
  /**
   * The newest version when the schema was first read. Rows above it were
   * saved while the page was open, and flash once as they arrive.
   */
  baseline: number;
  loadingOlder: boolean;
  olderError: string | null;
  loadOlder: () => void;
  reload: () => void;
}

interface HistoryState {
  status: HistoryStatus;
  list: VersionList | null;
  error: string | null;
  baseline: number;
}

/**
 * A schema's versions, newest first, an older page at a time. A live change
 * to the schema re-reads what is shown and joins it in place (applyRefresh),
 * so the list never empties and redraws: nothing the reader picked or
 * scrolled to is lost. `beforeRefresh` runs just before such a re-read is
 * applied, while the old rows are still on screen, so the page can note where
 * the reader was.
 */
export function useVersionHistory(schema: string, beforeRefresh?: () => void): VersionHistory {
  const [state, setState] = useState<HistoryState>({ status: schema ? "loading" : "idle", list: null, error: null, baseline: 0 });
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [olderError, setOlderError] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);

  const listRef = useRef<VersionList | null>(null);
  listRef.current = state.list;
  const beforeRef = useRef(beforeRefresh);
  beforeRef.current = beforeRefresh;
  const generation = useRef(0);
  const olderBusy = useRef(false);

  // The first page, whenever the schema changes or a reload is asked for.
  useEffect(() => {
    const gen = ++generation.current;
    olderBusy.current = false;
    setLoadingOlder(false);
    setOlderError(null);
    if (!schema) {
      setState({ status: "idle", list: null, error: null, baseline: 0 });
      return;
    }
    setState({ status: "loading", list: null, error: null, baseline: 0 });
    const ctl = new AbortController();
    ontologyHistoryApi
      .history({ schema, limit: PAGE_SIZE, offset: 0 }, { signal: ctl.signal })
      .then((page) => {
        if (gen !== generation.current) return;
        const list = listFromPage(page);
        setState({ status: "ready", list, error: null, baseline: newestVersion(list) });
      })
      .catch((err: unknown) => {
        if (isAbort(err) || gen !== generation.current) return;
        const text = message(err);
        setState({ status: isNoHistory(text) ? "missing" : "error", list: null, error: text, baseline: 0 });
      });
    return () => ctl.abort();
  }, [schema, nonce]);

  // A live change to the schema: re-read what is shown, a beat later.
  const pulse = useSchemaPulse(schema || null);
  const statusRef = useRef(state.status);
  statusRef.current = state.status;
  const seen = useRef<{ schema: string; pulse: string } | null>(null);
  useEffect(() => {
    // The pulse as it stood when a schema was opened is not news, whatever
    // it was for the schema before.
    if (!seen.current || seen.current.schema !== schema) {
      seen.current = { schema, pulse };
      return;
    }
    if (pulse === seen.current.pulse || !schema) return;
    seen.current = { schema, pulse };
    const gen = generation.current;
    const ctl = new AbortController();
    const t = setTimeout(() => {
      const shown = listRef.current;
      // The first page is still on its way, and it will be newer than the change.
      if (!shown && statusRef.current === "loading") return;
      ontologyHistoryApi
        .history({ schema, limit: refreshLimit(shown), offset: 0 }, { signal: ctl.signal })
        .then((page) => {
          if (gen !== generation.current) return;
          if (!listRef.current) {
            // The schema had no history when the page opened, and now it has:
            // every version in it was saved while the reader watched.
            setState({ status: "ready", list: listFromPage(page), error: null, baseline: 0 });
            return;
          }
          beforeRef.current?.();
          setState((s) => (s.list ? { ...s, list: applyRefresh(s.list, page) } : s));
        })
        .catch(() => {
          // A failed re-read keeps the list: it is still right as of its own
          // read, and the next change will try again.
        });
    }, REFRESH_DEBOUNCE_MS);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [pulse, schema]);

  const loadOlder = useCallback(() => {
    const shown = listRef.current;
    if (!shown || !shown.hasMore || olderBusy.current) return;
    olderBusy.current = true;
    const gen = generation.current;
    setLoadingOlder(true);
    setOlderError(null);
    ontologyHistoryApi
      .history({ schema: shown.schema, limit: PAGE_SIZE, offset: olderOffset(shown) })
      .then((page) => {
        if (gen !== generation.current) return;
        setState((s) => (s.list ? { ...s, list: appendOlder(s.list, page) } : s));
      })
      .catch((err: unknown) => {
        if (gen === generation.current) setOlderError(message(err));
      })
      .finally(() => {
        if (gen !== generation.current) return;
        olderBusy.current = false;
        setLoadingOlder(false);
      });
  }, []);

  const reload = useCallback(() => setNonce((n) => n + 1), []);

  return { ...state, loadingOlder, olderError, loadOlder, reload };
}

// ---- the diff between two versions -------------------------------------------

/**
 * Diffs already read. A stored version is never rewritten, so the diff
 * between two of them never changes: a pair compared once is shown at once
 * the next time, and a live re-read of the list does not fetch it again.
 */
const diffCache = new Map<string, OntologyDiff>();
const DIFF_CACHE_MAX = 64;

function remember(key: string, diff: OntologyDiff) {
  diffCache.set(key, diff);
  if (diffCache.size > DIFF_CACHE_MAX) {
    const oldest = diffCache.keys().next().value;
    if (oldest !== undefined) diffCache.delete(oldest);
  }
}

export interface DiffState {
  status: "idle" | "loading" | "ready" | "error";
  diff: OntologyDiff | null;
  error: string | null;
  retry: () => void;
}

export function useVersionDiff(schema: string, pair: Pair | null): DiffState {
  const key = pair && schema ? `${schema}\u0000${pair.from}\u0000${pair.to}` : "";
  const [state, setState] = useState<{ key: string; status: DiffState["status"]; diff: OntologyDiff | null; error: string | null }>({
    key: "",
    status: "idle",
    diff: null,
    error: null,
  });
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    if (!key || !pair) return;
    const cached = diffCache.get(key);
    if (cached) {
      setState({ key, status: "ready", diff: cached, error: null });
      return;
    }
    setState({ key, status: "loading", diff: null, error: null });
    const ctl = new AbortController();
    ontologyHistoryApi
      .diff(schema, pair.from, pair.to, { signal: ctl.signal })
      .then((r) => {
        const diff = { changes: r.diff?.changes ?? [], has_breaking_changes: !!r.diff?.has_breaking_changes };
        remember(key, diff);
        setState({ key, status: "ready", diff, error: null });
      })
      .catch((err: unknown) => {
        if (!isAbort(err)) setState({ key, status: "error", diff: null, error: message(err) });
      });
    return () => ctl.abort();
    // pair is read through key, which names it exactly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, nonce]);

  const retry = useCallback(() => setNonce((n) => n + 1), []);
  if (!key) return { status: "idle", diff: null, error: null, retry };
  // Until the effect has caught up with a new pair, say it is loading rather
  // than show the last pair's diff under the new pair's heading.
  if (state.key !== key) {
    const cached = diffCache.get(key);
    return cached ? { status: "ready", diff: cached, error: null, retry } : { status: "loading", diff: null, error: null, retry };
  }
  return { status: state.status, diff: state.diff, error: state.error, retry };
}
