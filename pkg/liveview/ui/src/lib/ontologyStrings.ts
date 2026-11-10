// Everything the ontology page says, in English and Chinese: the page and its
// controls (OntologyText), and the sentences lib/ontologyFinding and
// lib/ontologyLayout build from the report (FindingText). The names the
// schema declares, its api names, enforcement values and tool names are data
// and are never translated.
//
// A sentence that sets something off (a number, a name the reader acts on) is
// returned as runs, the way lib/ontologyFinding returns it, so each language
// keeps its own word order around the set-off pieces.

import type { Lang, Strings } from "@/lib/i18n";
import type { Run } from "@/lib/ontologyFinding";

const NUMS: Record<Lang, Intl.NumberFormat> = { en: new Intl.NumberFormat("en"), zh: new Intl.NumberFormat("zh-CN") };

/** A count in the language's digits. */
export const numIn = (lang: Lang, n: number | undefined | null) => NUMS[lang].format(Number(n || 0));

const one = (n: number | undefined | null) => Number(n || 0) === 1;

/** A set-off run; a number is written in the language's digits. */
const bIn = (lang: Lang) => (s: string | number): Run => ({ b: typeof s === "number" ? numIn(lang, s) : s });
const bE = bIn("en");
const bZ = bIn("zh");
const nE = (n: number | undefined | null) => numIn("en", n);
const nZ = (n: number | undefined | null) => numIn("zh", n);
const pl = (n: number | undefined | null, single: string, many: string) => (one(n) ? single : many);

// ---- the page --------------------------------------------------------------

export interface OntologyText {
  title: string;
  reading: string;
  versions: string;
  versionsTip: string;

  // controls
  whatToDraw: string;
  gap: string;
  gapTipDraft: string;
  gapTip: string;
  strays: string;
  straysTip: string;
  draft: string;
  draftTip: string;
  threshold: string;
  thresholdLabel: string;
  fromUrl: (t: string) => string;
  schema: string;
  chooseSchema: string;
  active: string;
  refresh: string;
  refreshLabel: string;

  found: string;

  // the review half of a draft
  toDecide: string;
  /** Before and after the tool name, which is set apart. */
  toDecideLead: [string, string];
  howMany: string;
  moreOfKind: (n: number) => string;
  against: string;
  breakingSr: string;
  moreNotListed: (n: number) => string;

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
  noProps: string;
  searchable: string;
  vectorized: string;
  required: string;
  inStore: string;
  nodesCarry: (n: number) => string;
  noNodes: string;
  notCountedGap: string;
  primaryKey: string;
  titleProperty: string;
  implementsK: string;
  aliases: string;
  status: string;
  properties: (n: number) => string;
  reaches: string;
  carriesFk: string;
  oneNoFk: string;
  multiplicity: string;
  notBothSides: string;
  edgesCarry: (n: number) => string;
  noEdges: string;
  sideA: string;
  sideB: string;
  extendsK: string;
  implementedBy: (n: number) => string;
  nothingImplements: string;
  sharedProps: (n: number) => string;

  // the diagram
  ifaceLabel: (name: string) => string;
  linkLabel: (name: string) => string;
  multNotDeclared: string;
  sideName: (side: string) => string;
  endTitle: (name: string, word: string, type: string, fk: string) => string;
  objectLabel: (name: string, count: string) => string;
  objectNotCounted: string;
  objectUnused: string;
  objectNodes: (count: string) => string;
  notCounted: string;
  diagramLabel: (objects: number, lanes: number, links: number) => string;
  dangling: (n: number, names: string) => string;
}

