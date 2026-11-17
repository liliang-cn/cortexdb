// Pure helpers for the standing rules on the Recalls page: the Rules tab's
// URL parameters, its header fields, the badges a rule and a near miss wear,
// and a recall's hits split into its rules and the rest. Nothing here touches
// React or the network.

import type { RecallHit, StandingRuleRow, StandingRulesView } from "@/api/recalls";
import type { RulesText } from "@/lib/rulesStrings";

/**
 * The Rules tab's own query parameters sit under this prefix: the timeline's
 * ?project= filters recalls by who asked, the tab's picks the session the
 * injection is worked out for, and the two are not the same choice.
 */
export const RULES_PREFIX = "r_";

/** The session project the tab's URL names ("" for none), read from its scoped parameters. */
export function rulesProject(scoped: URLSearchParams): string {
  return (scoped.get("project") ?? "").trim();
}

/** The scoped parameters with the session project set, or taken off for "". */
export function withRulesProject(scoped: URLSearchParams, project: string): URLSearchParams {
  const next = new URLSearchParams(scoped);
  if (project.trim()) next.set("project", project.trim());
  else next.delete("project");
  return next;
}

/** An importance the way a badge shows it: at most two decimals, no trailing zeros. */
export function formatImportance(x: number | undefined | null): string {
  const n = typeof x === "number" && Number.isFinite(x) ? x : 0;
  return String(Math.round(n * 100) / 100);
}

export interface RulesHeader {
  rules: number;
  injected: number;
  chars: number;
  maxChars: number;
  notQualifying: number;
}

/** The tab's header fields: Rules N, Injected M, Characters X / max, Not qualifying K. */
export function rulesHeader(v: StandingRulesView): RulesHeader {
  return {
    rules: v.rules.length,
    injected: v.injected,
    chars: v.chars,
    maxChars: v.max_chars,
    notQualifying: v.near_miss_total || v.near_misses.length,
  };
}

export type BadgeTone = "injected" | "over" | "other" | "missing";

export interface RowBadge {
  label: string;
  tone: BadgeTone;
}

/** Where a rule stands, as its badge; an unknown status shows as itself. */
export function statusBadge(status: string | undefined, t: RulesText): RowBadge | null {
  switch (status) {
    case "injected":
      return { label: t.injected, tone: "injected" };
    case "over_budget":
      return { label: t.overBudget, tone: "over" };
    case "other_project":
      return { label: t.otherProject, tone: "other" };
    case undefined:
    case "":
      return null;
    default:
      return { label: status, tone: "other" };
  }
}

/** What a near miss lacks, one badge each: "no type", "importance 0.3". */
export function missingBadges(row: Pick<StandingRuleRow, "missing" | "importance">, t: RulesText): RowBadge[] {
  return (row.missing ?? []).map((m) => ({
    label: m === "type" ? t.noType : m === "importance" ? t.importanceIs(formatImportance(row.importance)) : m,
    tone: "missing" as const,
  }));
}

/** Every badge a row wears: its standing for a rule, what it lacks for a near miss. */
export function rowBadges(row: StandingRuleRow, t: RulesText): RowBadge[] {
  const s = statusBadge(row.status, t);
  return (s ? [s] : []).concat(missingBadges(row, t));
}

/** Scope and project as a row shows them: "global · athanor". */
export function scopeLabel(row: Pick<StandingRuleRow, "project">, t: RulesText): string {
  return [t.global, row.project].filter(Boolean).join(" · ");
}

/** The row's date: the memory's own, else the day it was saved; "" for neither. */
export function rowDate(row: Pick<StandingRuleRow, "date" | "created_at">): string {
  if (row.date) return row.date;
  return row.created_at ? row.created_at.slice(0, 10) : "";
}

/** Rows whose text is long enough, or cut, to offer expanding. */
export function expandable(row: Pick<StandingRuleRow, "content" | "truncated">, clampAt = 160): boolean {
  return Boolean(row.truncated) || row.content.length > clampAt || row.content.includes("\n");
}

/**
 * A recall's hits split into its standing rules and the rest, each in the
 * order the log has them. A hit is a rule only when it says so: a hit with no
 * mark is not guessed at.
 */
export function splitRuleHits<H extends Pick<RecallHit, "kind" | "rule">>(hits: ReadonlyArray<H>): { rules: H[]; others: H[] } {
  const rules: H[] = [];
  const others: H[] = [];
  for (const h of hits) (h.kind === "memory" && h.rule === true ? rules : others).push(h);
  return { rules, others };
}
