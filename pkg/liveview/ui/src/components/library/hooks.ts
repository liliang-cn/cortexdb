// What the library page reads: the room, whatever is open in it, and whether
// the lights are down. The room is re-read when the live store says the
// snapshot moved on; the page never opens a stream of its own.

import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { isAbort } from "@/api";
import { libraryApi, type LibraryBookPage, type LibraryCard } from "@/api/library";
import { useLive } from "@/live";
import { shelve, type Opening, type Shelved } from "@/lib/library";

/** How long after the snapshot moves the room is read again: a burst of writes is one rebuild, not ten. */
const REREAD_MS = 1500;

/**
 * Whether the page is drawn dark, read from the class the theme puts on
 * <html>: that class is what the panels are drawn in, so following it keeps
 * the room and the panels in the same light whatever set it (the toggle,
 * ?mode=, or the system under "auto").
 */
export function useDarkClass(): boolean {
  return useSyncExternalStore(
    (cb) => {
      const mo = new MutationObserver(cb);
      mo.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
      return () => mo.disconnect();
    },
    () => document.documentElement.classList.contains("dark"),
    () => false,
  );
}

export interface LibraryState {
  lib: Shelved | null;
  error: string;
  reload: () => void;
}

/** The reading room, read once and again whenever the snapshot is at a newer version than the room was built from. */
export function useLibrary(): LibraryState {
  const [lib, setLib] = useState<Shelved | null>(null);
  const [error, setError] = useState("");
  const [asked, setAsked] = useState(0);
  const ready = useLive((s) => s.ready);
  const version = useLive((s) => s.version);
  // A view pinned to the past describes another instant; the room is the live one.
  const pinned = useLive((s) => s.pinned || s.asOf !== null);
  const current = useRef<number | null>(null);

  useEffect(() => {
    const ctl = new AbortController();
    libraryApi
      .library(ctl.signal)
      .then((raw) => {
        // The same version is the same room: keep the object, so nothing rebuilds.
        if (current.current === raw.version) return;
        current.current = raw.version;
        setLib(shelve(raw));
        setError("");
      })
      .catch((err) => {
        if (isAbort(err)) return;
        setError(err instanceof Error ? err.message : String(err));
      });
    return () => ctl.abort();
  }, [asked]);

  // Checked again after every reading too (lib is a dependency), so a write
  // that landed while the room was being read is not missed. Versions only
  // grow, so a room already as new as the stream is left alone.
  useEffect(() => {
    if (!ready || pinned || current.current === null || version <= current.current) return;
    const t = setTimeout(() => setAsked((n) => n + 1), REREAD_MS);
    return () => clearTimeout(t);
  }, [ready, pinned, version, lib]);

  const reload = useCallback(() => setAsked((n) => n + 1), []);
  return { lib, error, reload };
}

export type Opened =
  | { kind: "book"; id: string; book: LibraryBookPage }
  | { kind: "card"; id: string; card: LibraryCard };

export interface OpenState {
  /** What is open, once it has been read; it stays while a newer reading of it is on its way. */
  opened: Opened | null;
  loading: boolean;
  error: string;
}

/**
 * Reads what the URL opens. When the room is rebuilt around it (a newer
 * snapshot), it is read again, because a book's index and its neighbours come
 * from the snapshot too; the old reading stays up until the new one arrives.
 */
export function useOpened(opening: Opening, version: number | undefined): OpenState {
  const [state, setState] = useState<OpenState>({ opened: null, loading: false, error: "" });
  const kind = opening?.kind;
  const id = opening?.id;

  useEffect(() => {
    if (!kind || !id) {
      setState({ opened: null, loading: false, error: "" });
      return;
    }
    const ctl = new AbortController();
    setState((s) => ({ opened: s.opened && s.opened.kind === kind && s.opened.id === id ? s.opened : null, loading: true, error: "" }));
    const done = (opened: Opened) => setState({ opened, loading: false, error: "" });
    const fail = (err: unknown) => {
      if (isAbort(err)) return;
      setState({ opened: null, loading: false, error: err instanceof Error ? err.message : String(err) });
    };
    if (kind === "book") libraryApi.book(id, ctl.signal).then((book) => done({ kind, id, book }), fail);
    else libraryApi.card(id, ctl.signal).then((card) => done({ kind, id, card }), fail);
    return () => ctl.abort();
  }, [kind, id, version]);

  return state;
}
