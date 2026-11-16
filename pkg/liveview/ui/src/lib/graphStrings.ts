// Everything the graph page says, in both languages: the explore bar, the
// inspector, the key, the live rail, the toolbar, the view settings, the
// knowledge contract, the pin banner and the notes the page puts up as it
// works. Names from the store (node labels, types, predicates, tool names,
// ids) and Cypher are data and are never translated.

import { pickLang, type Lang, type Strings } from "@/lib/i18n";

/** The note under the canvas while a path is being traced, kept as what it says rather than a sentence, so it follows the language. */
export type PathNote =
  | { kind: "none" }
  | { kind: "first" }
  | { kind: "second"; from: string }
  | { kind: "nopath" }
  | { kind: "path"; hops: number; names: string[] };

export interface GraphText {
  loadingGraph: string;
  loadingLibrary: string;
  modeGraph: string;
  modeLibrary: string;
  readingBrain: string;
  emptyTitle: string;

  // panels and their buttons
  relation: string;
  key: string;
  live: string;
  liveChanges: string;
  view: string;
  viewSettings: string;
  results: string;
  showResults: string;
  fitGraph: string;
  tracePath: string;
  tracePathBetween: string;
  clearPath: string;
  cancel: string;
  orbit: string;
  stopOrbit: string;
  showNames: string;
  hideNames: string;
  showKey: string;
  hideKey: string;
  showLive: string;
  hideLive: string;
  contract: string;
  canvas: string;
  close: string;

  // explore
  find: string;
  ask: string;
  cypher: string;
  findPlaceholder: string;
  askPlaceholder: string;
  cannotExplore: string;
  cypherLabel: string;
  askLabel: string;
  findLabel: string;
  clearSearch: string;
  run: string;
  go: string;
  notInView: string;
  findNothing: string;
  askNothing: string;
  facts: string;
  memories: string;
  knowledge: string;
  rows: (n: number) => string;
  truncated: string;
  nodesN: (n: number) => string;
  broughtIn: (n: number) => string;
  asking: string;
  running: string;
  looking: string;
  lit: (n: number) => string;
  frame: string;
  clear: string;
  share: string;
  hide: string;

  // notes and toasts
  path: (note: PathNote) => string;
  newAround: (n: number, label: string) => string;
  nothingNewAround: (label: string) => string;
  notInViewNoExplore: string;
  noNode: (id: string) => string;
  sourceCannotExplore: string;
  linkCopied: string;
  link: string;
  nothingCalled: (name: string) => string;
  replayed: (query: string, hits: number, shown: number) => string;

  // inspector
  appearedTogether: string;
  fold: string;
  hiddenList: string;
  showFewer: string;
  more: (n: number) => string;
  noRelations: string;
  outgoing: string;
  incoming: string;
  noEntityChanges: string;
  noChangeLog: string;
  history: string;
  expand: string;
  expanding: string;
  flyTo: string;
  untyped: string;
  relations: (n: number) => string;
  notDrawn: string;
  relationsTitle: string;
  latestChanges: string;
  record: string;
  relationGone: string;
  noRecordId: string;
  openSource: string;
  openTarget: string;

  // record
  noRecords: string;
  readingRecord: string;
  cannotLookUp: string;
  recordGone: string;
  saved: string;
  scope: string;
  importance: string;
  noGrade: string;
  noReasonFor: (grade: string) => string;
  state: string;
  source: string;
  chunk: string;
  producer: string;
  producedAt: string;
  assertedBy: string;
  confidence: string;
  validFrom: string;
  from: string;
  to: string;
  inferredBy: string;
  unnamedRule: string;
  document: string;
  contradicts: string;
  drawnFrom: string;
  chunksShown: (shown: number, of: number) => string;
  chunksUnloaded: (n: number) => string;
  missingChunks: (n: number, ids: string) => string;
  decided: string;
  restsOn: string;
  chainCut: string;
  verifiedNoDecision: string;
  supersedes: string;

