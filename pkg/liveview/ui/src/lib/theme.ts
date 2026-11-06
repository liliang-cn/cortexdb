// Light / dark / auto, stored under the key the old pages already used so a
// person's choice carries over. Unset means light; auto is only ever a choice.
// The inline script in index.html applies the same rule before first paint.

import { useSyncExternalStore } from "react";

export type Mode = "light" | "dark" | "auto";
export const MODE_KEY = "cortexdb.liveview.mode";

const isMode = (v: unknown): v is Mode => v === "light" || v === "dark" || v === "auto";

function readStored(): Mode {
  try {
    const v = localStorage.getItem(MODE_KEY);
    if (isMode(v)) return v;
  } catch {
    // storage can be blocked; the default stands
  }
  return "light";
}

/** ?mode= overrides the stored choice for this load only. */
function readOverride(): Mode | null {
  const v = new URLSearchParams(location.search).get("mode");
  return isMode(v) ? v : null;
}

let mode: Mode = readOverride() ?? readStored();
const listeners = new Set<() => void>();
const media = window.matchMedia("(prefers-color-scheme: dark)");

function resolved(m: Mode): "light" | "dark" {
  return m === "auto" ? (media.matches ? "dark" : "light") : m;
}

function apply() {
  document.documentElement.classList.toggle("dark", resolved(mode) === "dark");
}

media.addEventListener("change", () => {
  if (mode === "auto") apply();
});

export function setMode(next: Mode) {
  mode = next;
  try {
    localStorage.setItem(MODE_KEY, next);
  } catch {
    // not persisted; still applies for this load
  }
  apply();
  listeners.forEach((l) => l());
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useMode(): Mode {
  return useSyncExternalStore(subscribe, () => mode);
}

apply();
