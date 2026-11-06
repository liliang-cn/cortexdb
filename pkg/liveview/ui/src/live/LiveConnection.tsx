import { useEffect } from "react";
import { startLive } from "./connection";

/**
 * Owns the page's single EventSource. Mount it once, at the app root, outside
 * the router's outlet, so navigating never closes or reopens the stream.
 */
export function LiveConnection() {
  useEffect(() => startLive(), []);
  return null;
}
