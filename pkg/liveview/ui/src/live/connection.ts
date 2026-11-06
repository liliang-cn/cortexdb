// The page's one EventSource.
//
// startLive is called once, by the app root, and the stream outlives every
// route change. The browser reconnects an EventSource by itself and re-sends
// Last-Event-ID (the last change seq), which is what makes a reconnect resume;
// this module only reports that as "reconnecting" and never opens a second
// stream beside it. The one case the browser gives up on (a non-SSE answer, a
// 4xx) is retried here with backoff, with the cursor in ?after= because a new
// EventSource has no last event id of its own.

import { apiUrl, asOfParam } from "@/api";
import type { ActivityEvent, ChangeItem, Delta, ResetEvent, SnapshotPayload } from "@/api";
import { useLive } from "./store";

const SWEEP_MS = 1000;
const RETRY_MIN_MS = 1000;
const RETRY_MAX_MS = 15000;

type Store = typeof useLive;

export function streamUrl(asOf: number | null, cursor: number): URL {
  if (asOf) return apiUrl("api/stream", { as_of: asOfParam(asOf) });
  return apiUrl("api/stream", { after: cursor > 0 ? cursor : undefined });
}

export function startLive(store: Store = useLive): () => void {
  let es: EventSource | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let retries = 0;
  let stopped = false;
  let openedAsOf = store.getState().asOf;

  const parse = <T,>(ev: Event): T | null => {
    try {
      return JSON.parse((ev as MessageEvent<string>).data) as T;
    } catch (err) {
      console.warn("liveview: unreadable stream event", err);
      return null;
    }
  };

  const close = () => {
    clearTimeout(retryTimer);
    es?.close();
    es = null;
  };

  const open = () => {
    close();
    if (stopped) return;
    const s = store.getState();
    openedAsOf = s.asOf;
    s.setLink(s.ready ? "reconnecting" : "connecting");

    const source = new EventSource(streamUrl(s.asOf, s.cursor));
    es = source;
    source.onopen = () => {
      retries = 0;
      store.getState().setLink("open");
    };
    source.onerror = () => {
      if (source !== es) return;
      if (source.readyState === EventSource.CLOSED) {
        // The browser will not retry this one.
        source.close();
        es = null;
        store.getState().setLink("offline");
        const wait = Math.min(RETRY_MAX_MS, RETRY_MIN_MS * 2 ** retries++);
        retryTimer = setTimeout(open, wait);
      } else {
        store.getState().setLink("reconnecting");
      }
    };
    source.addEventListener("snapshot", (ev) => {
      const p = parse<SnapshotPayload>(ev);
      if (p) store.getState().onSnapshot(p);
    });
    source.addEventListener("delta", (ev) => {
      const d = parse<Delta>(ev);
      if (d) store.getState().onDelta(d);
    });
    source.addEventListener("activity", (ev) => {
      const e = parse<ActivityEvent>(ev);
      if (e) store.getState().onActivity(e);
    });
    source.addEventListener("change", (ev) => {
      const c = parse<ChangeItem>(ev);
      if (c) store.getState().onChange(c);
    });
    source.addEventListener("reset", (ev) => {
      const r = parse<ResetEvent>(ev);
      if (r) store.getState().onReset(r);
    });
    // "ping" only keeps the connection warm; the browser needs no help.
  };

  open();

  // Pinning or unpinning reconnects with the other query; nothing else does.
  const unsubscribe = store.subscribe((s) => {
    if (s.asOf !== openedAsOf) open();
  });
  const sweeper = setInterval(() => {
    if (Object.keys(store.getState().touched).length > 0) store.getState().sweep();
  }, SWEEP_MS);

  return () => {
    stopped = true;
    unsubscribe();
    clearInterval(sweeper);
    close();
  };
}