  // key
  hideX: (label: string) => string;
  showX: (label: string) => string;
  showAllN: (n: number) => string;
  noGrades: string;
  recallLogOff: string;
  readingRecallLog: string;
  never: string;
  reachedN: (n: number) => string;
  noRelationsYet: string;
  hideCoOccurs: string;
  showCoOccurs: string;
  showAll: string;
  howEstablished: string;
  reachedByRecalls: string;
  entities: string;
  relationsHeading: string;

  // rail
  all: string;
  changes: string;
  calls: string;
  railNoSource: string;
  railEmpty: string;

  // view settings
  pickInstant: string;
  notInstant: string;
  asOf: string;
  pin: string;
  backToNow: string;
  stillNow: (reason: string) => string;
  noPast: string;
  pinnedStopped: string;
  findOnCanvas: string;
  highlightByName: string;
  colourBy: string;
  type: string;
  grade: string;
  recalls: string;
  noGradesShort: string;
  show: string;
  coOccursLabel: string;
  namesOnCanvas: string;
  pulses: string;

  // the knowledge contract
  nodes: string;
  edges: string;
  untaggedRow: string;
  blank: string;
  cannotReadContract: string;
  nothingOnShelf: string;
  /** Around two bold counts: before the nodes, between, after the edges. */
  noneGraded: [string, string, string];
  needsPerson: string;
  nothingHeld: string;
  byWhom: (refused: boolean, by: string) => string;
  edge: string;
  noReason: string;
  showingOf: (shown: string, of: string) => string;
  pinnedContract: string;
  readingContract: string;

  // the pin banner
  /** Around the bold instant. */
  pinnedBanner: [string, string];
  aPastInstant: string;
}

