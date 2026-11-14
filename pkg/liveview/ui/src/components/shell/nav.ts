import { Activity, ChartColumn, GitCommitHorizontal, History, Library, Network, ScanSearch, Shapes, type LucideIcon } from "lucide-react";
import { hiddenPages, pageShown } from "@/lib/pages";
import type { NavKey } from "@/lib/shellStrings";

export interface NavItem {
  /** The route, which is also the key of its label in SHELL_TEXT.nav. */
  to: NavKey;
  icon: LucideIcon;
  end?: boolean;
}

/**
 * The app's pages, in the groups the top bar separates: the brain as it is and
 * as it changes, then the views of it that each read one thing — the recall
 * log, the brain as a reading room, the declared model and its versions, and
 * the brain in numbers.
 */
export const NAV_GROUPS: NavItem[][] = [
  [
    { to: "/", icon: Network, end: true },
    { to: "/changes", icon: GitCommitHorizontal },
    { to: "/activity", icon: Activity },
  ],
  [
    { to: "/recalls", icon: ScanSearch },
    { to: "/library", icon: Library },
    { to: "/ontology", icon: Shapes },
    { to: "/ontology-history", icon: History },
    { to: "/analysis", icon: ChartColumn },
  ],
];

/** The groups with the pages this view leaves out taken away, and any group left empty by that. */
export function visibleNav(groups: NavItem[][] = NAV_GROUPS, hidden: ReadonlySet<string> = hiddenPages()): NavItem[][] {
  return groups.map((g) => g.filter((item) => pageShown(item.to, hidden))).filter((g) => g.length > 0);
}
