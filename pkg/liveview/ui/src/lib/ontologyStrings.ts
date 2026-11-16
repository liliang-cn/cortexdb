// Everything the ontology page says, in English and Chinese: the page and its
// controls (OntologyText), and the labels lib/ontologyFinding and
// lib/ontologyLayout put beside the report's numbers (FindingText). The names
// the schema declares, its api names, enforcement values and tool names are
// data and are never translated.
//
// Every value here is a title, a label, a unit or a status word. A count is
// shown beside its label, never inside a sentence.

import type { Lang, Strings } from "@/lib/i18n";

const NUMS: Record<Lang, Intl.NumberFormat> = { en: new Intl.NumberFormat("en"), zh: new Intl.NumberFormat("zh-CN") };

/** A count in the language's digits. */
export const numIn = (lang: Lang, n: number | undefined | null) => NUMS[lang].format(Number(n || 0));

const one = (n: number | undefined | null) => Number(n || 0) === 1;
const nE = (n: number | undefined | null) => numIn("en", n);
const nZ = (n: number | undefined | null) => numIn("zh", n);
const pl = (n: number | undefined | null, single: string, many: string) => (one(n) ? single : many);

// ---- the page --------------------------------------------------------------

export interface OntologyText {
  title: string;
  reading: string;
  versions: string;

  // controls
  whatToDraw: string;
  gap: string;
  strays: string;
  draft: string;
  threshold: string;
  fromUrl: (t: string) => string;
  schema: string;
  chooseSchema: string;
  active: string;
  refresh: string;

  found: string;

  // the review half of a draft
  toDecide: string;
  howMany: string;
  /** How many a capped list left out. */
  more: (n: number) => string;
  against: string;
  breaking: string;

  // outside the model
  outside: string;
  noType: string;

  // the key
  key: string;
  keyOne: string;
  keyMany: string;
  keyFk: string;
  keyUndeclared: string;
  keyDashed: string;
  keyNotCountedMark: string;
  keyNotCounted: string;
  keyLive: string;
  keyIdle: string;
  keyLane: string;
  keyDots: string;

  // the detail panel
  objectType: string;
  linkType: string;
  iface: string;
  close: string;
  none: string;
  searchable: string;
  vectorized: string;
  required: string;
  nodes: string;
  edges: string;
  notCounted: string;
  primaryKey: string;
  titleProperty: string;
  implementsK: string;
  aliases: string;
  status: string;
  properties: (n: number) => string;
  fk: string;
  noFk: string;
  multiplicity: string;
  notDeclared: string;
  sideA: string;
  sideB: string;
  extendsK: string;
  implementedBy: (n: number) => string;
  sharedProps: (n: number) => string;

  // the diagram
  ifaceLabel: (name: string) => string;
  linkLabel: (name: string) => string;
  sideName: (side: string) => string;
  endTitle: (name: string, word: string, type: string, fk: string) => string;
  objectLabel: (name: string, count: string) => string;
  objectUnused: string;
  objectNodes: (count: string) => string;
  diagramLabel: (objects: number, lanes: number, links: number) => string;
  dangling: string;
}

