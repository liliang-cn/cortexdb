import { useEffect, useState } from "react";
import { isAbort } from "@/api";
import { useLive } from "@/live";
import { loadRecallHeat, type RecallHeat } from "./recalls";
import { getScene } from "./controller";

let cache: { data: RecallHeat | null; error: string } = { data: null, error: "" };

/** The recall heat for "colour by recalls", read once when that mode is first chosen and again on a reconnect. */
export function useRecallHeat(enabled: boolean): { data: RecallHeat | null; error: string } {
  const [state, setState] = useState(cache);
  const resets = useLive((s) => s.resets);
  useEffect(() => {
    if (!enabled) return;
    const ctl = new AbortController();
    loadRecallHeat(ctl.signal)
      .then((data) => {
        cache = { data: { nodes: data.nodes ?? {}, max: data.max ?? 0 }, error: "" };
        setState(cache);
        getScene()?.setHeat(cache.data!);
      })
      .catch((err) => {
        if (isAbort(err)) return;
        cache = { data: { nodes: {}, max: 0 }, error: err instanceof Error ? err.message : String(err) };
        setState(cache);
      });
    return () => ctl.abort();
  }, [enabled, resets]);
  return state;
}
