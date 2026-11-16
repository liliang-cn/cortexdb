import { Activity, ChartColumn, GitCommitHorizontal, Network, ScanSearch, Shapes, type LucideIcon } from "lucide-react";
import { hiddenPages, routeShown } from "@/lib/pages";
import type { NavKey } from "@/lib/shellStrings";

export interface NavItem {
  /** The route, which is also the key of its label in SHELL_TEXT.nav. */
  to: NavKey;
  icon: LucideIcon;
  end?: boolean;
}

/**
 * The app's pages, in the groups the top bar separates: the brain as it is and
 * as it changes (the graph with the library a view of it), then the views of it
 * that each read one thing — the recall log, the declared model (its versions a
 * tab of it), and the brain in numbers.
 */
export const NAV_GROUPS: NavItem[][] = [
  [
    { to: "/", icon: Network, end: true },
    { to: "/changes", icon: GitCommitHorizontal },
    { to: "/activity", icon: Activity },
  ],
  [
    { to: "/recalls", icon: ScanSearch },
    { to: "/ontology", icon: Shapes },
    { to: "/analysis", icon: ChartColumn },
  ],
];

/** The groups with the pages this view leaves out taken away, and any group left empty by that. */
export function visibleNav(groups: NavItem[][] = NAV_GROUPS, hidden: ReadonlySet<string> = hiddenPages()): NavItem[][] {
  return groups.map((g) => g.filter((item) => routeShown(item.to, hidden))).filter((g) => g.length > 0);
}
