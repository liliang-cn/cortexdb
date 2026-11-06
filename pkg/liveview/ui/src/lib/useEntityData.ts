import { useEffect, useRef, useState } from "react";
import { ApiError, api, isAbort, type ChangeItem, type EntityView, type GraphHistory, type RecordDetail } from "@/api";
import { mergeChangeItems, useEntityChanges } from "@/live";

export interface EntityData {
  view: EntityView | null;
  history: GraphHistory | null;
  record: RecordDetail | null;
  /** Fetched change rows for this entity, newest first. */
  changes: ReadonlyArray<ChangeItem>;
  loading: boolean;
  /** A problem that leaves the page with nothing to show. */
  error: string | null;
  /** The server has no /api/entity. */
  unsupported: boolean;
}

const REFETCH_DEBOUNCE_MS = 400;

async function soft<T>(p: Promise<T>): Promise<T | null> {
  try {
    return await p;
  } catch (err) {
    if (isAbort(err)) throw err;
    return null;
  }
}

/**
 * Everything the entity page shows, fetched together. When a live change
 * touches the entity the fetch repeats (debounced, so a burst is one read) and
 * the old data stays on screen until the new arrives.
 */
export function useEntityData(id: string | null): EntityData {
  const [data, setData] = useState<EntityData>({ view: null, history: null, record: null, changes: [], loading: !!id, error: null, unsupported: false });
  const live = useEntityChanges(id);
  const newest = live[0]?.seq ?? 0;
  const loadedFor = useRef<string | null>(null);

  useEffect(() => {
    if (!id) return;
    const first = loadedFor.current !== id;
    const ctl = new AbortController();
    const run = async () => {
      if (first) setData({ view: null, history: null, record: null, changes: [], loading: true, error: null, unsupported: false });
      try {
        const o = { signal: ctl.signal };
        const [view, history, record, page] = await Promise.all([
          api.entity(id, o),
          soft(api.history({ entity: id }, o)),
          soft(api.record(id, o)),
          soft(api.changes({ entity: [id], limit: 20 }, o)),
        ]);
        loadedFor.current = id;
        setData({ view, history, record, changes: page?.items ?? [], loading: false, error: null, unsupported: false });
      } catch (err) {
        if (isAbort(err)) return;
        // The history is worth showing even when the live graph cannot describe the id.
        if (err instanceof ApiError && err.status === 404) {
          const [history, page] = await Promise.all([
            soft(api.history({ entity: id }, { signal: ctl.signal })),
            soft(api.changes({ entity: [id], limit: 20 }, { signal: ctl.signal })),
          ]);
          loadedFor.current = id;
          setData({ view: null, history, record: null, changes: page?.items ?? [], loading: false, error: null, unsupported: true });
          return;
        }
        setData((d) => ({ ...d, loading: false, error: err instanceof Error ? err.message : String(err) }));
      }
    };
    // The first read is immediate; reads caused by a live change wait a beat.
    const t = setTimeout(() => void run(), first ? 0 : REFETCH_DEBOUNCE_MS);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [id, newest]);

  return data;
}

/** The fetched and live change rows for one entity, joined. */
export function useRecentChanges(id: string | null, fetched: ReadonlyArray<ChangeItem>, limit = 15): ReadonlyArray<ChangeItem> {
  const live = useEntityChanges(id);
  return mergeChangeItems(fetched, live, limit);
}
