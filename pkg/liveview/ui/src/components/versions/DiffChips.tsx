// The words and colours of a schema diff: added, removed, changed, and
// breaking on top of any of them. Built from the change-op meanings in
// lib/semantics.ts rather than new colours, so "added" is the same green here
// as on the Changes page, and every chip carries its word and icon as well as
// its colour.
//
// The words are the diff's own (lib/versionsStrings), not the change ops':
// "added" between two versions of a schema means something else than a new
// record. The keys are kept apart from the change ops' so a chip never picks
// up their sentence.

import { Minus, Pencil, Plus, TriangleAlert } from "lucide-react";
import { Chip } from "@/components/semantic";
import { useLang, type Lang } from "@/lib/i18n";
import { CHANGE_OPS, type Semantic } from "@/lib/semantics";
import { DIFF_CLASS_ORDER, type DiffClass, type DiffCounts } from "@/lib/ontologyHistoryDiff";
import { VERSIONS_TEXT } from "@/lib/versionsStrings";
import { cn } from "@/lib/utils";

const TONES: Record<Lang, Record<DiffClass, Semantic>> = {
  en: tonesIn("en"),
  zh: tonesIn("zh"),
};

function tonesIn(lang: Lang): Record<DiffClass, Semantic> {
  const t = VERSIONS_TEXT[lang];
  return {
    added: { ...CHANGE_OPS.added, key: "diff_added", label: t.added, means: t.addedMeans, icon: Plus },
    removed: { ...CHANGE_OPS.retracted, key: "diff_removed", label: t.removed, means: t.removedMeans, icon: Minus },
    changed: { ...CHANGE_OPS.updated, key: "diff_changed", label: t.changed, means: t.changedMeans, icon: Pencil },
  };
}

/** The three classes' colours and words, in English; diffTones gives them in a language. */
export const DIFF_TONES: Record<DiffClass, Semantic> = TONES.en;

export const diffTones = (lang: Lang): Record<DiffClass, Semantic> => TONES[lang];

function breakingIn(lang: Lang): Semantic {
  return {
    key: "diff_breaking",
    label: VERSIONS_TEXT[lang].breaking,
    means: VERSIONS_TEXT[lang].breakingMeans,
    icon: TriangleAlert,
    color: "var(--destructive)",
    // Outlined where the three classes are filled, so a breaking removal does
    // not read as two chips of one red.
    chip: "border-destructive/45 bg-transparent text-destructive",
    dot: "bg-destructive",
    text: "text-destructive",
    rail: "border-destructive",
  };
}

const BREAKING: Record<Lang, Semantic> = { en: breakingIn("en"), zh: breakingIn("zh") };

export const BREAKING_TONE: Semantic = BREAKING.en;

export function ClassChip({ cls, label, className }: { cls: DiffClass; label?: string; className?: string }) {
  return <Chip s={TONES[useLang()][cls]} label={label} className={className} />;
}

export function BreakingChip({ label, className }: { label?: string; className?: string }) {
  return <Chip s={BREAKING[useLang()]} label={label} className={className} />;
}

/** "2 added", "1 removed", "3 changed", "1 breaking": only the ones that are not zero. */
export function CountChips({ counts, className }: { counts: DiffCounts; className?: string }) {
  const lang = useLang();
  const t = VERSIONS_TEXT[lang];
  return (
    <span className={cn("inline-flex flex-wrap items-center gap-1", className)}>
      {DIFF_CLASS_ORDER.map((cls) =>
        counts[cls] > 0 ? <ClassChip key={cls} cls={cls} label={t.countOf(counts[cls], TONES[lang][cls].label)} /> : null,
      )}
      {counts.breaking > 0 && <BreakingChip label={t.countOf(counts.breaking, t.breaking)} />}
    </span>
  );
}