export const ONTOLOGY_TEXT: Strings<OntologyText> = {
  en: {
    title: "Ontology",
    reading: "Reading…",
    versions: "Versions",

    whatToDraw: "What to draw",
    gap: "Declared vs actual",
    strays: "Undeclared",
    draft: "Draft",
    threshold: "Threshold",
    fromUrl: (t) => `${t} (URL)`,
    schema: "Schema",
    chooseSchema: "Schema",
    active: "active",
    refresh: "Refresh",

    found: "Summary",

    toDecide: "To decide",
    howMany: "Kinds",
    more: (n) => `+${nE(n)}`,
    against: "Against saved schema",
    breaking: "breaking",

    outside: "Outside the model",
    noType: "(no type)",

    key: "Key",
    keyOne: "One",
    keyMany: "Many",
    keyFk: "Foreign key side",
    keyUndeclared: "Cardinality not declared",
    keyDashed: "Declared, unused",
    keyNotCountedMark: "not counted",
    keyNotCounted: "Not counted",
    keyLive: "Link type in use",
    keyIdle: "Link type unused or not counted",
    keyLane: "Interface lane",
    keyDots: "Implemented interfaces",

    objectType: "Object type",
    linkType: "Link type",
    iface: "Interface",
    close: "Close",
    none: "none",
    searchable: "searchable",
    vectorized: "vectorized",
    required: "required",
    nodes: "Nodes",
    edges: "Edges",
    notCounted: "not counted",
    primaryKey: "Primary key",
    titleProperty: "Title property",
    implementsK: "Implements",
    aliases: "Aliases",
    status: "Status",
    properties: (n) => `Properties (${n})`,
    fk: "FK",
    noFk: "no FK",
    multiplicity: "Multiplicity",
    notDeclared: "not declared",
    sideA: "Side A",
    sideB: "Side B",
    extendsK: "Extends",
    implementedBy: (n) => `Implemented by (${n})`,
    sharedProps: (n) => `Shared properties (${n})`,

    ifaceLabel: (name) => `Interface ${name}`,
    linkLabel: (name) => `Link type ${name}`,
    sideName: (side) => `side ${side.toUpperCase()}`,
    endTitle: (name, word, type, fk) => `${name} → ${word} ${type}${fk ? ` · FK ${fk}` : ""}`,
    objectLabel: (name, count) => `Object type ${name}, ${count}`,
    objectUnused: "unused",
    objectNodes: (count) => `${count} nodes`,
    diagramLabel: (objects, lanes, links) => `Ontology: ${objects} object types, ${lanes} lanes, ${links} link types`,
    dangling: "Undeclared link ends",
  },
  zh: {
    title: "本体",
    reading: "读取中…",
    versions: "版本",

    whatToDraw: "绘制内容",
    gap: "声明与实际",
    strays: "未声明",
    draft: "草稿",
    threshold: "阈值",
    fromUrl: (t) => `${t}（URL）`,
    schema: "模式",
    chooseSchema: "模式",
    active: "生效中",
    refresh: "刷新",

    found: "概要",

    toDecide: "待决定",
    howMany: "类别",
    more: (n) => `+${nZ(n)}`,
    against: "对照已保存的模式",
    breaking: "破坏性",

    outside: "模型之外",
    noType: "（无类型）",

    key: "图例",
    keyOne: "一个",
    keyMany: "多个",
    keyFk: "外键端",
    keyUndeclared: "基数未声明",
    keyDashed: "已声明，未使用",
    keyNotCountedMark: "未统计",
    keyNotCounted: "未统计",
    keyLive: "在用的链接类型",
    keyIdle: "未使用或未统计的链接类型",
    keyLane: "接口泳道",
    keyDots: "实现的接口",

    objectType: "对象类型",
    linkType: "链接类型",
    iface: "接口",
    close: "关闭",
    none: "无",
    searchable: "可搜索",
    vectorized: "已向量化",
    required: "必填",
    nodes: "节点",
    edges: "边",
    notCounted: "未统计",
    primaryKey: "主键",
    titleProperty: "标题属性",
    implementsK: "实现",
    aliases: "别名",
    status: "状态",
    properties: (n) => `属性（${n}）`,
    fk: "外键",
    noFk: "无外键",
    multiplicity: "多重性",
    notDeclared: "未声明",
    sideA: "A 侧",
    sideB: "B 侧",
    extendsK: "扩展",
    implementedBy: (n) => `实现者（${n}）`,
    sharedProps: (n) => `共享属性（${n}）`,

    ifaceLabel: (name) => `接口 ${name}`,
    linkLabel: (name) => `链接类型 ${name}`,
    sideName: (side) => `${side.toUpperCase()} 侧`,
    endTitle: (name, word, type, fk) => `${name} → ${word} ${type}${fk ? ` · 外键 ${fk}` : ""}`,
    objectLabel: (name, count) => `对象类型 ${name}，${count}`,
    objectUnused: "未使用",
    objectNodes: (count) => `${count} 个节点`,
    diagramLabel: (objects, lanes, links) => `本体：${objects} 个对象类型，${lanes} 条泳道，${links} 个链接类型`,
    dangling: "未声明的链接端",
  },
};

// ---- the labels beside the report's numbers --------------------------------

export interface FindingText {
  // stat labels
  objectTypes: string;
  linkTypes: string;
  nodes: string;
  edges: string;
  nodeTypes: string;
  edgeTypes: string;
  unusedDeclarations: string;
  undeclaredTypes: string;
  toDecide: string;
  undeclaredNodes: string;
  undeclaredEdges: string;
  changes: string;
  breaking: string;

  // status words, one per state
  unreadable: string;
  absent: string;
  unused: string;
  uncounted: string;
  unknownState: string;
  undraftable: string;
  nothingToDraft: string;
  drafted: string;

  // label → value facts under the numbers
  gapLabel: string;
  off: string;
  countedLabel: string;
  counted: (counted: "store" | "graph") => string;
  returnedLabel: string;
  storeOwnLabel: string;
  thresholdLabel: string;
  prunedLabel: string;
  pruned: (nodeTypes: number, nodes: number, edgeTypes: number, edges: number) => string;
  missing: string;

  // the header
  noDraft: string;
  nothingToDraftFrom: string;
  draftPill: string;
  thresholdPill: (minNodes: number, minEdges: number) => string;
  vs: (against: string) => string;
  draftTitle: string;
  noAnswer: string;
  noSchema: string;
  active: string;
  inactive: string;
  strictActions: string;
  actions: (n: number) => string;
  objectSets: (n: number) => string;

  // the band outside the model
  nodeTypesGroup: string;
  edgeTypesGroup: string;
  more: (n: number) => string;

  // the drawing
  notCounted: string;
  unusedBox: string;
  implementsNothing: string;
  objectTypesCount: (n: number) => string;
  extendsList: (list: string) => string;
  sharedProps: (n: number) => string;
  implementors: (n: number) => string;
  one: string;
  many: string;
}

