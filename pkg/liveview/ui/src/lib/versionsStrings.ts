// Everything the ontology versions page says, in English and Chinese: the
// list of saved versions, the comparison of two, and the words for a schema
// diff. Schema ids, version numbers, writers' names and the server's change
// details are data and stay as they came.

import type { Lang, Strings } from "@/lib/i18n";

const one = (n: number) => n === 1;

export interface VersionsText {
  title: string;
  description: string;
  diagram: string;
  schema: string;
  active: string;
  notSavedNow: string;

  // where the schema stands
  versionsN: (n: number) => string;
  deleted: (count: string, n: number) => string;
  /** Around the latest version number, which is set apart. */
  latestIs: string;
  andActive: string;
  notValidating: string;
  noChangeEvents: string;

  // empty and failed states
  loadingVersions: string;
  tryAgain: string;
  listFailed: string;
  noOntologySource: string;
  noSchemaYet: string;
  noSchemaYetBody: string;
  noVersionsFor: (schema: string) => string;
  noVersionsBody: string;
  pickAnother: string;
  readFailed: string;

  // the list
  versionsLabel: string;
  comparison: string;
  justSaved: (version: number) => string;
  newVersions: (n: number) => string;
  arrived: (n: number, first: number, by: string) => string;
  loadingOlder: string;
  loadOlder: string;
  newestOf: (shown: number, total: number) => string;
  onlyVersion: string;
  everyVersion: (oldest: number) => string;

  // the phone's bar and sheet
  pickedOneMore: string;
  showDiff: string;
  clearPicks: string;
  to: string;
  sheetDescription: (schema: string) => string;

  // one version
  pickToCompare: (version: number) => string;
  pick: string;
  roles: { from: string; to: string; picked: string };
  latest: string;
  activeValidator: string;
  /** Before the writer's name, which is set apart. */
  savedBy: string;
  writerUnknown: string;
  renamedTo: string;
  strictActions: string;
  diffWith: (version: number) => string;
  firstVersion: string;
  unchanged: string;
  uncounted: string;
  unrecorded: string;
  neverActive: string;
  validatingSince: string;
  validated: string;
  until: string;
  now: string;
  switchedOnBy: (who: string) => string;
  ended: (why: string) => string;
  endedBy: (who: string) => string;
  endWords: Record<string, string>;

  // the comparison
  compare: string;
  pickTwo: string;
  pickTwoBody: string;
  /** After the picked version number, which is set apart. */
  pickedPickMore: string;
  withBefore: (version: number) => string;
  withLatest: (version: number) => string;
  clear: string;
  from: string;
  by: (who: string) => string;
  comparing: string;
  compareFailed: string;
  noDifference: string;
  changes: (n: number) => string;
  toTypes: (n: number) => string;
  breakingSentence: (from: number, to: number) => string;
  safeSentence: (from: number, to: number) => string;
  notCompared: string;
  objectType: string;
  linkType: string;
  noTypeDifference: string;
  couldNotCompare: string;
  andBreaking: (n: number) => string;

  // the diff's words
  added: string;
  addedMeans: string;
  removed: string;
  removedMeans: string;
  changed: string;
  changedMeans: string;
  breaking: string;
  breakingMeans: string;
  countOf: (n: number, word: string) => string;
  /** The part of a type a change touched, by the part's English name. */
  parts: Record<string, string>;
}

