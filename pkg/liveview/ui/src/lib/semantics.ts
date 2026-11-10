// The one place that says what a colour and an icon mean.
//
// Every page (graph, changes, entity, activity) asks here for how to draw a
// change op, an activity kind or a knowledge-contract grade, so the same
// meaning always has the same colour, icon and word. The colours themselves are
// tokens in index.css; the class strings below are written out in full so
// Tailwind can see them.

import {
  Ban,
  CircleCheck,
  CircleDashed,
  CirclePause,
  CircleQuestionMark,
  GitMerge,
  Link2,
  PencilLine,
  Pencil,
  Plus,
  Quote,
  Replace,
  Search,
  ShieldCheck,
  Trash2,
  TriangleAlert,
  Undo2,
  type LucideIcon,
} from "lucide-react";
import type { ChangeOp } from "@/api";
import type { Lang, Strings } from "@/lib/i18n";

export interface Semantic {
  /** Stable key, the wire value. */
  key: string;
  /** Short label for chips and filters, in English; semLabel gives it in the app's language. */
  label: string;
  /** One sentence for tooltips and legends, in English; semMeans gives it in the app's language. */
  means: string;
  icon: LucideIcon;
  /** CSS variable holding the solid colour, for canvas, SVG and inline style. */
  color: string;
  /** Tinted chip: background plus readable text. */
  chip: string;
  /** Solid fill, for a dot or a bar. */
  dot: string;
  /** Text and icon colour on a plain surface. */
  text: string;
  /** Left rail or border accent. */
  rail: string;
}

function tone(
  key: string,
  v: string,
  label: string,
  means: string,
  icon: LucideIcon,
  c: { chip: string; dot: string; text: string; rail: string },
): Semantic {
  return { key, label, means, icon, color: `var(--${v})`, ...c };
}

// ---- change ops ---------------------------------------------------------

export const CHANGE_OPS: Record<ChangeOp, Semantic> = {
  added: tone("added", "op-added", "Added", "A new fact or record.", Plus, {
    chip: "bg-op-added-soft text-op-added-ink",
    dot: "bg-op-added",
    text: "text-op-added",
    rail: "border-op-added",
  }),
  updated: tone("updated", "op-updated", "Updated", "Rewritten in place.", Pencil, {
    chip: "bg-op-updated-soft text-op-updated-ink",
    dot: "bg-op-updated",
    text: "text-op-updated",
    rail: "border-op-updated",
  }),
  superseded: tone("superseded", "op-superseded", "Superseded", "Replaced by a newer value.", Replace, {
    chip: "bg-op-superseded-soft text-op-superseded-ink",
    dot: "bg-op-superseded",
    text: "text-op-superseded",
    rail: "border-op-superseded",
  }),
  retracted: tone("retracted", "op-retracted", "Retracted", "Taken back by its producer.", Undo2, {
    chip: "bg-op-retracted-soft text-op-retracted-ink",
    dot: "bg-op-retracted",
    text: "text-op-retracted",
    rail: "border-op-retracted",
  }),
  merged: tone("merged", "op-merged", "Merged", "Folded into another entity.", GitMerge, {
    chip: "bg-op-merged-soft text-op-merged-ink",
    dot: "bg-op-merged",
    text: "text-op-merged",
    rail: "border-op-merged",
  }),
  deleted: tone("deleted", "op-deleted", "Deleted", "Removed from the store.", Trash2, {
    chip: "bg-op-deleted-soft text-op-deleted-ink",
    dot: "bg-op-deleted",
    text: "text-op-deleted",
    rail: "border-op-deleted",
  }),
};

export const CHANGE_OP_ORDER: ChangeOp[] = ["added", "updated", "superseded", "retracted", "merged", "deleted"];

export function changeOp(op: string): Semantic {
  return CHANGE_OPS[op as ChangeOp] ?? CHANGE_OPS.updated;
}

// ---- activity kinds -----------------------------------------------------

export type ActivityTone = "query" | "write" | "relate" | "failed";