export const ONTOLOGY_TEXT: Strings<OntologyText> = {
  en: {
    title: "Ontology",
    reading: "Reading the ontology…",
    versions: "Versions",
    versionsTip: "Every saved version of this schema, when it was in effect, and what changed between them",

    whatToDraw: "What to draw",
    gap: "Declared vs actual",
    gapTipDraft: "A draft is derived from the store's own counts, so the overlay is already taken.",
    gapTip: "Count what the store actually holds and hold the declarations against it. Off reads nothing about the data.",
    strays: "Undeclared",
    straysTip: "Show the types the store uses that no schema declares.",
    draft: "Draft",
    draftTip: "Derive an ontology from what this store already holds. Nothing is saved: this page reviews, and a person saves, through ontology_save.",
    threshold: "Threshold",
    thresholdLabel: "Keep types with fewer records than this out of the drawing",
    fromUrl: (t) => `${t} (from the URL)`,
    schema: "Schema",
    chooseSchema: "Choose a schema",
    active: "active",
    refresh: "Refresh",
    refreshLabel: "Read it again now",

    found: "What this page found",

    toDecide: "To decide",
    toDecideLead: [
      "The half of a draft that is not a picture. Nothing here is decided by this page or by the deriver that raised it — a schema is saved by a person, through ",
      ".",
    ],
    howMany: "How many of each kind",
    moreOfKind: (n) => `and ${nE(n)} more of this kind, not listed`,
    against: "Against the saved schema",
    breakingSr: " (breaking)",
    moreNotListed: (n) => `and ${nE(n)} more, not listed`,

    outside: "Outside the model",
    noType: "(no type at all)",

    key: "Key",
    keyOne: "This end reaches exactly one object, and it is the end that carries the foreign key.",
    keyMany: "This end reaches many.",
    keyFk: "A foreign key property is declared on this side.",
    keyUndeclared: "The schema leaves this side's cardinality undeclared.",
    keyDashed: "Dashed box: declared, and nothing in the store is of this type.",
    keyNotCountedMark: "not counted",
    keyNotCounted: "The gap was not measured, so the box has no number, not even zero.",
    keyLive: "A link type edges in the store carry.",
    keyIdle: "A link type nothing uses, or one whose count was not taken.",
    keyLane: "A lane groups the object types implementing one interface.",
    keyDots: "Dots down a box's edge: the interfaces it implements, in their lanes' colours.",

    objectType: "Object type",
    linkType: "Link type",
    iface: "Interface",
    close: "Close",
    noProps: "no properties declared",
    searchable: "searchable",
    vectorized: "vectorized",
    required: "required",
    inStore: "In the store",
    nodesCarry: (n) => `${nE(n)} ${pl(n, "node carries", "nodes carry")} this type`,
    noNodes: "nothing in the store is of this type",
    notCountedGap: "not counted — the gap was not measured",
    primaryKey: "Primary key",
    titleProperty: "Title property",
    implementsK: "Implements",
    aliases: "Aliases",
    status: "Status",
    properties: (n) => `Properties (${n})`,
    reaches: "reaches",
    carriesFk: "carries the foreign key:",
    oneNoFk: "single-valued, but no foreign key property is declared",
    multiplicity: "Multiplicity",
    notBothSides: "not declared on both sides",
    edgesCarry: (n) => `${nE(n)} ${pl(n, "edge carries", "edges carry")} this type`,
    noEdges: "no edge in the store carries this type",
    sideA: "Side A",
    sideB: "Side B",
    extendsK: "Extends",
    implementedBy: (n) => `Implemented by (${n})`,
    nothingImplements: "nothing implements this interface",
    sharedProps: (n) => `Shared properties (${n})`,

    ifaceLabel: (name) => `Interface ${name}: light its implementors`,
    linkLabel: (name) => `Link type ${name}`,
    multNotDeclared: "multiplicity not declared on both sides",
    sideName: (side) => `side ${side.toUpperCase()}`,
    endTitle: (name, word, type, fk) => `${name}: reaches ${word} ${type}${fk ? `; carries the foreign key ${fk}` : ""}`,
    objectLabel: (name, count) => `Object type ${name}, ${count}`,
    objectNotCounted: "not counted",
    objectUnused: "declared, and nothing in the store is of this type",
    objectNodes: (count) => `${count} nodes`,
    notCounted: "not counted",
    diagramLabel: (objects, lanes, links) => `The ontology as lanes: ${objects} object types in ${lanes} lanes, ${links} link types`,
    dangling: (n, names) =>
      `${n === 1 ? "One link type names" : `${n} link types name`} a type this schema does not declare, so there is no box to draw it between: ${names}.`,
  },
  zh: {
    title: "本体",
    reading: "正在读取本体…",
    versions: "版本",
    versionsTip: "这个模式保存过的每个版本、各自生效的时间，以及版本之间改了什么",

    whatToDraw: "绘制内容",
    gap: "声明与实际",
    gapTipDraft: "草稿本身就是从存储的计数推导出来的，叠加对比已经包含在内。",
    gapTip: "统计存储里实际有什么，并拿声明去对照。关闭时不读取任何数据。",
    strays: "未声明",
    straysTip: "显示存储在用、但没有任何模式声明的类型。",
    draft: "草稿",
    draftTip: "从这个存储已有的数据推导出一个本体。不会保存任何东西：本页只负责审阅，保存要由人通过 ontology_save 完成。",
    threshold: "阈值",
    thresholdLabel: "记录数少于此值的类型不画进图里",
    fromUrl: (t) => `${t}（来自 URL）`,
    schema: "模式",
    chooseSchema: "选择一个模式",
    active: "生效中",
    refresh: "刷新",
    refreshLabel: "立即重新读取",

    found: "本页的发现",

    toDecide: "待决定",
    toDecideLead: ["这是草稿中不是图的那一半。这里的问题既不由本页决定，也不由提出它的推导器决定——模式要由人通过 ", " 保存。"],
    howMany: "每类各有多少",
    moreOfKind: (n) => `这一类还有 ${nZ(n)} 条未列出`,
    against: "对照已保存的模式",
    breakingSr: "（破坏性）",
    moreNotListed: (n) => `还有 ${nZ(n)} 条未列出`,

    outside: "模型之外",
    noType: "（完全没有类型）",

    key: "图例",
    keyOne: "这一端恰好指向一个对象，并且是持有外键的那一端。",
    keyMany: "这一端指向多个。",
    keyFk: "这一侧声明了外键属性。",
    keyUndeclared: "模式没有声明这一侧的基数。",
    keyDashed: "虚线框：已声明，但存储中没有这个类型的任何东西。",
    keyNotCountedMark: "未统计",
    keyNotCounted: "没有测量差距，所以框上没有数字，连 0 也没有。",
    keyLive: "存储中有边在用的链接类型。",
    keyIdle: "没有被使用的链接类型，或者没有统计数量的链接类型。",
    keyLane: "一条泳道把实现同一个接口的对象类型归在一起。",
    keyDots: "框边上的圆点：它实现的接口，颜色与对应泳道一致。",

    objectType: "对象类型",
    linkType: "链接类型",
    iface: "接口",
    close: "关闭",
    noProps: "没有声明属性",
    searchable: "可搜索",
    vectorized: "已向量化",
    required: "必填",
    inStore: "存储中",
    nodesCarry: (n) => `${nZ(n)} 个节点属于这个类型`,
    noNodes: "存储中没有这个类型的任何东西",
    notCountedGap: "未统计——没有测量差距",
    primaryKey: "主键",
    titleProperty: "标题属性",
    implementsK: "实现",
    aliases: "别名",
    status: "状态",
    properties: (n) => `属性（${n}）`,
    reaches: "指向",
    carriesFk: "持有外键：",
    oneNoFk: "单值，但没有声明外键属性",
    multiplicity: "多重性",
    notBothSides: "两侧没有都声明",
    edgesCarry: (n) => `${nZ(n)} 条边属于这个类型`,
    noEdges: "存储中没有边属于这个类型",
    sideA: "A 侧",
    sideB: "B 侧",
    extendsK: "扩展",
    implementedBy: (n) => `实现者（${n}）`,
    nothingImplements: "没有任何类型实现这个接口",
    sharedProps: (n) => `共享属性（${n}）`,

    ifaceLabel: (name) => `接口 ${name}：高亮它的实现者`,
    linkLabel: (name) => `链接类型 ${name}`,
    multNotDeclared: "两侧没有都声明多重性",
    sideName: (side) => `${side.toUpperCase()} 侧`,
    endTitle: (name, word, type, fk) => `${name}：指向${word} ${type}${fk ? `；持有外键 ${fk}` : ""}`,
    objectLabel: (name, count) => `对象类型 ${name}，${count}`,
    objectNotCounted: "未统计",
    objectUnused: "已声明，但存储中没有这个类型的任何东西",
    objectNodes: (count) => `${count} 个节点`,
    notCounted: "未统计",
    diagramLabel: (objects, lanes, links) => `以泳道表示的本体：${objects} 个对象类型分布在 ${lanes} 条泳道中，${links} 个链接类型`,
    dangling: (n, names) => `${n === 1 ? "有 1 个链接类型" : `有 ${n} 个链接类型`}引用了这个模式未声明的类型，所以画不出连接它的框：${names}。`,
  },
};

