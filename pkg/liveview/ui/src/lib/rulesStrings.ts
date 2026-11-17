// Everything the standing rules say on the Recalls page, in English and
// Chinese: the Rules tab and the rules group a recall opens with. Every value
// is a title, a label, a badge or a unit; a count sits beside its label, never
// inside a sentence. Ids, types and project names are data and are never
// translated.

import type { Strings } from "@/lib/i18n";

export interface RulesText {
  /** The tab, and the group a recall's rule hits are set apart in. */
  tab: string;
  /** A rule hit's badge. */
  rule: string;
  /** The badge of a recall whose rule marks were read from the memories now. */
  now: string;
  otherHits: string;

  // header fields
  rules: string;
  injected: string;
  characters: string;
  project: string;
  noProject: string;
  partial: string;

  // where a rule stands
  overBudget: string;
  otherProject: string;

  // near misses
  notQualifying: string;
  noType: string;
  /** "importance 0.3": what a near miss under the minimum carries. */
  importanceIs: (value: string) => string;
  /** How many a capped list left out. */
  more: (n: string) => string;

  // a row
  importance: string;
  scope: string;
  global: string;
  date: string;
  recalls: string;
  expand: string;
  collapse: string;
  open: string;

  // states
  loading: string;
  none: string;
  noneNear: string;
  readError: string;
  retry: string;
}

export const RULES_TEXT: Strings<RulesText> = {
  en: {
    tab: "Rules",
    rule: "Rule",
    now: "Current metadata",
    otherHits: "Other hits",

    rules: "Rules",
    injected: "Injected",
    characters: "Characters",
    project: "Project",
    noProject: "No project",
    partial: "Partial",

    overBudget: "Over budget",
    otherProject: "Other project",

    notQualifying: "Not qualifying",
    noType: "no type",
    importanceIs: (v) => `importance ${v}`,
    more: (n) => `+${n}`,

    importance: "Importance",
    scope: "Scope",
    global: "global",
    date: "Date",
    recalls: "Recalls",
    expand: "Expand",
    collapse: "Collapse",
    open: "Open",

    loading: "Loading rules",
    none: "No standing rules",
    noneNear: "None",
    readError: "Rules unreadable",
    retry: "Retry",
  },
  zh: {
    tab: "规则",
    rule: "规则",
    now: "当前元数据",
    otherHits: "其他命中",

    rules: "规则",
    injected: "注入",
    characters: "字符",
    project: "项目",
    noProject: "无项目",
    partial: "不完整",

    overBudget: "超出预算",
    otherProject: "其他项目",

    notQualifying: "未达标",
    noType: "无类型",
    importanceIs: (v) => `重要性 ${v}`,
    more: (n) => `+${n}`,

    importance: "重要性",
    scope: "范围",
    global: "全局",
    date: "日期",
    recalls: "召回",
    expand: "展开",
    collapse: "收起",
    open: "打开",

    loading: "正在读取规则",
    none: "没有常设规则",
    noneNear: "无",
    readError: "无法读取规则",
    retry: "重试",
  },
};
