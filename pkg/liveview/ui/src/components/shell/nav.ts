import { Activity, GitCommitHorizontal, Network, type LucideIcon } from "lucide-react";

/** Pages of the single-page app: client-side links. */
export const APP_NAV: { to: string; label: string; icon: LucideIcon; end?: boolean }[] = [
  { to: "/", label: "Graph", icon: Network, end: true },
  { to: "/changes", label: "Changes", icon: GitCommitHorizontal },
  { to: "/activity", label: "Activity", icon: Activity },
];

/**
 * Pages still served by Go, as full page loads. The hrefs are relative to the
 * document's <base>, so they keep working under a mount prefix.
 */
export const PAGE_LINKS: { href: string; label: string }[] = [
  { href: "ontology", label: "Ontology" },
  { href: "ontology-history", label: "Versions" },
  { href: "recalls", label: "Recalls" },
  { href: "library", label: "Library" },
];