export const FINDING_TEXT: Strings<FindingText> = {
  en: {
    objectTypes: "Object types",
    linkTypes: "Link types",
    nodes: "Nodes",
    edges: "Edges",
    nodeTypes: "Node types",
    edgeTypes: "Edge types",
    unusedDeclarations: "Unused",
    undeclaredTypes: "Undeclared types",
    toDecide: "To decide",
    undeclaredNodes: "Undeclared nodes",
    undeclaredEdges: "Undeclared edges",
    changes: "Changes",
    breaking: "Breaking",

    unreadable: "Unreadable",
    absent: "No ontology",
    unused: "Unused",
    uncounted: "Not measured",
    unknownState: "Unknown state",
    undraftable: "Cannot draft",
    nothingToDraft: "Nothing to draft",
    drafted: "Not saved",

    gapLabel: "Declared vs actual",
    off: "off",
    countedLabel: "Counted over",
    counted: (counted) => (counted === "store" ? "store, chunks included" : "entity graph, chunks excluded"),
    returnedLabel: "Nodes returned",
    storeOwnLabel: "Store records",
    thresholdLabel: "Threshold",
    prunedLabel: "Not drawn",
    pruned: (nt, n, et, e) =>
      `${nE(nt)} node ${pl(nt, "type", "types")} (${nE(n)}) · ${nE(et)} edge ${pl(et, "type", "types")} (${nE(e)})`,
    missing: "—",

    noDraft: "no draft",
    nothingToDraftFrom: "nothing to draft",
    draftPill: "draft",
    thresholdPill: (minNodes, minEdges) => `min ${nE(minNodes)} / ${nE(minEdges)}`,
    vs: (against) => `vs ${against}`,
    draftTitle: "draft",
    noAnswer: "no answer",
    noSchema: "no schema",
    active: "active",
    inactive: "inactive",
    strictActions: "strict actions",
    actions: (n) => `${nE(n)} ${pl(n, "action", "actions")}`,
    objectSets: (n) => `${nE(n)} object ${pl(n, "set", "sets")}`,

    nodeTypesGroup: "node types",
    edgeTypesGroup: "edge types",
    more: (n) => `+${nE(n)}`,

    notCounted: "not counted",
    unusedBox: "unused",
    implementsNothing: "no interface",
    objectTypesCount: (n) => `${n} object ${pl(n, "type", "types")}`,
    extendsList: (list) => `extends ${list}`,
    sharedProps: (n) => `${n} shared properties`,
    implementors: (n) => `${n} implementors`,
    one: "one",
    many: "many",
  },
  zh: {
    objectTypes: "对象类型",
    linkTypes: "链接类型",
    nodes: "节点",
    edges: "边",
    nodeTypes: "节点类型",
    edgeTypes: "边类型",
    unusedDeclarations: "未使用",
    undeclaredTypes: "未声明类型",
    toDecide: "待决定",
    undeclaredNodes: "未声明节点",
    undeclaredEdges: "未声明边",
    changes: "变更",
    breaking: "破坏性",

    unreadable: "无法读取",
    absent: "无本体",
    unused: "未使用",
    uncounted: "未测量",
    unknownState: "未知状态",
    undraftable: "无法起草",
    nothingToDraft: "无可起草内容",
    drafted: "未保存",

    gapLabel: "声明与实际",
    off: "关闭",
    countedLabel: "统计范围",
    counted: (counted) => (counted === "store" ? "存储，含分块" : "实体图，不含分块"),
    returnedLabel: "返回节点",
    storeOwnLabel: "存储自身记录",
    thresholdLabel: "阈值",
    prunedLabel: "未绘制",
    pruned: (nt, n, et, e) => `${nZ(nt)} 个节点类型（${nZ(n)}）· ${nZ(et)} 个边类型（${nZ(e)}）`,
    missing: "—",

    noDraft: "无草稿",
    nothingToDraftFrom: "无可起草内容",
    draftPill: "草稿",
    thresholdPill: (minNodes, minEdges) => `最少 ${nZ(minNodes)} / ${nZ(minEdges)}`,
    vs: (against) => `对照 ${against}`,
    draftTitle: "草稿",
    noAnswer: "无回答",
    noSchema: "无模式",
    active: "生效中",
    inactive: "未生效",
    strictActions: "严格动作",
    actions: (n) => `${nZ(n)} 个动作`,
    objectSets: (n) => `${nZ(n)} 个对象集`,

    nodeTypesGroup: "节点类型",
    edgeTypesGroup: "边类型",
    more: (n) => `+${nZ(n)}`,

    notCounted: "未统计",
    unusedBox: "未使用",
    implementsNothing: "无接口",
    objectTypesCount: (n) => `${n} 个对象类型`,
    extendsList: (list) => `扩展 ${list}`,
    sharedProps: (n) => `${n} 个共享属性`,
    implementors: (n) => `${n} 个实现者`,
    one: "一个",
    many: "多个",
  },
};