const en: GraphText = {
  loadingGraph: "Loading the graph",
  loadingLibrary: "Opening the library",
  modeGraph: "Graph",
  modeLibrary: "Library",
  readingBrain: "Reading the brain",
  emptyTitle: "Empty graph",

  relation: "Relation",
  key: "Key",
  live: "Live",
  liveChanges: "Live changes",
  view: "View",
  viewSettings: "View settings",
  results: "Results",
  showResults: "Show results",
  fitGraph: "Fit the graph",
  tracePath: "Trace a path",
  tracePathBetween: "Trace a path between two nodes",
  clearPath: "Clear the path",
  cancel: "Cancel",
  orbit: "Orbit",
  stopOrbit: "Stop orbiting",
  showNames: "Show names",
  hideNames: "Hide names",
  showKey: "Show the key",
  hideKey: "Hide the key",
  showLive: "Show live changes",
  hideLive: "Hide live changes",
  contract: "Knowledge contract",
  canvas: "Canvas",
  close: "Close",

  find: "Find",
  ask: "Ask",
  cypher: "Cypher",
  findPlaceholder: "Find by name",
  askPlaceholder: "Ask",
  cannotExplore: "Explore unavailable",
  cypherLabel: "Cypher query",
  askLabel: "Ask the brain",
  findLabel: "Find by name",
  clearSearch: "Clear the search",
  run: "Run",
  go: "Go",
  notInView: "Not in view",
  findNothing: "No results",
  askNothing: "No results",
  facts: "Facts",
  memories: "Memories",
  knowledge: "Knowledge",
  rows: (n) => `${n} row${n === 1 ? "" : "s"}`,
  truncated: " (truncated)",
  nodesN: (n) => `${n} nodes`,
  broughtIn: (n) => ` · +${n} in view`,
  asking: "Asking...",
  running: "Running...",
  looking: "Looking...",
  lit: (n) => `${n} lit`,
  frame: "Frame",
  clear: "Clear",
  share: "Share",
  hide: "Hide",

  path: (p) => {
    switch (p.kind) {
      case "none":
        return "";
      case "first":
        return "From —";
      case "second":
        return `From ${p.from} · To —`;
      case "nopath":
        return "No path";
      case "path":
        return `${p.hops} hop${p.hops === 1 ? "" : "s"}: ${p.names.join(" → ")}`;
    }
  },
  newAround: (n, label) => `${label} · +${n}`,
  nothingNewAround: (label) => `${label} · +0`,
  notInViewNoExplore: "Not in view",
  noNode: (id) => `No node ${id}`,
  sourceCannotExplore: "Explore unavailable",
  linkCopied: "Link copied",
  link: "Link",
  nothingCalled: (name) => `No match: ${name}`,
  replayed: (query, hits, shown) => `Recall "${query}" · ${hits} hits · ${shown} in view`,

  appearedTogether: "appeared together",
  fold: "Fold",
  hiddenList: "Hidden, list",
  showFewer: "Show fewer",
  more: (n) => `${n} more`,
  noRelations: "No relations",
  outgoing: "Outgoing",
  incoming: "Incoming",
  noEntityChanges: "No changes",
  noChangeLog: "No change log",
  history: "History",
  expand: "Expand",
  expanding: "Expanding",
  flyTo: "Fly to it",
  untyped: "untyped",
  relations: (n) => `${n} ${n === 1 ? "relation" : "relations"}`,
  notDrawn: "Not drawn",
  relationsTitle: "Relations",
  latestChanges: "Latest changes",
  record: "Record",
  relationGone: "Not in scene",
  noRecordId: "No record id",
  openSource: "Open source",
  openTarget: "Open target",

  noRecords: "Records unavailable",
  readingRecord: "Reading…",
  cannotLookUp: "Lookup failed",
  recordGone: "Record not found",
  saved: "Saved",
  scope: "Scope",
  importance: "Importance",
  noGrade: "No grade",
  noReasonFor: (grade) => `No reason (required for ${grade})`,
  state: "State",
  source: "Source",
  chunk: "Chunk",
  producer: "Producer",
  producedAt: "Produced at",
  assertedBy: "Asserted by",
  confidence: "Confidence",
  validFrom: "Valid from",
  from: "From",
  to: "To",
  inferredBy: "Inferred by",
  unnamedRule: "(unnamed rule)",
  document: "Document",
  contradicts: "Contradicts",
  drawnFrom: "Drawn from",
  chunksShown: (shown, of) => `${shown} / ${of} chunks`,
  chunksUnloaded: (n) => `${n} chunks cited · 0 loaded`,
  missingChunks: (n, ids) => `Missing chunks (${n}): ${ids}`,
  decided: "Decided",
  restsOn: "Rests on",
  chainCut: "Truncated",
  verifiedNoDecision: "Verified · no decision",
  supersedes: "supersedes",

  hideX: (label) => `Hide ${label}`,
  showX: (label) => `Show ${label}`,
  showAllN: (n) => `Show all ${n}`,
  noGrades: "No grades",
  recallLogOff: "Recall log off",
  readingRecallLog: "Reading…",
  never: "never",
  reachedN: (n) => `Reached by recalls: ${n}`,
  noRelationsYet: "No relations",
  hideCoOccurs: "Hide co_occurs",
  showCoOccurs: "Show co_occurs",
  showAll: "Show all",
  howEstablished: "How well established",
  reachedByRecalls: "Reached by recalls",
  entities: "Entities",
  relationsHeading: "Relations",

  all: "All",
  changes: "Changes",
  calls: "Calls",
  railNoSource: "No changes or calls",
  railEmpty: "Empty",

  pickInstant: "No instant",
  notInstant: "Invalid instant",
  asOf: "As of",
  pin: "Pin",
  backToNow: "Back to now",
  stillNow: (reason) => `Now · ${reason}`,
  noPast: "Past unavailable",
  pinnedStopped: "Live updates paused",
  findOnCanvas: "Find on the canvas",
  highlightByName: "Highlight by name",
  colourBy: "Colour entities by",
  type: "Type",
  grade: "Grade",
  recalls: "Recalls",
  noGradesShort: "No grades",
  show: "Show",
  coOccursLabel: "co_occurs relations",
  namesOnCanvas: "Names on the canvas",
  pulses: "Pulses along new relations",

  nodes: "nodes",
  edges: "edges",
  untaggedRow: "untagged",
  blank: "(blank)",
  cannotReadContract: "Contract unavailable",
  nothingOnShelf: "No records",
  noneGraded: ["Nodes ", " · Edges ", " · Graded 0"],
  needsPerson: "Needs a person",
  nothingHeld: "Held 0 · Refused 0",
  byWhom: (refused, by) => `${refused ? "refused" : "held"} by ${by}`,
  edge: "edge",
  noReason: "No reason (required)",
  showingOf: (shown, of) => `${shown} / ${of}`,
  pinnedContract: "Pinned",
  readingContract: "Reading…",

  pinnedBanner: ["As of ", " · paused"],
  aPastInstant: "a past instant",
};