export const VERSIONS_TEXT: Strings<VersionsText> = {
  en: {
    title: "Ontology versions",
    description:
      "Every saved version of a schema: when and by whom it was saved, what it changed, and when it was the active validator. Pick two to see what changed between them.",
    diagram: "Diagram",
    schema: "Schema",
    active: "active",
    notSavedNow: "not saved now",

    versionsN: (n) => `${n} ${one(n) ? "version" : "versions"}`,
    deleted: (count, n) => `This schema was deleted. Its ${count} ${one(n) ? "is" : "are"} kept.`,
    latestIs: " is the latest",
    andActive: " and the active validator.",
    notValidating: "; the schema does not validate writes now.",
    noChangeEvents: " This source sends no change events, so versions saved from now on show after a reload.",

    loadingVersions: "Loading versions",
    tryAgain: "Try again",
    listFailed: "The schemas could not be listed",
    noOntologySource: "This source cannot be asked for its ontology",
    noSchemaYet: "No schema saved yet",
    noSchemaYetBody: "Save one with ontology_save and every save is listed here as a version: who saved it, what it changed, and when it validated writes.",
    noVersionsFor: (schema) => `No versions stored for “${schema}”`,
    noVersionsBody: "Each save of a schema with this id is listed here once it has one.",
    pickAnother: " Pick another schema above.",
    readFailed: "The versions could not be read",

    versionsLabel: "Versions",
    comparison: "Comparison",
    justSaved: (v) => `v${v} just saved`,
    newVersions: (n) => `${n} new versions`,
    arrived: (n, first, by) => `${one(n) ? `Version ${first} was` : `${n} versions were`} just saved${by ? ` by ${by}` : ""}.`,
    loadingOlder: "Loading older versions",
    loadOlder: "Load older versions",
    newestOf: (shown, total) => `The newest ${shown} of ${total}.`,
    onlyVersion: "That is the only version.",
    everyVersion: (oldest) => `That is every version, back to v${oldest}.`,

    pickedOneMore: " picked. Pick one more.",
    showDiff: "Show diff",
    clearPicks: "Clear the picks",
    to: "to",
    sheetDescription: (schema) => `What changed in ${schema} between the two versions.`,

    pickToCompare: (v) => `Pick v${v} to compare`,
    pick: "Pick",
    roles: { from: "From", to: "To", picked: "Picked" },
    latest: "Latest",
    activeValidator: "Active validator",
    savedBy: "Saved by ",
    writerUnknown: "Writer not recorded",
    renamedTo: " · renamed to ",
    strictActions: " · strict actions",
    diffWith: (v) => `Diff with v${v}`,
    firstVersion: "First version",
    unchanged: "Saved again, definition unchanged",
    uncounted: "Changed actions, interfaces or object sets only",
    unrecorded: "No change summary: saved before the history kept them",
    neverActive: "Never the active validator",
    validatingSince: "Validating since ",
    validated: "Validated ",
    until: " to ",
    now: "now",
    switchedOnBy: (who) => `, switched on by ${who}`,
    ended: (why) => `. Ended: ${why}`,
    endedBy: (who) => `, by ${who}`,
    endWords: {
      replaced: "replaced by a newer version",
      switched: "another schema was activated",
      deactivated: "deactivated",
      deleted: "the schema was deleted",
    },

    compare: "Compare",
    pickTwo: "Pick two versions",
    pickTwoBody: "Use Pick on any two versions in the list, or Diff on one to compare it with the version before it.",
    pickedPickMore: " picked. Pick one more to compare it with, or:",
    withBefore: (v) => `With v${v}, the one before`,
    withLatest: (v) => `With v${v}, the latest`,
    clear: "Clear",
    from: "From",
    by: (who) => `by ${who}`,
    comparing: "Comparing",
    compareFailed: "The two versions could not be compared",
    noDifference: "No difference in object types or link types.",
    changes: (n) => `${n} ${one(n) ? "change" : "changes"}`,
    toTypes: (n) => ` to ${n} ${one(n) ? "type" : "types"}.`,
    breakingSentence: (from, to) => `Data written under v${from} may no longer validate under v${to}.`,
    safeSentence: (from, to) => `Nothing written under v${from} stops validating under v${to}.`,
    notCompared: "Actions, interfaces and object sets are not compared: they describe how data is read and written, not the shape it has.",
    objectType: "object type",
    linkType: "link type",
    noTypeDifference: "No difference in types",
    couldNotCompare: "Could not compare",
    andBreaking: (n) => `, ${n} breaking`,

    added: "Added",
    addedMeans: "In the newer version and not in the older.",
    removed: "Removed",
    removedMeans: "In the older version and gone from the newer.",
    changed: "Changed",
    changedMeans: "In both versions, declared differently.",
    breaking: "Breaking",
    breakingMeans: "Data written under the older version may no longer validate under the newer.",
    countOf: (n, word) => `${n} ${word.toLowerCase()}`,
    parts: {},
  },
  zh: {
    title: "本体版本",
    description: "一个模式保存过的每个版本：何时、由谁保存，改了什么，以及何时作为生效的校验器。选两个版本即可查看它们之间的变化。",
    diagram: "图示",
    schema: "模式",
    active: "生效中",
    notSavedNow: "当前未保存",

    versionsN: (n) => `${n} 个版本`,
    deleted: (count) => `这个模式已被删除，它的 ${count}仍然保留。`,
    latestIs: " 是最新版本",
    andActive: "，也是生效的校验器。",
    notValidating: "；这个模式目前不校验写入。",
    noChangeEvents: "这个数据源不发送变更事件，所以之后保存的版本要重新加载才会显示。",

    loadingVersions: "正在加载版本",
    tryAgain: "重试",
    listFailed: "无法列出模式",
    noOntologySource: "无法向这个数据源询问本体",
    noSchemaYet: "还没有保存模式",
    noSchemaYetBody: "用 ontology_save 保存一个，之后每次保存都会作为一个版本列在这里：谁保存的、改了什么、何时校验写入。",
    noVersionsFor: (schema) => `“${schema}”没有存储任何版本`,
    noVersionsBody: "这个 id 的模式每保存一次，都会在这里列出。",
    pickAnother: "可以在上方选择另一个模式。",
    readFailed: "无法读取版本",

    versionsLabel: "版本",
    comparison: "对比",
    justSaved: (v) => `v${v} 刚刚保存`,
    newVersions: (n) => `${n} 个新版本`,
    arrived: (n, first, by) => `${one(n) ? `版本 ${first}` : `${n} 个版本`}刚刚${by ? `由 ${by} ` : ""}保存。`,
    loadingOlder: "正在加载更早的版本",
    loadOlder: "加载更早的版本",
    newestOf: (shown, total) => `共 ${total} 个，显示最新的 ${shown} 个。`,
    onlyVersion: "这是唯一的版本。",
    everyVersion: (oldest) => `这是全部版本，最早到 v${oldest}。`,

    pickedOneMore: " 已选。再选一个。",
    showDiff: "查看差异",
    clearPicks: "清除所选",
    to: "到",
    sheetDescription: (schema) => `${schema} 在这两个版本之间的变化。`,

    pickToCompare: (v) => `选择 v${v} 进行对比`,
    pick: "选择",
    roles: { from: "起点", to: "终点", picked: "已选" },
    latest: "最新",
    activeValidator: "生效的校验器",
    savedBy: "保存者 ",
    writerUnknown: "未记录写入者",
    renamedTo: " · 重命名为 ",
    strictActions: " · 严格动作",
    diffWith: (v) => `与 v${v} 对比`,
    firstVersion: "第一个版本",
    unchanged: "再次保存，定义未变",
    uncounted: "只改了动作、接口或对象集",
    unrecorded: "没有变更摘要：保存于历史开始记录之前",
    neverActive: "从未作为生效的校验器",
    validatingSince: "校验中，始于 ",
    validated: "校验于 ",
    until: " 至 ",
    now: "现在",
    switchedOnBy: (who) => `，由 ${who} 启用`,
    ended: (why) => `。结束原因：${why}`,
    endedBy: (who) => `，操作者 ${who}`,
    endWords: {
      replaced: "被更新的版本取代",
      switched: "启用了另一个模式",
      deactivated: "已停用",
      deleted: "模式已被删除",
    },

    compare: "对比",
    pickTwo: "选择两个版本",
    pickTwoBody: "在列表中对任意两个版本点“选择”，或对某个版本点“对比”，与它的上一个版本比较。",
    pickedPickMore: " 已选。再选一个来对比，或者：",
    withBefore: (v) => `与上一个 v${v} 对比`,
    withLatest: (v) => `与最新的 v${v} 对比`,
    clear: "清除",
    from: "起点",
    by: (who) => `由 ${who}`,
    comparing: "对比中",
    compareFailed: "无法对比这两个版本",
    noDifference: "对象类型和链接类型没有差异。",
    changes: (n) => `${n} 处变更`,
    toTypes: (n) => `，涉及 ${n} 个类型。`,
    breakingSentence: (from, to) => `在 v${from} 下写入的数据，在 v${to} 下可能无法通过校验。`,
    safeSentence: (from, to) => `在 v${from} 下写入的数据，在 v${to} 下都仍能通过校验。`,
    notCompared: "不对比动作、接口和对象集：它们描述的是数据如何读写，而不是数据的形状。",
    objectType: "对象类型",
    linkType: "链接类型",
    noTypeDifference: "类型没有差异",
    couldNotCompare: "无法对比",
    andBreaking: (n) => `，其中 ${n} 处破坏性`,

    added: "新增",
    addedMeans: "新版本有、旧版本没有。",
    removed: "移除",
    removedMeans: "旧版本有、新版本已去掉。",
    changed: "变更",
    changedMeans: "两个版本都有，但声明不同。",
    breaking: "破坏性",
    breakingMeans: "在旧版本下写入的数据，在新版本下可能无法通过校验。",
    countOf: (n, word) => `${word} ${n}`,
    parts: {
      "object type": "对象类型",
      "primary key": "主键",
      property: "属性",
      "required property": "必填属性",
      "property type": "属性类型",
      "link type": "链接类型",
      side: "侧",
    },
  },
};

/** A part's words in the language; one this build has no word for stays as the server spelled it. */
export function partIn(lang: Lang, part: string): string {
  return VERSIONS_TEXT[lang].parts[part] ?? part;
}
