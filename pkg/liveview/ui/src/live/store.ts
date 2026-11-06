import { create } from "zustand";
import type { ActivityEvent, ChangeItem, Delta, ResetEvent, SnapshotPayload } from "@/api";
import {
  applyActivity,
  applyChange,
  applyDelta,
  applyReset,
  applySnapshot,
  initialData,
  mergeChangeItems,
  pinTo,
  sweepTouched,
  type Link,
  type LiveData,
} from "./reducer";

export interface LiveActions {
  setLink: (link: Link) => void;
  onSnapshot: (p: SnapshotPayload, now?: number) => void;
  onDelta: (d: Delta, now?: number) => void;
  onActivity: (e: ActivityEvent) => void;
  onChange: (c: ChangeItem, now?: number) => void;
  onReset: (r: ResetEvent) => void;
  /** Drop "recently touched" marks older than their lifetime. */
  sweep: (now?: number) => void;
  /** Pin the whole view to an instant (unix ms); the connection reopens with ?as_of=. */
  pin: (asOf: number) => void;
  /** Go back to the live graph. */
  unpin: () => void;
  /** Splice change rows a page fetched itself into the live ring. */
  addChanges: (items: ReadonlyArray<ChangeItem>) => void;
}

export type LiveStore = LiveData & LiveActions;

/** ?as_of= on the page URL, as the old graph page accepted it (RFC 3339 or unix ms). */
export function initialAsOf(search: string): number | null {
  const raw = new URLSearchParams(search).get("as_of")?.trim().replaceAll(" ", "+");
  if (!raw) return null;
  const ms = /^\d{10,}$/.test(raw) ? Number(raw) : Date.parse(raw);
  return Number.isFinite(ms) ? ms : null;
}

export const useLive = create<LiveStore>()((set, get) => ({
  ...initialData(typeof location === "undefined" ? null : initialAsOf(location.search)),

  setLink: (link) => {
    if (get().link !== link) set({ link });
  },
  onSnapshot: (p, now = Date.now()) => set(applySnapshot(get(), p, now)),
  onDelta: (d, now = Date.now()) => set(applyDelta(get(), d, now)),
  onActivity: (e) => set(applyActivity(get(), e)),
  onChange: (c, now = Date.now()) => set(applyChange(get(), c, now)),
  onReset: (r) => set(applyReset(get(), r)),
  sweep: (now = Date.now()) => {
    const next = sweepTouched(get(), now);
    if (next !== get()) set({ touched: next.touched });
  },
  pin: (asOf) => set(pinTo(get(), asOf)),
  unpin: () => set(pinTo(get(), null)),
  addChanges: (items) => set({ changes: mergeChangeItems(get().changes, items) }),
}));