// ---- the sentences built from the report ----------------------------------

export interface FindingText {
  noReason: string;
  noState: string;

  unreadable: Run[];
  absent: Run[];
  absentUsage: (nodes: number, nodeTypes: number, edges: number, edgeTypes: number) => Run[];
  unused: (objects: number, links: number) => Run[];
  live: (objects: number, links: number, nodes: number, edges: number) => Run[];
  liveUnused: (n: number) => Run[];
  liveStrays: (n: number) => Run[];
  liveClean: Run[];
  liveUncounted: (objects: number, links: number) => Run[];
  gapOff: string;
  /** What a reading counted, worded from the server's values. */
  scope: (counted: "store" | "graph", returned?: number, total?: number) => string;
  unknownState: Run[];

  undraftable: Run[];
  nothingToDraft: Run[];
  unknownDraftState: Run[];
  drafted: (objects: number, links: number, nodes: number, edges: number) => Run[];
  waiting: (n: number) => Run[];
  threshold: (minNodes: number, minEdges: number) => Run[];
  pruned: (nodeTypes: number, nodes: number, edgeTypes: number, edges: number) => Run[];
  notPruned: Run[];
  storeCarries: (nodeTypes: number, edgeTypes: number) => Run[];

  // the header
  noDraft: string;
  nothingToDraftFrom: string;
  draftPill: string;
  draftPillTitle: string;
  draftEnforcementTitle: string;
  thresholdPill: (minNodes: number, minEdges: number) => string;
  thresholdTitle: string;
  vs: (against: string) => string;
  vsTitle: string;
  draftTitle: string;
  derived: string;
  noAnswer: string;
  noSchema: string;
  active: string;
  activeTitle: string;
  inactive: string;
  inactiveTitle: string;
  enforcementTitle: string;
  strictActions: string;
  actions: (n: number) => string;
  objectSets: (n: number) => string;

