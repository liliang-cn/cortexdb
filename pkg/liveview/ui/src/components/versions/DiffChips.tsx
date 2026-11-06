// The words and colours of a schema diff: added, removed, changed, and
// breaking on top of any of them. Built from the change-op meanings in
// lib/semantics.ts rather than new colours, so "added" is the same green here
// as on the Changes page, and every chip carries its word and icon as well as
// its colour.

import { Minus, Pencil, Plus, TriangleAlert } from "lucide-react";
import { Chip } from "@/components/semantic";
import { CHANGE_OPS, type Semantic } from "@/lib/semantics";
import { DIFF_CLASS_ORDER, type DiffClass, type DiffCounts } from "@/lib/ontologyHistoryDiff";
import { cn } from "@/lib/utils";

export const DIFF_TONES: Record<DiffClass, Semantic> = {
  added: { ...CHANGE_OPS.added, key: "added", label: "Added", means: "In the newer version and not in the older.", icon: Plus },
  removed: { ...CHANGE_OPS.retracted, key: "removed", label: "Removed", means: "In the older version and gone from the newer.", icon: Minus },
  changed: { ...CHANGE_OPS.updated, key: "changed", label: "Changed", means: "In both versions, declared differently.", icon: Pencil },
};

export const BREAKING_TONE: Semantic = {
  key: "breaking",
  label: "Breaking",
  means: "Data written under the older version may no longer validate under the newer.",
  icon: TriangleAlert,
  color: "var(--destructive)",
  // Outlined where the three classes are filled, so a breaking removal does
  // not read as two chips of one red.
  chip: "border-destructive/45 bg-transparent text-destructive",
  dot: "bg-destructive",
  text: "text-destructive",
  rail: "border-destructive",
};

export function ClassChip({ cls, label, className }: { cls: DiffClass; label?: string; className?: string }) {
  return <Chip s={DIFF_TONES[cls]} label={label} className={className} />;
}

export function BreakingChip({ label, className }: { label?: string; className?: string }) {
  return <Chip s={BREAKING_TONE} label={label} className={className} />;
}

/** "2 added", "1 removed", "3 changed", "1 breaking": only the ones that are not zero. */
export function CountChips({ counts, className }: { counts: DiffCounts; className?: string }) {
  return (
    <span className={cn("inline-flex flex-wrap items-center gap-1", className)}>
      {DIFF_CLASS_ORDER.map((cls) =>
        counts[cls] > 0 ? <ClassChip key={cls} cls={cls} label={`${counts[cls]} ${DIFF_TONES[cls].label.toLowerCase()}`} /> : null,
      )}
      {counts.breaking > 0 && <BreakingChip label={`${counts.breaking} breaking`} />}
    </span>
  );
}
