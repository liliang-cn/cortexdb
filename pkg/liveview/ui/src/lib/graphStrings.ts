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
  readingBrain: string;
  emptyTitle: string;
  emptyBody: string;

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
  findTitle: string;
  askTitle: string;
  cypherTitle: string;
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
  found: (n: number, inView: number) => string;
  arrowMarks: string;
  askNothing: string;
  named: (n: number, inView: number) => string;
  bringIn: (n: number) => string;
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
  historyTitle: string;
  expand: string;
  expanding: string;
  expandTitle: string;
  flyTo: string;
  shareTitle: string;
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
  coOccursHidden: string;
  showAll: string;
  howEstablished: string;
  reachedByRecalls: string;
  entities: string;
  relationsHeading: string;

  // rail
  all: string;
  changes: string;
  calls: string;
  changesTitle: string;
  callsTitle: string;
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
  gradeTitle: string;
  recalls: string;
  recallsTitle: string;
  noGradesShort: string;
  show: string;
  coOccursLabel: string;
  coOccursHint: string;
  namesOnCanvas: string;
  pulses: string;
  pulsesHint: string;

  // the knowledge contract
  nodes: string;
  edges: string;
  untaggedRow: string;
  blank: string;
  cannotReadContract: string;
  nothingOnShelf: string;
  /** Around two bold counts: before the nodes, between, after the edges. */
  noneGraded: [string, string, string];
  countedWhole: string;
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
  readingBrain: "Reading the brain",
  emptyTitle: "Nothing in the graph yet",
  emptyBody: "Entities and relations show up here as they are written.",

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
  findTitle: "Look a name up across the whole store",
  askTitle: "Run the brain's own recall",
  cypherTitle: "A read-only graph query",
  findPlaceholder: "Find anything in the store by name",
  askPlaceholder: "Ask the brain a question",
  cannotExplore: "This source cannot be explored",
  cypherLabel: "Cypher query",
  askLabel: "Ask the brain",
  findLabel: "Find by name",
  clearSearch: "Clear the search",
  run: "Run",
  go: "Go",
  notInView: "Not in the drawn part: opening it fetches it",
  findNothing: "Nothing in the store is called that.",
  found: (n, inView) => `${n} found, ${inView} in view`,
  arrowMarks: ". The arrow marks one outside it.",
  askNothing: "The brain recalled nothing for that.",
  named: (n, inView) => `${n} things named, ${inView} in view`,
  bringIn: (n) => `Bring the other ${n} in`,
  facts: "Facts",
  memories: "Memories",
  knowledge: "Knowledge",
  rows: (n) => `${n} row${n === 1 ? "" : "s"}`,
  truncated: " (truncated)",
  nodesN: (n) => `${n} nodes`,
  broughtIn: (n) => `, ${n} brought into view`,
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
        return "Pick the first node.";
      case "second":
        return `From ${p.from}. Pick the other end.`;
      case "nopath":
        return "No path between them.";
      case "path":
        return `${p.hops} hop${p.hops === 1 ? "" : "s"}: ${p.names.join(" → ")}`;
    }
  },
  newAround: (n, label) => `${n} new around ${label}`,
  nothingNewAround: (label) => `Nothing new around ${label}`,
  notInViewNoExplore: "Not in view, and this source cannot be explored.",
  noNode: (id) => `No node ${id}`,
  sourceCannotExplore: "This view's source cannot be explored.",
  linkCopied: "Link copied",
  link: "Link",
  nothingCalled: (name) => `Nothing called ${name}`,
  replayed: (query, hits, shown) => `Recall "${query}": ${hits} hits, ${shown} in this scene`,

  appearedTogether: "appeared together",
  fold: "Fold",
  hiddenList: "Hidden, list",
  showFewer: "Show fewer",
  more: (n) => `${n} more`,
  noRelations: "No relations in the scene.",
  outgoing: "Outgoing",
  incoming: "Incoming",
  noEntityChanges: "No changes to this entity in the change log.",
  noChangeLog: "This source keeps no change log.",
  history: "History",
  historyTitle: "Every version of this entity and its relations, and which one is in effect",
  expand: "Expand",
  expanding: "Expanding",
  expandTitle: "Fetch what this connects to, even outside the drawn part",
  flyTo: "Fly to it",
  shareTitle: "Copy a link that opens here",
  untyped: "untyped",
  relations: (n) => `${n} ${n === 1 ? "relation" : "relations"}`,
  notDrawn: "Not drawn in the scene. The store can still answer about it.",
  relationsTitle: "Relations",
  latestChanges: "Latest changes",
  record: "Record",
  relationGone: "This relation is no longer in the scene.",
  noRecordId: "This view cannot look this relation up: the source did not name its record id.",
  openSource: "Open source",
  openTarget: "Open target",

  noRecords:
    "This view's source cannot look a record up, so there is nothing to show about where this came from. That is not the same as a record that carries nothing.",
  readingRecord: "Reading the record...",
  cannotLookUp: "This view cannot look the record up.",
  recordGone: "The source has no record with this id any more: it was on screen and is not on the shelf.",
  saved: "Saved",
  scope: "Scope",
  importance: "Importance",
  noGrade: "Nothing on this record says how it is known.",
  noReasonFor: (grade) => `No reason given: the contract requires one for ${grade}.`,
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
  chunksShown: (shown, of) => `Showing ${shown} of ${of} chunks.`,
  chunksUnloaded: (n) => `${n} chunks cited, none of them loaded.`,
  missingChunks: (n, ids) => `${n} cited chunk(s) no longer exist: ${ids}`,
  decided: "Decided",
  restsOn: "Rests on",
  chainCut: "The chain goes further than this: it was cut, not finished.",
  verifiedNoDecision: "Graded verified, and no decision in the ledger names it.",
  supersedes: "supersedes",

  hideX: (label) => `Hide ${label}`,
  showX: (label) => `Show ${label}`,
  showAllN: (n) => `Show all ${n}`,
  noGrades:
    "This source does not report grades, so nothing here can be coloured by one. That is not the same as a store where nothing is graded.",
  recallLogOff: "The recall log is off on this brain. Start it with CORTEXDB_RECALL_LOG=1.",
  readingRecallLog: "Reading the recall log...",
  never: "never",
  reachedN: (n) => `${n} nodes reached by recalls. Grey was never recalled.`,
  noRelationsYet: "No relations yet.",
  hideCoOccurs: "Hide co_occurs",
  showCoOccurs: "Show co_occurs: two names appeared together, nothing more",
  coOccursHidden: "co_occurs only says two names appeared together, so it is hidden.",
  showAll: "Show all",
  howEstablished: "How well established",
  reachedByRecalls: "Reached by recalls",
  entities: "Entities",
  relationsHeading: "Relations",

  all: "All",
  changes: "Changes",
  calls: "Calls",
  changesTitle: "Rows written to the store",
  callsTitle: "Tool calls an agent made",
  railNoSource: "This source reports the graph but no changes or tool calls.",
  railEmpty: "Nothing yet. Changes and tool calls appear here as they happen, newest first.",

  pickInstant: "Pick an instant first.",
  notInstant: "That is not an instant.",
  asOf: "As of",
  pin: "Pin",
  backToNow: "Back to now",
  stillNow: (reason) => `Still showing now: ${reason}`,
  noPast: "This source cannot be asked about the past.",
  pinnedStopped: "Live updates are stopped while pinned.",
  findOnCanvas: "Find on the canvas",
  highlightByName: "Highlight by name",
  colourBy: "Colour entities by",
  type: "Type",
  grade: "Grade",
  gradeTitle: "How each record is known: the knowledge contract's grade",
  recalls: "Recalls",
  recallsTitle: "How often recalls reached each node (needs the recall log)",
  noGradesShort: "This source does not report grades.",
  show: "Show",
  coOccursLabel: "co_occurs relations",
  coOccursHint: "Only says two names appeared together.",
  namesOnCanvas: "Names on the canvas",
  pulses: "Pulses along new relations",
  pulsesHint: "Light that runs from source to target.",

  nodes: "nodes",
  edges: "edges",
  untaggedRow: "untagged",
  blank: "(blank)",
  cannotReadContract: "This view cannot read the knowledge contract.",
  nothingOnShelf: "Nothing on this shelf yet: no records to grade.",
  noneGraded: ["", " nodes and ", " edges, and not one carries a grade. Nothing here says how it is known."],
  countedWhole: "Counted over the whole store; the scene draws its most-connected part.",
  needsPerson: "Needs a person",
  nothingHeld: "Nothing held, nothing refused.",
  byWhom: (refused, by) => `${refused ? "refused" : "held"} by ${by}`,
  edge: "edge",
  noReason: "No reason given: the contract requires one.",
  showingOf: (shown, of) => `Showing ${shown} of ${of}.`,
  pinnedContract:
    "The view is pinned to a past instant. This tally counts the store as it is now, so it is not read while the scene shows then.",
  readingContract: "Reading the contract...",

  pinnedBanner: ["Showing the graph as it stood at ", ". This is the past, and it is not updating."],
  aPastInstant: "a past instant",
};

