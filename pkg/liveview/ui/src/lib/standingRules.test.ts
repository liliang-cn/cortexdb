import { describe, expect, it } from "vitest";
import type { StandingRuleRow, StandingRulesView } from "@/api/recalls";
import { RULES_TEXT } from "./rulesStrings";
import {
  expandable,
  formatImportance,
  missingBadges,
  rowBadges,
  rowDate,
  rulesHeader,
  rulesProject,
  scopeLabel,
  splitRuleHits,
  statusBadge,
  withRulesProject,
} from "./standingRules";

const en = RULES_TEXT.en;
const zh = RULES_TEXT.zh;

const row = (over: Partial<StandingRuleRow> = {}): StandingRuleRow => ({
  id: "m1",
  content: "never 8080",
  importance: 0.9,
  recall_count: 0,
  node_id: "memory:m1",
  ...over,
});

describe("the Rules tab's URL", () => {
  it("reads and writes the session project under its own prefix", () => {
    expect(rulesProject(new URLSearchParams("project=%20athanor%20"))).toBe("athanor");
    expect(rulesProject(new URLSearchParams(""))).toBe("");
    expect(withRulesProject(new URLSearchParams("x=1"), "athanor").toString()).toBe("x=1&project=athanor");
    expect(withRulesProject(new URLSearchParams("project=a"), " ").toString()).toBe("");
  });
});

describe("a rule's badges", () => {
  it("name where a rule stands", () => {
    expect(statusBadge("injected", en)).toEqual({ label: "Injected", tone: "injected" });
    expect(statusBadge("over_budget", zh)).toEqual({ label: "超出预算", tone: "over" });
    expect(statusBadge("other_project", en)?.label).toBe("Other project");
    expect(statusBadge("later", en)).toEqual({ label: "later", tone: "other" });
    expect(statusBadge(undefined, en)).toBeNull();
  });

  it("name what a near miss lacks, with its importance", () => {
    expect(missingBadges(row({ missing: ["type"] }), en).map((b) => b.label)).toEqual(["no type"]);
    expect(missingBadges(row({ missing: ["importance"], importance: 0.3 }), en).map((b) => b.label)).toEqual(["importance 0.3"]);
    expect(missingBadges(row({ missing: ["importance"], importance: 0 }), zh).map((b) => b.label)).toEqual(["重要性 0"]);
    expect(rowBadges(row({ status: "injected" }), en).map((b) => b.tone)).toEqual(["injected"]);
    expect(rowBadges(row({ missing: ["type"] }), en).map((b) => b.tone)).toEqual(["missing"]);
  });

  it("print an importance with at most two decimals", () => {
    expect(formatImportance(0.85)).toBe("0.85");
    expect(formatImportance(0.3)).toBe("0.3");
    expect(formatImportance(0.799999)).toBe("0.8");
    expect(formatImportance(undefined)).toBe("0");
  });
});

describe("a rule row", () => {
  it("shows its scope with its project, and a date", () => {
    expect(scopeLabel(row(), en)).toBe("global");
    expect(scopeLabel(row({ project: "athanor" }), zh)).toBe("全局 · athanor");
    expect(rowDate(row({ date: "2026-11-01", created_at: "2026-10-01T00:00:00Z" }))).toBe("2026-11-01");
    expect(rowDate(row({ created_at: "2026-10-01T08:00:00Z" }))).toBe("2026-10-01");
    expect(rowDate(row())).toBe("");
  });

  it("offers expanding a long, multi-line or cut text", () => {
    expect(expandable(row())).toBe(false);
    expect(expandable(row({ content: "x".repeat(161) }))).toBe(true);
    expect(expandable(row({ content: "a\nb" }))).toBe(true);
    expect(expandable(row({ truncated: true }))).toBe(true);
  });
});

describe("the tab's header", () => {
  it("counts rules, injected, characters against the cap and near misses", () => {
    const v: StandingRulesView = {
      project: "",
      projects: [],
      rules: [row({ status: "injected" }), row({ id: "m2", status: "over_budget" })],
      injected: 1,
      chars: 120,
      max_rules: 16,
      max_chars: 2400,
      near_misses: [row({ id: "m3", missing: ["type"] })],
      near_miss_total: 140,
    };
    expect(rulesHeader(v)).toEqual({ rules: 2, injected: 1, chars: 120, maxChars: 2400, notQualifying: 140 });
    expect(rulesHeader({ ...v, near_miss_total: 0 }).notQualifying).toBe(1);
  });
});

describe("a recall's rules", () => {
  it("are the memory hits marked as rules, in the log's order", () => {
    const hits = [
      { kind: "memory", id: "a", rule: false },
      { kind: "memory", id: "b", rule: true },
      { kind: "knowledge", id: "c" },
      { kind: "memory", id: "d" },
      { kind: "memory", id: "e", rule: true },
    ];
    const { rules, others } = splitRuleHits(hits);
    expect(rules.map((h) => h.id)).toEqual(["b", "e"]);
    expect(others.map((h) => h.id)).toEqual(["a", "c", "d"]);
  });
});
