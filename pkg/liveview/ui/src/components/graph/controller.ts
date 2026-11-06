// The one live scene, reachable from actions that are not React components
// (a search result, a rail row, a message from the page that frames this one).

import type { GraphScene } from "@/lib/graph/scene";

let current: GraphScene | null = null;

export const setScene = (s: GraphScene | null) => {
  current = s;
};
export const getScene = () => current;