export const ACTIVITY_KINDS: Record<ActivityTone, Semantic> = {
  query: tone("query", "kind-query", "Query", "Read the brain; changed nothing.", Search, {
    chip: "bg-kind-query-soft text-kind-query-ink",
    dot: "bg-kind-query",
    text: "text-kind-query",
    rail: "border-kind-query",
  }),
  write: tone("write", "kind-write", "Write", "Saved a memory, fact or document.", PencilLine, {
    chip: "bg-kind-write-soft text-kind-write-ink",
    dot: "bg-kind-write",
    text: "text-kind-write",
    rail: "border-kind-write",
  }),
  relate: tone("relate", "kind-relate", "Relate", "Linked two things.", Link2, {
    chip: "bg-kind-relate-soft text-kind-relate-ink",
    dot: "bg-kind-relate",
    text: "text-kind-relate",
    rail: "border-kind-relate",
  }),
  failed: tone("failed", "kind-failed", "Failed", "The call returned an error.", TriangleAlert, {
    chip: "bg-kind-failed-soft text-kind-failed-ink",
    dot: "bg-kind-failed",
    text: "text-kind-failed",
    rail: "border-kind-failed",
  }),
};

export const ACTIVITY_ORDER: ActivityTone[] = ["query", "write", "relate", "failed"];

/** A failed call is a failure whatever its kind, so it wins. */
export function activityTone(e: { kind: string; failed?: boolean }): Semantic {
  if (e.failed) return ACTIVITY_KINDS.failed;
  return ACTIVITY_KINDS[e.kind as ActivityTone] ?? ACTIVITY_KINDS.query;
}

// ---- knowledge-contract grades ------------------------------------------

export type GradeKey = "verified" | "self_consistent" | "asserted" | "held" | "refused" | "untagged" | "unknown";

export const GRADES: Record<GradeKey, Semantic> = {
  verified: tone("verified", "grade-verified", "Verified", "Something outside the producer established it.", ShieldCheck, {
    chip: "bg-grade-verified-soft text-grade-verified-ink",
    dot: "bg-grade-verified",
    text: "text-grade-verified",
    rail: "border-grade-verified",
  }),
  self_consistent: tone(
    "self_consistent",
    "grade-self-consistent",
    "Self-consistent",
    "Coherent with what was already stated; unchecked against the world.",
    CircleCheck,
    {
      chip: "bg-grade-self-consistent-soft text-grade-self-consistent-ink",
      dot: "bg-grade-self-consistent",
      text: "text-grade-self-consistent",
      rail: "border-grade-self-consistent",
    },
  ),
  asserted: tone("asserted", "grade-asserted", "Asserted", "A source or a model said so and nothing has checked it.", Quote, {
    chip: "bg-grade-asserted-soft text-grade-asserted-ink",
    dot: "bg-grade-asserted",
    text: "text-grade-asserted",
    rail: "border-grade-asserted",
  }),
  held: tone("held", "grade-held", "Held", "Nothing yet; a person has to look.", CirclePause, {
    chip: "bg-grade-held-soft text-grade-held-ink",
    dot: "bg-grade-held",
    text: "text-grade-held",
    rail: "border-grade-held",
  }),
  refused: tone("refused", "grade-refused", "Refused", "The producer declined, and said why.", Ban, {
    chip: "bg-grade-refused-soft text-grade-refused-ink",
    dot: "bg-grade-refused",
    text: "text-grade-refused",
    rail: "border-grade-refused",
  }),
  untagged: tone("untagged", "grade-untagged", "Untagged", "No _grade at all; nothing says how it is known.", CircleDashed, {
    chip: "bg-grade-untagged-soft text-grade-untagged-ink",
    dot: "bg-grade-untagged",
    text: "text-muted-foreground",
    rail: "border-grade-untagged",
  }),
  unknown: tone("unknown", "grade-unknown", "Unknown", "A _grade the contract does not define; a producer's bug.", CircleQuestionMark, {
    chip: "bg-grade-unknown-soft text-grade-unknown-ink",
    dot: "bg-grade-unknown",
    text: "text-grade-unknown",
    rail: "border-grade-unknown",
  }),
};

/** The contract's ladder, best established first, then the two non-grades. */
export const GRADE_ORDER: GradeKey[] = [
  "verified",
  "self_consistent",
  "asserted",
  "held",
  "refused",
  "untagged",
  "unknown",
];