const zh: GraphText = {
  loadingGraph: "正在加载图谱",
  readingBrain: "正在读取大脑",
  emptyTitle: "图谱里还没有内容",
  emptyBody: "实体和关系一写入就会出现在这里。",

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
  findTitle: "在整个存储里按名称查找",
  askTitle: "运行大脑自己的召回",
  cypherTitle: "只读的图查询",
  findPlaceholder: "按名称在存储里查找任何东西",
  askPlaceholder: "向大脑提一个问题",
  cannotExplore: "这个数据源不支持探索",
  cypherLabel: "Cypher 查询",
  askLabel: "向大脑提问",
  findLabel: "按名称查找",
  clearSearch: "清除搜索",
  run: "运行",
  go: "执行",
  notInView: "不在已绘制的部分：打开时会去取回",
  findNothing: "存储里没有叫这个名字的东西。",
  found: (n, inView) => `找到 ${n} 个，${inView} 个在视图中`,
  arrowMarks: "。箭头标出了视图外的那些。",
  askNothing: "大脑没有召回任何内容。",
  named: (n, inView) => `提到 ${n} 个对象，${inView} 个在视图中`,
  bringIn: (n) => `把另外 ${n} 个也拉进来`,
  facts: "事实",
  memories: "记忆",
  knowledge: "知识",
  rows: (n) => `${n} 行`,
  truncated: "（已截断）",
  nodesN: (n) => `${n} 个节点`,
  broughtIn: (n) => `，${n} 个被拉进视图`,
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
        return "选第一个节点。";
      case "second":
        return `起点：${p.from}。再选另一端。`;
      case "nopath":
        return "两者之间没有路径。";
      case "path":
        return `${p.hops} 跳：${p.names.join(" → ")}`;
    }
  },
  newAround: (n, label) => `${label} 周围新增 ${n} 个`,
  nothingNewAround: (label) => `${label} 周围没有新内容`,
  notInViewNoExplore: "不在视图中，而这个数据源不支持探索。",
  noNode: (id) => `没有节点 ${id}`,
  sourceCannotExplore: "这个视图的数据源不支持探索。",
  linkCopied: "链接已复制",
  link: "链接",
  nothingCalled: (name) => `没有叫 ${name} 的东西`,
  replayed: (query, hits, shown) => `召回“${query}”：命中 ${hits} 条，${shown} 条在当前场景中`,

  appearedTogether: "共同出现",
  fold: "收起",
  hiddenList: "已隐藏，列出",
  showFewer: "收起",
  more: (n) => `还有 ${n} 个`,
  noRelations: "场景中没有关系。",
  outgoing: "出边",
  incoming: "入边",
  noEntityChanges: "变更日志里没有这个实体的变更。",
  noChangeLog: "这个数据源不保留变更日志。",
  history: "历史",
  historyTitle: "这个实体及其关系的每个版本，以及当前生效的是哪一个",
  expand: "展开",
  expanding: "展开中",
  expandTitle: "取回它所连接的东西，包括已绘制部分之外的",
  flyTo: "飞到它",
  shareTitle: "复制一个打开到这里的链接",
  untyped: "无类型",
  relations: (n) => `${n} 条关系`,
  notDrawn: "场景中没有绘制它，但存储仍然可以回答关于它的问题。",
  relationsTitle: "关系",
  latestChanges: "最近变更",
  record: "记录",
  relationGone: "这条关系已不在场景中。",
  noRecordId: "这个视图查不了这条关系：数据源没有给出它的记录 id。",
  openSource: "打开起点",
  openTarget: "打开终点",

  noRecords: "这个视图的数据源不能查询记录，所以无法显示它的来历。这和一条什么都没带的记录不是一回事。",
  readingRecord: "正在读取记录...",
  cannotLookUp: "这个视图查不了这条记录。",
  recordGone: "数据源里已经没有这个 id 的记录了：它曾出现在屏幕上，但已不在存储中。",
  saved: "保存于",
  scope: "范围",
  importance: "重要度",
  noGrade: "这条记录上没有任何信息说明它是怎么知道的。",
  noReasonFor: (grade) => `没有给出原因：契约要求 ${grade} 必须说明原因。`,
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
  chunksShown: (shown, of) => `显示 ${of} 个分块中的 ${shown} 个。`,
  chunksUnloaded: (n) => `引用了 ${n} 个分块，一个都没有加载。`,
  missingChunks: (n, ids) => `${n} 个被引用的分块已不存在：${ids}`,
  decided: "决定",
  restsOn: "依据",
  chainCut: "这条链还有后续：是被截断了，不是到头了。",
  verifiedNoDecision: "标为已核实，但账本里没有任何决定提到它。",
  supersedes: "取代",

  hideX: (label) => `隐藏 ${label}`,
  showX: (label) => `显示 ${label}`,
  showAllN: (n) => `显示全部 ${n} 个`,
  noGrades: "这个数据源不报告等级，所以这里没法按等级着色。这和一个什么都没评级的存储不是一回事。",
  recallLogOff: "这个大脑的召回日志没有开启。用 CORTEXDB_RECALL_LOG=1 开启。",
  readingRecallLog: "正在读取召回日志...",
  never: "从未",
  reachedN: (n) => `召回触达了 ${n} 个节点。灰色的从未被召回。`,
  noRelationsYet: "还没有关系。",
  hideCoOccurs: "隐藏 co_occurs",
  showCoOccurs: "显示 co_occurs：只表示两个名字一起出现过",
  coOccursHidden: "co_occurs 只表示两个名字一起出现过，所以默认隐藏。",
  showAll: "全部显示",
  howEstablished: "可信程度",
  reachedByRecalls: "召回触达",
  entities: "实体",
  relationsHeading: "关系",

  all: "全部",
  changes: "变更",
  calls: "调用",
  changesTitle: "写入存储的记录",
  callsTitle: "智能体发起的工具调用",
  railNoSource: "这个数据源只报告图谱，没有变更和工具调用。",
  railEmpty: "还没有内容。变更和工具调用发生时会出现在这里，最新的在最上面。",

  pickInstant: "请先选一个时间点。",
  notInstant: "这不是一个有效的时间点。",
  asOf: "时间点",
  pin: "固定",
  backToNow: "回到现在",
  stillNow: (reason) => `仍显示现在：${reason}`,
  noPast: "这个数据源不支持查询过去。",
  pinnedStopped: "固定期间实时更新会停止。",
  findOnCanvas: "在画布上查找",
  highlightByName: "按名称高亮",
  colourBy: "实体着色依据",
  type: "类型",
  grade: "等级",
  gradeTitle: "每条记录是怎么知道的：知识契约的等级",
  recalls: "召回",
  recallsTitle: "召回触达每个节点的次数（需要召回日志）",
  noGradesShort: "这个数据源不报告等级。",
  show: "显示",
  coOccursLabel: "co_occurs 关系",
  coOccursHint: "只表示两个名字一起出现过。",
  namesOnCanvas: "画布上的名称",
  pulses: "新关系上的脉冲",
  pulsesHint: "从起点流向终点的光。",

  nodes: "节点",
  edges: "边",
  untaggedRow: "未标注",
  blank: "（空）",
  cannotReadContract: "这个视图读不了知识契约。",
  nothingOnShelf: "这里还什么都没有：没有可评级的记录。",
  noneGraded: ["", " 个节点和 ", " 条边，没有一条带等级。这里没有任何东西说明它是怎么知道的。"],
  countedWhole: "按整个存储统计；场景只绘制连接最多的那部分。",
  needsPerson: "需要人工处理",
  nothingHeld: "没有待定的，也没有拒绝的。",
  byWhom: (refused, by) => `由 ${by} ${refused ? "拒绝" : "搁置"}`,
  edge: "边",
  noReason: "没有给出原因：契约要求必须说明。",
  showingOf: (shown, of) => `显示 ${of} 条中的 ${shown} 条。`,
  pinnedContract: "视图固定在过去某一刻。这份统计按存储的当前状态计算，所以在场景显示过去时不会读取。",
  readingContract: "正在读取契约...",

  pinnedBanner: ["显示的是图谱在 ", " 时的样子。这是过去的状态，不会更新。"],
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
