import { useMemo } from "react";
import { useLive } from "./store";
import type { Touch } from "./reducer";
import type { LinkKind } from "@/lib/semantics";
import { useLang } from "@/lib/i18n";
import { SHELL_TEXT } from "@/lib/shellStrings";
import { formatClock } from "@/lib/time";

export { useLive } from "./store";

/** All nodes, as an array that changes identity only when a node did. */
export function useNodes() {
  const nodes = useLive((s) => s.nodes);
  return useMemo(() => Array.from(nodes.values()), [nodes]);
}

export function useEdges() {
  const edges = useLive((s) => s.edges);
  return useMemo(() => Array.from(edges.values()), [edges]);
}

export function useNode(id: string | null | undefined) {
  return useLive((s) => (id ? s.nodes.get(id) : undefined));
}

export function useCounts() {
  const nodes = useLive((s) => s.nodes.size);
  const edges = useLive((s) => s.edges.size);
  return { nodes, edges };
}

export const useCapabilities = () => useLive((s) => s.caps);
export const useActivity = () => useLive((s) => s.activity);
export const useChanges = () => useLive((s) => s.changes);
export const useCursor = () => useLive((s) => s.cursor);

/** What recently happened to one node or edge, or undefined. Re-renders only for that id. */
export function useTouch(id: string | null | undefined): Touch | undefined {
  return useLive((s) => (id ? s.touched[id] : undefined));
}

export const useTouchedMap = () => useLive((s) => s.touched);

/** Changes that touched an entity, newest first. */
export function useEntityChanges(id: string | null | undefined) {
  const changes = useChanges();
  return useMemo(
    () => (id ? changes.filter((c) => c.id === id || c.entities.includes(id)) : []),
    [changes, id],
  );
}

export interface LinkView {
  kind: LinkKind;
  /** The pill's word. */
  label: string;
  /** Hover text: where the data comes from and what the state means. */
  detail: string;
}

/** The connection pill's state, derived once for the whole app. */
export function useLinkView(): LinkView {
  const link = useLive((s) => s.link);
  const ready = useLive((s) => s.ready);
  const pinned = useLive((s) => s.pinned);
  const pinnedAt = useLive((s) => s.pinnedAt);
  const pinReason = useLive((s) => s.pinReason);
  const source = useLive((s) => s.source);
  const activity = useLive((s) => s.caps.activity);
  const changes = useLive((s) => s.caps.changes);
  const lang = useLang();

  return useMemo(() => {
    const t = SHELL_TEXT[lang];
    const from = source ? t.source(source) : "";
    const and = (sentence: string) => `${sentence} ${from}`.trim();
    if (link === "offline") return { kind: "offline", label: t.offline, detail: and(t.offlineDetail) };
    if (!ready && link === "connecting") return { kind: "connecting", label: t.connecting, detail: t.connectingDetail };
    if (link === "reconnecting") {
      return { kind: "reconnecting", label: t.reconnecting, detail: and(t.reconnectingDetail) };
    }
    if (pinned) {
      return {
        kind: "pinned",
        label: t.pinnedTo(pinnedAt ? formatClock(lang, pinnedAt) : t.aPastInstant),
        detail: and(t.pinnedDetail),
      };
    }
    if (pinReason) {
      return { kind: "live", label: t.live, detail: and(t.pinRefused(pinReason)) };
    }
    if (ready && !activity && !changes) {
      return { kind: "structure", label: t.structureOnly, detail: and(t.structureDetail) };
    }
    return { kind: "live", label: t.live, detail: from || t.receiving };
  }, [lang, link, ready, pinned, pinnedAt, pinReason, source, activity, changes]);
}
