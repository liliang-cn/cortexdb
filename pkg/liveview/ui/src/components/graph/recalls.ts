import { getJSON } from "@/api";

/** How often recalls reached each node, from the recall log. */
export interface RecallHeat {
  nodes: Record<string, number>;
  max: number;
}

export const loadRecallHeat = (signal?: AbortSignal) => getJSON<RecallHeat>("api/recalls/heat", undefined, signal);
