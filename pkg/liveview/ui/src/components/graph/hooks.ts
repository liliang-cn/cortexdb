import { useEffect, useMemo, useState } from "react";
import { useLive } from "@/live";
import { mergedGraph, type Merged } from "./bind";
import { useGraphUI } from "./state";
import { useMode } from "@/lib/theme";

/** The core graph plus whatever exploring added, as the scene sees it. */
export function useMerged(): Merged {
  const nodes = useLive((s) => s.nodes);
  const edges = useLive((s) => s.edges);
  const explored = useGraphUI((s) => s.explored);
  // The inputs are the memo key; mergedGraph reads the same stores itself.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useMemo(() => mergedGraph(), [nodes, edges, explored]);
}

/** Whether the page is drawn dark, following the mode setting and the system. */
export function useDark(): boolean {
  const mode = useMode();
  const [system, setSystem] = useState(() => window.matchMedia("(prefers-color-scheme: dark)").matches);
  useEffect(() => {
    const m = window.matchMedia("(prefers-color-scheme: dark)");
    const on = () => setSystem(m.matches);
    m.addEventListener("change", on);
    return () => m.removeEventListener("change", on);
  }, []);
  return mode === "dark" || (mode === "auto" && system);
}