/** A record's grade: the contract's value, nothing at all, or somebody's bug. */
const CONTRACT_GRADES: ReadonlySet<string> = new Set(["verified", "self_consistent", "asserted", "held", "refused"]);

export function grade(value: string | undefined | null): Semantic {
  if (!value) return GRADES.untagged;
  return CONTRACT_GRADES.has(value) ? GRADES[value as GradeKey] : GRADES.unknown;
}

// ---- change kinds (what row a change touched) ---------------------------

export const CHANGE_KIND_LABELS: Strings<Record<string, string>> = {
  en: {
    node: "Entity",
    edge: "Relation",
    triple: "Triple",
    memory: "Memory",
    knowledge: "Document",
    ontology_schema: "Schema",
  },
  zh: {
    node: "实体",
    edge: "关系",
    triple: "三元组",
    memory: "记忆",
    knowledge: "文档",
    ontology_schema: "模式",
  },
};

/** The word for a change kind; a kind this build does not know is shown as it came. */
export function changeKindLabel(kind: string, lang: Lang): string {
  return CHANGE_KIND_LABELS[lang][kind] ?? kind;
}

// ---- the words, in both languages ---------------------------------------

type SemanticWords = Record<ChangeOp | ActivityTone | GradeKey, { label: string; means: string }>;

/** Every op, kind and grade's word and sentence, keyed by Semantic.key (the keys do not collide). */
export const SEMANTIC_TEXT: Strings<SemanticWords> = {
  en: Object.fromEntries(
    [...Object.values(CHANGE_OPS), ...Object.values(ACTIVITY_KINDS), ...Object.values(GRADES)].map((s) => [
      s.key,
      { label: s.label, means: s.means },
    ]),
  ) as SemanticWords,
  zh: {
    added: { label: "新增", means: "一条新的事实或记录。" },
    updated: { label: "更新", means: "原地改写。" },
    superseded: { label: "被取代", means: "被更新的值替换。" },
    retracted: { label: "撤回", means: "由写入方收回。" },
    merged: { label: "合并", means: "并入了另一个实体。" },
    deleted: { label: "删除", means: "已从存储中移除。" },
    query: { label: "查询", means: "读取大脑，没有改动任何东西。" },
    write: { label: "写入", means: "保存了一条记忆、事实或文档。" },
    relate: { label: "关联", means: "把两样东西连了起来。" },
    failed: { label: "失败", means: "调用返回了错误。" },
    verified: { label: "已核实", means: "写入方之外的来源证实了它。" },
    self_consistent: { label: "自洽", means: "与已有陈述一致，但未对照外部世界核查。" },
    asserted: { label: "断言", means: "来源或模型这么说，尚无任何核查。" },
    held: { label: "待定", means: "尚无结论，需要人来看。" },
    refused: { label: "拒绝", means: "写入方拒绝了，并说明了原因。" },
    untagged: { label: "未标注", means: "完全没有 _grade，无从得知它是怎么知道的。" },
    unknown: { label: "未知", means: "契约未定义的 _grade，属于写入方的 bug。" },
  },
};

/** A meaning's short word in the given language. */
export function semLabel(s: Semantic, lang: Lang): string {
  return (SEMANTIC_TEXT[lang] as Record<string, { label: string }>)[s.key]?.label ?? s.label;
}

/** A meaning's sentence in the given language. */
export function semMeans(s: Semantic, lang: Lang): string {
  return (SEMANTIC_TEXT[lang] as Record<string, { means: string }>)[s.key]?.means ?? s.means;
}

// ---- connection states --------------------------------------------------

export type LinkKind = "connecting" | "live" | "reconnecting" | "pinned" | "structure" | "offline";

export const LINK_TONES: Record<LinkKind, { dot: string; text: string; pulse: boolean }> = {
  connecting: { dot: "bg-link-wait", text: "text-link-wait", pulse: true },
  live: { dot: "bg-link-live", text: "text-link-live", pulse: true },
  reconnecting: { dot: "bg-link-wait", text: "text-link-wait", pulse: true },
  pinned: { dot: "bg-link-pinned", text: "text-link-pinned", pulse: false },
  structure: { dot: "bg-muted-foreground", text: "text-muted-foreground", pulse: false },
  offline: { dot: "bg-link-off", text: "text-link-off", pulse: false },
};