const zh: GraphText = {
  loadingGraph: "正在加载图谱",
  loadingLibrary: "正在打开图书馆",
  modeGraph: "图谱",
  modeLibrary: "图书馆",
  readingBrain: "正在读取大脑",
  emptyTitle: "空图谱",

  relation: "关系",
  key: "图例",
  live: "实时",
  liveChanges: "实时变更",
  view: "视图",
  viewSettings: "视图设置",
  results: "结果",
  showResults: "显示结果",
  fitGraph: "适配整个图谱",
  tracePath: "追踪路径",
  tracePathBetween: "追踪两个节点之间的路径",
  clearPath: "清除路径",
  cancel: "取消",
  orbit: "环绕",
  stopOrbit: "停止环绕",
  showNames: "显示名称",
  hideNames: "隐藏名称",
  showKey: "显示图例",
  hideKey: "隐藏图例",
  showLive: "显示实时变更",
  hideLive: "隐藏实时变更",
  contract: "知识契约",
  canvas: "画布",
  close: "关闭",

  find: "查找",
  ask: "提问",
  cypher: "Cypher",
  findPlaceholder: "按名称查找",
  askPlaceholder: "提问",
  cannotExplore: "不支持探索",
  cypherLabel: "Cypher 查询",
  askLabel: "向大脑提问",
  findLabel: "按名称查找",
  clearSearch: "清除搜索",
  run: "运行",
  go: "执行",
  notInView: "不在视图中",
  findNothing: "无结果",
  askNothing: "无结果",
  facts: "事实",
  memories: "记忆",
  knowledge: "知识",
  rows: (n) => `${n} 行`,
  truncated: "（已截断）",
  nodesN: (n) => `${n} 个节点`,
  broughtIn: (n) => ` · 视图 +${n}`,
  asking: "提问中...",
  running: "运行中...",
  looking: "查找中...",
  lit: (n) => `${n} 个已高亮`,
  frame: "框选",
  clear: "清除",
  share: "分享",
  hide: "隐藏",

  path: (p) => {
    switch (p.kind) {
      case "none":
        return "";
      case "first":
        return "起点 —";
      case "second":
        return `起点 ${p.from} · 终点 —`;
      case "nopath":
        return "无路径";
      case "path":
        return `${p.hops} 跳：${p.names.join(" → ")}`;
    }
  },
  newAround: (n, label) => `${label} · +${n}`,
  nothingNewAround: (label) => `${label} · +0`,
  notInViewNoExplore: "不在视图中",
  noNode: (id) => `没有节点 ${id}`,
  sourceCannotExplore: "不支持探索",
  linkCopied: "链接已复制",
  link: "链接",
  nothingCalled: (name) => `无匹配：${name}`,
  replayed: (query, hits, shown) => `召回“${query}” · 命中 ${hits} · 视图中 ${shown}`,

  appearedTogether: "共同出现",
  fold: "收起",
  hiddenList: "已隐藏，列出",
  showFewer: "收起",
  more: (n) => `还有 ${n} 个`,
  noRelations: "无关系",
  outgoing: "出边",
  incoming: "入边",
  noEntityChanges: "无变更",
  noChangeLog: "无变更日志",
  history: "历史",
  expand: "展开",
  expanding: "展开中",
  flyTo: "飞到它",
  untyped: "无类型",
  relations: (n) => `${n} 条关系`,
  notDrawn: "未绘制",
  relationsTitle: "关系",
  latestChanges: "最近变更",
  record: "记录",
  relationGone: "不在场景中",
  noRecordId: "无记录 id",
  openSource: "打开起点",
  openTarget: "打开终点",

  noRecords: "不支持查询记录",
  readingRecord: "读取中…",
  cannotLookUp: "查询失败",
  recordGone: "记录不存在",
  saved: "保存于",
  scope: "范围",
  importance: "重要度",
  noGrade: "无等级",
  noReasonFor: (grade) => `无原因（${grade} 必填）`,
  state: "状态",
  source: "来源",
  chunk: "分块",
  producer: "写入方",
  producedAt: "写入时间",
  assertedBy: "断言者",
  confidence: "置信度",
  validFrom: "生效起始",
  from: "起点",
  to: "终点",
  inferredBy: "推理规则",
  unnamedRule: "（未命名规则）",
  document: "文档",
  contradicts: "相矛盾",
  drawnFrom: "出处",
  chunksShown: (shown, of) => `${shown} / ${of} 个分块`,
  chunksUnloaded: (n) => `引用 ${n} 个分块 · 已加载 0`,
  missingChunks: (n, ids) => `缺失分块（${n}）：${ids}`,
  decided: "决定",
  restsOn: "依据",
  chainCut: "已截断",
  verifiedNoDecision: "已核实 · 无决定",
  supersedes: "取代",

  hideX: (label) => `隐藏 ${label}`,
  showX: (label) => `显示 ${label}`,
  showAllN: (n) => `显示全部 ${n} 个`,
  noGrades: "无等级",
  recallLogOff: "召回日志未开启",
  readingRecallLog: "读取中…",
  never: "从未",
  reachedN: (n) => `召回触达：${n}`,
  noRelationsYet: "无关系",
  hideCoOccurs: "隐藏 co_occurs",
  showCoOccurs: "显示 co_occurs",
  showAll: "全部显示",
  howEstablished: "可信程度",
  reachedByRecalls: "召回触达",
  entities: "实体",
  relationsHeading: "关系",

  all: "全部",
  changes: "变更",
  calls: "调用",
  railNoSource: "无变更或调用",
  railEmpty: "空",

  pickInstant: "未选时间点",
  notInstant: "无效时间点",
  asOf: "时间点",
  pin: "固定",
  backToNow: "回到现在",
  stillNow: (reason) => `现在 · ${reason}`,
  noPast: "不支持查询过去",
  pinnedStopped: "实时更新已暂停",
  findOnCanvas: "在画布上查找",
  highlightByName: "按名称高亮",
  colourBy: "实体着色依据",
  type: "类型",
  grade: "等级",
  recalls: "召回",
  noGradesShort: "无等级",
  show: "显示",
  coOccursLabel: "co_occurs 关系",
  namesOnCanvas: "画布上的名称",
  pulses: "新关系上的脉冲",

  nodes: "节点",
  edges: "边",
  untaggedRow: "未标注",
  blank: "（空）",
  cannotReadContract: "无法读取知识契约",
  nothingOnShelf: "无记录",
  noneGraded: ["节点 ", " · 边 ", " · 已评级 0"],
  needsPerson: "需要人工处理",
  nothingHeld: "待定 0 · 拒绝 0",
  byWhom: (refused, by) => `由 ${by} ${refused ? "拒绝" : "搁置"}`,
  edge: "边",
  noReason: "无原因（必填）",
  showingOf: (shown, of) => `${shown} / ${of}`,
  pinnedContract: "已固定",
  readingContract: "读取中…",

  pinnedBanner: ["截至 ", " · 已暂停"],
  aPastInstant: "过去某一刻",
};

export const GRAPH_TEXT: Strings<GraphText> = { en, zh };

/**
 * The graph's strings for code that runs outside a render (a toast from an
 * action, a note set by a click): the language the app shell last marked the
 * document with.
 */
export function currentGraphText(): GraphText {
  const lang: Lang = typeof document === "undefined" ? "en" : pickLang(document.documentElement.lang, null);
  return GRAPH_TEXT[lang];
}
