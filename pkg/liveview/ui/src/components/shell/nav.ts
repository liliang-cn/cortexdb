import { Activity, GitCommitHorizontal, History, Library, Network, ScanSearch, Shapes, type LucideIcon } from "lucide-react";
import { hiddenPages, pageShown } from "@/lib/pages";

export interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  end?: boolean;
}

/**
 * The app's pages, in the groups the top bar separates: the brain as it is and
 * as it changes, then the views of it that each read one thing — the recall
 * log, the brain as a reading room, the declared model and its versions.
 */
export const NAV_GROUPS: NavItem[][] = [
  [
    { to: "/", label: "Graph", icon: Network, end: true },
    { to: "/changes", label: "Changes", icon: GitCommitHorizontal },
    { to: "/activity", label: "Activity", icon: Activity },
  ],
  [
    { to: "/recalls", label: "Recalls", icon: ScanSearch },
    { to: "/library", label: "Library", icon: Library },
    { to: "/ontology", label: "Ontology", icon: Shapes },
    { to: "/ontology-history", label: "Versions", icon: History },
  ],
];

/** The groups with the pages this view leaves out taken away, and any group left empty by that. */
export function visibleNav(groups: NavItem[][] = NAV_GROUPS, hidden: ReadonlySet<string> = hiddenPages()): NavItem[][] {
  return groups.map((g) => g.filter((item) => pageShown(item.to, hidden))).filter((g) => g.length > 0);
}