  // the band outside the model
  outsideDraft: string;
  outsideSaved: string;
  outsideNone: string;
  outsideCounts: (undeclaredNodes: number, nodes: number, undeclaredEdges: number, edges: number) => Run[];
  nodeTypes: string;
  edgeTypes: string;
  andMore: (n: number) => string;

  against: (total: number, breaking: boolean, listedBreaking: number) => Run[];

  // the drawing
  notCounted: string;
  unusedBox: string;
  implementsNothing: string;
  objectTypes: (n: number) => string;
  extendsList: (list: string) => string;
  sharedProps: (n: number) => string;
  implementors: (n: number) => string;
  one: string;
  many: string;
}

export const FINDING_TEXT: Strings<FindingText> = {
  en: {
    noReason: "no reason given",
    noState: "no state given",

    unreadable: [bE("This view cannot be asked for an ontology."), " That is not the same as this store having none — nobody asked it."],
    absent: [bE("No ontology is saved in this store."), " Nothing here declares what may be talked about, so nothing written to it was ever checked against a model."],
    absentUsage: (nodes, nodeTypes, edges, edgeTypes) => [
      " Its ", bE(nodes), ` ${pl(nodes, "node", "nodes")} carry `, bE(nodeTypes), ` distinct ${pl(nodeTypes, "type", "types")} and its `,
      bE(edges), ` ${pl(edges, "edge", "edges")} carry `, bE(edgeTypes), " — a vocabulary that exists and was never written down.",
    ],
    unused: (objects, links) => [
      bE("An ontology is saved, and nothing in the store uses it."), " Every one of its ", bE(objects), ` object ${pl(objects, "type", "types")} and `,
      bE(links), ` link ${pl(links, "type", "types")} is at zero. The model below describes intent; nothing here is evidence of it.`,
    ],
    live: (objects, links, nodes, edges) => [bE(objects), " object types and ", bE(links), " link types declared, against ", bE(nodes), " nodes and ", bE(edges), " edges in the store."],
    liveUnused: (n) => [" ", bE(n), ` ${pl(n, "declaration is", "declarations are")} at zero — declared, never used.`],
    liveStrays: (n) => [" ", bE(n), ` ${pl(n, "type is", "types are")} in the data that the schema does not describe.`],
    liveClean: [" Everything declared is in use and nothing in the store is outside the model."],
    liveUncounted: (objects, links) => [bE(objects), " object types and ", bE(links), " link types declared. ", bE("The gap was not measured"), ", so no count below means anything."],
    gapOff: "declared vs actual is switched off",
    scope: (counted, returned, total) =>
      counted === "store"
        ? "counted over every row in the store, chunk nodes included"
        : `counted over the entity graph this brain returns — chunk nodes and the edges that only wire them are not in it${total ? `; and only the ${numIn("en", returned ?? 0)} most-connected of ${numIn("en", total)} nodes came back` : ""}`,
    unknownState: [bE("This report arrived in a state this page does not know how to read.")],

    undraftable: [bE("This source cannot draft an ontology."), " That is not the same as this store having nothing to draft — nobody could ask it."],
    nothingToDraft: [
      bE("There is nothing here to draft from."),
      " The store was read and holds no nodes, so there is no vocabulary to write down. That is an answer about this store, not about the deriver.",
    ],
    unknownDraftState: [bE("This draft arrived in a state this page does not know how to read.")],
    drafted: (objects, links, nodes, edges) => [
      bE("Nobody has saved this."), " It is a first schema derived from what this store already contains: ",
      bE(objects), ` object ${pl(objects, "type", "types")} and `, bE(links), ` link ${pl(links, "type", "types")} proposed over the `,
      bE(nodes), ` ${pl(nodes, "node", "nodes")} and `, bE(edges), ` ${pl(edges, "edge", "edges")} that were read. `,
      "Every type is experimental, every primary key is a guess, every cardinality is MANY, and enforcement is vocabulary. ",
      "Saving it — or a corrected version of it — is a person's act and happens elsewhere, through ", bE("ontology_save"), ".",
    ],
    waiting: (n) => [" ", bE(n), ` ${pl(n, "question is", "questions are")} waiting for a decision below.`],
    threshold: (minNodes, minEdges) => ["Drawn at ", bE(`min_nodes=${nE(minNodes)}`), ", ", bE(`min_edges=${nE(minEdges)}`), ". "],
    pruned: (nodeTypes, nodes, edgeTypes, edges) => [
      "That kept ", bE(nodeTypes), ` node ${pl(nodeTypes, "type", "types")} (${nE(nodes)} nodes) and `,
      bE(edgeTypes), ` edge ${pl(edgeTypes, "type", "types")} (${nE(edges)} edges) out of the `, { em: "drawing" },
      " — not out of the counts. They are in the band below, and a lower threshold draws them.",
    ],
    notPruned: ["Nothing was kept out of the drawing by the threshold; everything absent below was excluded by the deriver's own rules."],
    storeCarries: (nodeTypes, edgeTypes) => [" The store carries ", bE(nodeTypes), " node types and ", bE(edgeTypes), " edge types in all."],

    noDraft: "no draft from this source",
    nothingToDraftFrom: "nothing to draft from",
    draftPill: "draft · nobody signed this",
    draftPillTitle: "Derived from the store; nothing is saved until a person saves it with ontology_save.",
    draftEnforcementTitle: "Enforcement the draft would be saved with",
    thresholdPill: (minNodes, minEdges) => `min ${nE(minNodes)} / ${nE(minEdges)}`,
    thresholdTitle: "The threshold the draft ran with: min_nodes / min_edges",
    vs: (against) => `vs ${against}`,
    vsTitle: "Held against the schema this store already saved",
    draftTitle: "draft",
    derived: "derived, not saved",
    noAnswer: "no answer from this source",
    noSchema: "no schema saved",
    active: "active",
    activeTitle: "This schema decides what writes are allowed",
    inactive: "inactive",
    inactiveTitle: "Saved, and not the one in effect",
    enforcementTitle: "What an active schema does to a write that does not conform",
    strictActions: "strict actions",
    actions: (n) => `${nE(n)} ${pl(n, "action", "actions")}`,
    objectSets: (n) => `${nE(n)} object ${pl(n, "set", "sets")}`,

    outsideDraft:
      "The draft is silent about these. They are what the deriver bucketed out — record kinds and the writes that named no type — or withheld: a minority spelling it would not merge, a name that cannot be an API name, or a type under the threshold.",
    outsideSaved: "These are the types the store's own records carry, which this schema never declares.",
    outsideNone: "There is no schema, so every type the store uses is here. This is the vocabulary the brain already has and has never written down.",
    outsideCounts: (un, nodes, ue, edges) => [" ", bE(un), " of ", bE(nodes), " nodes and ", bE(ue), " of ", bE(edges), " edges are under one of them."],
    nodeTypes: "node types",
    edgeTypes: "edge types",
    andMore: (n) => `and ${nE(n)} more`,

    against: (total, breaking, listed) => [
      "This store already has a schema, and nothing here is applied to it. This is what would differ if the draft replaced it, taken through the same comparison a version bump goes through: ",
      bE(total), ` ${pl(total, "change", "changes")}`,
      ...(breaking
        ? [", of which ", bE(listed), " listed here would stop something already stored from validating."]
        : [", none of which would invalidate anything already stored."]),
    ],

    notCounted: "not counted",
    unusedBox: "unused",
    implementsNothing: "implements nothing",
    objectTypes: (n) => `${n} object ${pl(n, "type", "types")}`,
    extendsList: (list) => `extends ${list}`,
    sharedProps: (n) => `${n} shared properties`,
    implementors: (n) => `${n} implementors`,
    one: "one",
    many: "many",
  },
  zh: {
    noReason: "未给出原因",
    noState: "未给出状态",

    unreadable: [bZ("无法向这个视图询问本体。"), "这并不等于这个存储没有本体——只是没有人问过它。"],
    absent: [bZ("这个存储没有保存本体。"), "这里没有任何东西声明可以谈论什么，所以写入它的内容从未对照过模型。"],
    absentUsage: (nodes, nodeTypes, edges, edgeTypes) => [
      "它的 ", bZ(nodes), " 个节点带有 ", bZ(nodeTypes), " 种不同的类型，", bZ(edges), " 条边带有 ", bZ(edgeTypes), " 种——一套已经存在、却从未写下来的词汇。",
    ],
    unused: (objects, links) => [
      bZ("已保存了一个本体，但存储中没有任何东西在用它。"), "它的 ", bZ(objects), " 个对象类型和 ", bZ(links),
      " 个链接类型全部为零。下面的模型描述的是意图，这里没有任何东西能证明它。",
    ],
    live: (objects, links, nodes, edges) => ["声明了 ", bZ(objects), " 个对象类型和 ", bZ(links), " 个链接类型，对照存储中的 ", bZ(nodes), " 个节点和 ", bZ(edges), " 条边。"],
    liveUnused: (n) => [bZ(n), " 项声明为零——声明了，从未使用。"],
    liveStrays: (n) => ["数据中有 ", bZ(n), " 种类型是模式没有描述的。"],
    liveClean: ["声明的都在使用，存储中也没有任何东西在模型之外。"],
    liveUncounted: (objects, links) => ["声明了 ", bZ(objects), " 个对象类型和 ", bZ(links), " 个链接类型。", bZ("没有测量差距"), "，所以下面的计数都没有意义。"],
    gapOff: "声明与实际的对照已关闭",
    scope: (counted, returned, total) =>
      counted === "store"
        ? "统计范围：存储中的每一行，包括分块节点"
        : `统计范围：这个大脑返回的实体图——不含分块节点，也不含只连接分块的边${total ? `；而且只返回了 ${numIn("zh", total)} 个节点中连接最多的 ${numIn("zh", returned ?? 0)} 个` : ""}`,
    unknownState: [bZ("这份报告的状态是本页不认识的，无法解读。")],

    undraftable: [bZ("这个数据源无法起草本体。"), "这并不等于这个存储没有可起草的内容——只是没有办法问它。"],
    nothingToDraft: [bZ("这里没有可以用来起草的内容。"), "已经读取了存储，里面没有节点，所以没有词汇可写。这是关于这个存储的回答，而不是关于推导器的。"],
    unknownDraftState: [bZ("这份草稿的状态是本页不认识的，无法解读。")],
    drafted: (objects, links, nodes, edges) => [
      bZ("还没有人保存它。"), "这是根据存储已有内容推导出的第一版模式：基于读取到的 ",
      bZ(nodes), " 个节点和 ", bZ(edges), " 条边，提出了 ", bZ(objects), " 个对象类型和 ", bZ(links), " 个链接类型。",
      "每个类型都是实验性的，每个主键都是猜测，每个基数都是 MANY，约束方式是 vocabulary。",
      "保存它（或它的修正版）是人的行为，在别处通过 ", bZ("ontology_save"), " 完成。",
    ],
    waiting: (n) => ["下面有 ", bZ(n), " 个问题等待决定。"],
    threshold: (minNodes, minEdges) => ["绘制阈值为 ", bZ(`min_nodes=${nZ(minNodes)}`), "、", bZ(`min_edges=${nZ(minEdges)}`), "。"],
    pruned: (nodeTypes, nodes, edgeTypes, edges) => [
      "因此有 ", bZ(nodeTypes), ` 个节点类型（${nZ(nodes)} 个节点）和 `, bZ(edgeTypes), ` 个边类型（${nZ(edges)} 条边）没有画进`, { em: "图" },
      "里——但仍计入计数。它们在下面的区域中，降低阈值就会画出来。",
    ],
    notPruned: ["阈值没有把任何东西挡在图外；下面缺少的都是被推导器自己的规则排除的。"],
    storeCarries: (nodeTypes, edgeTypes) => ["存储中共有 ", bZ(nodeTypes), " 种节点类型和 ", bZ(edgeTypes), " 种边类型。"],

    noDraft: "这个数据源没有草稿",
    nothingToDraftFrom: "没有可起草的内容",
    draftPill: "草稿 · 无人签署",
    draftPillTitle: "从存储推导而来；在有人用 ontology_save 保存之前，什么都不会保存。",
    draftEnforcementTitle: "草稿保存时会采用的约束方式",
    thresholdPill: (minNodes, minEdges) => `最少 ${nZ(minNodes)} / ${nZ(minEdges)}`,
    thresholdTitle: "草稿使用的阈值：min_nodes / min_edges",
    vs: (against) => `对照 ${against}`,
    vsTitle: "与这个存储已保存的模式对照",
    draftTitle: "草稿",
    derived: "推导所得，未保存",
    noAnswer: "这个数据源没有回答",
    noSchema: "没有保存模式",
    active: "生效中",
    activeTitle: "这个模式决定允许哪些写入",
    inactive: "未生效",
    inactiveTitle: "已保存，但不是当前生效的那个",
    enforcementTitle: "生效的模式如何处理不符合的写入",
    strictActions: "严格动作",
    actions: (n) => `${nZ(n)} 个动作`,
    objectSets: (n) => `${nZ(n)} 个对象集`,

    outsideDraft:
      "草稿对这些只字未提。它们是推导器归类排除的——记录类别，以及没有指明类型的写入——或者暂不采纳的：不会被合并的少数拼写、不能作为 API 名的名称，或低于阈值的类型。",
    outsideSaved: "这些是存储自身记录带有、而这个模式从未声明的类型。",
    outsideNone: "没有模式，所以存储使用的每种类型都在这里。这是大脑已经拥有、却从未写下来的词汇。",
    outsideCounts: (un, nodes, ue, edges) => [bZ(nodes), " 个节点中有 ", bZ(un), " 个、", bZ(edges), " 条边中有 ", bZ(ue), " 条属于其中某个类型。"],
    nodeTypes: "节点类型",
    edgeTypes: "边类型",
    andMore: (n) => `还有 ${nZ(n)} 个`,

    against: (total, breaking, listed) => [
      "这个存储已经有一个模式，这里的任何内容都不会应用到它上面。下面是草稿替换它时会有的差异，经过与版本升级相同的比较：",
      bZ(total), " 处变更",
      ...(breaking ? ["，其中这里列出的 ", bZ(listed), " 处会让已存储的内容无法通过校验。"] : ["，都不会让已存储的内容失效。"]),
    ],

    notCounted: "未统计",
    unusedBox: "未使用",
    implementsNothing: "未实现接口",
    objectTypes: (n) => `${n} 个对象类型`,
    extendsList: (list) => `扩展 ${list}`,
    sharedProps: (n) => `${n} 个共享属性`,
    implementors: (n) => `${n} 个实现者`,
    one: "一个",
    many: "多个",
  },
};
