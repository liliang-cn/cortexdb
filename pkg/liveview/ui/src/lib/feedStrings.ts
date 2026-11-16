// What the change and activity feeds say, wherever they are shown (the Changes,
// Activity and Entity pages), in both languages. The words for an op, a kind
// or a grade live in lib/semantics; the frame's in lib/shellStrings.

import type { Strings } from "@/lib/i18n";

export interface FeedText {
  // ---- lib/changes: why a change happened -------------------------------
  /** Known history reason codes, as a clause. */
  reasons: Record<string, string>;
  replaced: (value: string) => string;
  replacedBy: (value: string) => string;
  mergedInto: (value: string) => string;
  takenBack: string;
  merged: string;
  today: string;
  yesterday: string;

  // ---- lib/entity: why a version is in effect ---------------------------
  ieTitles: Record<string, string>;
  opinions: Record<string, string>;
  alsoStatedBy: (n: number) => string;
  /** History statuses; an unknown one is shown as it came. */
  statuses: Record<string, string>;

  // ---- feed rows --------------------------------------------------------
  findInGraph: (term: string) => string;
  more: (n: number) => string;
  hideDetails: string;
  showDetails: string;
  schemaTitle: string;
  schemaChip: (schema: string) => string;
  dWhen: string;
  dRow: string;
  rowKind: (kind: string, seq: number) => string;
  dReason: string;
  dSchema: string;
  dReplaces: string;
  dReplacedBy: string;
  dProducer: string;
  dTouched: string;

  // ---- filter bar -------------------------------------------------------
  kind: string;
  op: string;
  producer: string;
  entity: string;
  filterKinds: string;
  filterOps: string;
  producerName: string;
  searchEntities: string;
  typeToSearch: string;
  nothingToPick: string;
  /** Before the typed value, which is shown in code type. */
  use: string;
  clearFilters: string;
  removeFilter: (what: string) => string;

  // ---- relation cards ---------------------------------------------------
  incomingOf: (predicate: string) => string;
  outgoingOf: (predicate: string) => string;
  oneOfSeveral: string;
  nothingInEffect: string;
  ended: string;
  recorded: string;
  /** "{prefix} {time}": "ended 2 min ago", "2分钟前结束". */
  when: (prefix: string, time: string) => string;
  replacedByLead: string;
  because: (why: string) => string;
  byLead: string;
  underSchema: (schema: string) => string;

  // ---- Changes page -----------------------------------------------------
  changesTitle: string;
  loadingChanges: string;
  noFeedTitle: string;
  readErrorTitle: string;
  tryAgain: string;
  noMatchTitle: string;
  clearThemAll: string;
  noChangesTitle: string;
  loadingOlder: string;
  loadOlder: string;
  resumeLive: string;
  pauseLive: string;
  rebuilt: string;
  dismiss: string;
  pausedNew: (n: number) => string;
  pausedResume: string;
  newChanges: (n: number) => string;

  // ---- Activity page ----------------------------------------------------
  activityTitle: string;
  connecting: string;
  waitingStream: string;
  noCallsViewTitle: string;
  noKindTitle: string;
  noCallsTitle: string;
  resumeNew: (n: number) => string;
  filterByKind: string;

  // ---- Entity page ------------------------------------------------------
  copyId: string;
  justNow: (op: string) => string;
  fGrade: string;
  fSource: string;
  fDocument: string;
  fChunks: string;
  fChunk: string;
  fProducer: string;
  fRecorded: string;
  byWho: (who: string) => string;
  fConfidence: string;
  fInferred: string;
  byRule: (rule: string) => string;
  inferredYes: string;
  fContradicts: string;
  fDecisions: string;
  decision: string;
  onWhen: (at: string) => string;
  /** Between a decision's id and the ids it replaces. */
  replacesWord: string;
  loadingEntity: string;
  noEntityTitle: string;
  allChanges: string;
  nothingKnown: string;
  notInGraph: string;
  noLiveRelations: string;
  incomingRelations: string;
  outgoingRelations: string;
  incoming: string;
  outgoing: string;
  nothingPointsHere: string;
  nothingLeaves: string;
  nIn: (n: number) => string;
  nOut: (n: number) => string;
  mentionedWith: (n: number) => string;
  mentionedAside: string;
  mergedFrom: string;
  whereFrom: string;
  recentChanges: string;
  nShown: (n: number) => string;
  noEntityChanges: string;
  readingFeed: string;
}

const s = (n: number, one: string, many: string) => (n === 1 ? one : many);

export const FEED_TEXT: Strings<FeedText> = {
  en: {
    reasons: {
      superseded: "a newer value replaced it",
      retracted: "its producer took it back",
      merged: "folded into another entity",
      document_deleted: "its source document was deleted",
      orphaned: "nothing refers to it any more",
      pruned: "pruned by retention",
    },
    replaced: (v) => `replaced ${v}`,
    replacedBy: (v) => `replaced by ${v}`,
    mergedInto: (v) => `merged into ${v}`,
    takenBack: "taken back",
    merged: "merged",
    today: "Today",
    yesterday: "Yesterday",

    ieTitles: {
      only_version: "The only version",
      replaced: "Replaced an earlier value",
      revised: "Revised in place",
      merge_survivor: "Survivor of a merge",
      several_in_effect: "Several values in effect",
    },
    opinions: {
      no_opinion: "The schema has no opinion on this relation, so nothing is closed and no value is picked as the winner.",
      multi_valued: "The schema declares this relation multi-valued, so these values hold together.",
      single_valued_unenforced:
        "The schema declares this relation single-valued, but these values were written by a path that does not close the old one.",
    },
    alsoStatedBy: (n) => `Also stated by ${n} other ${s(n, "record", "records")}.`,
    statuses: { superseded: "replaced" },

    findInGraph: (t) => `Find "${t}" in the graph`,
    more: (n) => `+${n} more`,
    hideDetails: "Hide details",
    showDetails: "Show details",
    schemaTitle: "The schema this was decided under",
    schemaChip: (schema) => `schema ${schema}`,
    dWhen: "When",
    dRow: "Row",
    rowKind: (kind, seq) => `(${kind.toLowerCase()}, seq ${seq})`,
    dReason: "Reason",
    dSchema: "Schema",
    dReplaces: "Replaces",
    dReplacedBy: "Replaced by",
    dProducer: "Producer",
    dTouched: "Touched",

    kind: "Kind",
    op: "Op",
    producer: "Producer",
    entity: "Entity",
    filterKinds: "Filter kinds",
    filterOps: "Filter ops",
    producerName: "Producer name",
    searchEntities: "Search entities",
    typeToSearch: "Type to search.",
    nothingToPick: "Nothing to pick.",
    use: "Use",
    clearFilters: "Clear filters",
    removeFilter: (what) => `Remove ${what.toLowerCase()} filter`,

    incomingOf: (p) => `${p}, incoming`,
    outgoingOf: (p) => `${p}, outgoing`,
    oneOfSeveral: "one of several in effect",
    nothingInEffect: "Nothing in effect now.",
    ended: "ended",
    recorded: "recorded",
    when: (prefix, time) => `${prefix} ${time}`,
    replacedByLead: "Replaced by",
    because: (why) => `Because ${why}.`,
    byLead: "By",
    underSchema: (schema) => `Under schema ${schema}.`,

    changesTitle: "Changes",
    loadingChanges: "Loading changes",
    noFeedTitle: "This source has no change feed",
    readErrorTitle: "The changes could not be read",
    tryAgain: "Try again",
    noMatchTitle: "No changes match these filters",
    clearThemAll: "clear them all",
    noChangesTitle: "No changes yet",
    loadingOlder: "Loading older changes",
    loadOlder: "Load older changes",
    resumeLive: "Resume live",
    pauseLive: "Pause live",
    rebuilt: "The change history moved past where this page was reading, so the list was rebuilt from the newest changes.",
    dismiss: "Dismiss",
    pausedNew: (n) => `Paused, ${n} new ${s(n, "change", "changes")}. Resume`,
    pausedResume: "Paused. Resume",
    newChanges: (n) => `${n} new ${s(n, "change", "changes")}`,

    activityTitle: "Activity",
    connecting: "Connecting",
    waitingStream: "Waiting for the live stream.",
    noCallsViewTitle: "This view cannot see tool calls",
    noKindTitle: "No calls of that kind",
    noCallsTitle: "No calls yet",
    resumeNew: (n) => `Resume (${n} new)`,
    filterByKind: "Filter by kind",

    copyId: "Copy id",
    justNow: (op) => `${op.toLowerCase()} just now`,
    fGrade: "Grade",
    fSource: "Source",
    fDocument: "Document",
    fChunks: "Chunks",
    fChunk: "Chunk",
    fProducer: "Producer",
    fRecorded: "Recorded",
    byWho: (who) => `by ${who}`,
    fConfidence: "Confidence",
    fInferred: "Inferred",
    byRule: (rule) => `By rule ${rule}.`,
    inferredYes: "Yes, not stated directly.",
    fContradicts: "Contradicts",
    fDecisions: "Decisions",
    decision: "decision",
    onWhen: (at) => `on ${at}`,
    replacesWord: "replaces",
    loadingEntity: "Loading entity",
    noEntityTitle: "No entity chosen",
    allChanges: "All changes",
    nothingKnown: "Nothing is known about this id: it is not in the graph and no change mentions it.",
    notInGraph: "This entity is not in the graph right now. It may have been merged, retracted or deleted; what is known about it is below.",
    noLiveRelations: "This server cannot describe live relations, so only history is shown.",
    incomingRelations: "Incoming relations",
    outgoingRelations: "Outgoing relations",
    incoming: "Incoming",
    outgoing: "Outgoing",
    nothingPointsHere: "Nothing points here.",
    nothingLeaves: "Nothing leaves from here.",
    nIn: (n) => `${n} in`,
    nOut: (n) => `${n} out`,
    mentionedWith: (n) => `Mentioned together with ${n}`,
    mentionedAside: "Seen in the same passage; not a stated relation.",
    mergedFrom: "Merged from",
    whereFrom: "Where it came from",
    recentChanges: "Recent changes",
    nShown: (n) => `${n} shown`,
    noEntityChanges: "No changes recorded for this entity",
    readingFeed: "Reading the change feed.",
  },
  zh: {
    reasons: {
      superseded: "被更新的值替换",
      retracted: "被写入方收回",
      merged: "并入了另一个实体",
      document_deleted: "来源文档已删除",
      orphaned: "已不再被任何东西引用",
      pruned: "被保留策略清理",
    },
    replaced: (v) => `替换了 ${v}`,
    replacedBy: (v) => `被 ${v} 替换`,
    mergedInto: (v) => `并入 ${v}`,
    takenBack: "已收回",
    merged: "已合并",
    today: "今天",
    yesterday: "昨天",

    ieTitles: {
      only_version: "唯一的版本",
      replaced: "替换了更早的值",
      revised: "原地修订",
      merge_survivor: "合并后保留的一方",
      several_in_effect: "多个值同时生效",
    },
    opinions: {
      no_opinion: "模式对这个关系没有规定，所以不会关闭任何值，也不会选出胜出的值。",
      multi_valued: "模式把这个关系声明为多值，所以这些值同时成立。",
      single_valued_unenforced: "模式把这个关系声明为单值，但这些值是经由一条不会关闭旧值的路径写入的。",
    },
    alsoStatedBy: (n) => `另有 ${n} 条记录也这么说。`,
    statuses: { superseded: "已替换", current: "当前", retracted: "已撤回", deleted: "已删除", merged: "已合并" },

    findInGraph: (t) => `在图谱中查找“${t}”`,
    more: (n) => `还有 ${n} 个`,
    hideDetails: "收起详情",
    showDetails: "展开详情",
    schemaTitle: "做出这一决定所依据的模式",
    schemaChip: (schema) => `模式 ${schema}`,
    dWhen: "时间",
    dRow: "行",
    rowKind: (kind, seq) => `（${kind}，序号 ${seq}）`,
    dReason: "原因",
    dSchema: "模式",
    dReplaces: "替换了",
    dReplacedBy: "被替换为",
    dProducer: "写入方",
    dTouched: "涉及",

    kind: "类别",
    op: "操作",
    producer: "写入方",
    entity: "实体",
    filterKinds: "筛选类别",
    filterOps: "筛选操作",
    producerName: "写入方名称",
    searchEntities: "搜索实体",
    typeToSearch: "输入以搜索。",
    nothingToPick: "没有可选项。",
    use: "使用",
    clearFilters: "清除筛选",
    removeFilter: (what) => `移除${what}筛选`,

    incomingOf: (p) => `${p}，入向`,
    outgoingOf: (p) => `${p}，出向`,
    oneOfSeveral: "多个生效值之一",
    nothingInEffect: "当前没有生效的值。",
    ended: "结束",
    recorded: "记录",
    when: (prefix, time) => `${time}${prefix}`,
    replacedByLead: "被替换为",
    because: (why) => `原因：${why}。`,
    byLead: "写入方",
    underSchema: (schema) => `依据模式 ${schema}。`,

    changesTitle: "变更",
    loadingChanges: "正在加载变更",
    noFeedTitle: "这个数据源没有变更流",
    readErrorTitle: "无法读取变更",
    tryAgain: "重试",
    noMatchTitle: "没有符合这些筛选条件的变更",
    clearThemAll: "全部清除",
    noChangesTitle: "还没有变更",
    loadingOlder: "正在加载更早的变更",
    loadOlder: "加载更早的变更",
    resumeLive: "恢复实时",
    pauseLive: "暂停实时",
    rebuilt: "变更历史已越过本页正在读取的位置，所以列表已从最新的变更重新构建。",
    dismiss: "关闭",
    pausedNew: (n) => `已暂停，${n} 条新变更。恢复`,
    pausedResume: "已暂停。恢复",
    newChanges: (n) => `${n} 条新变更`,

    activityTitle: "活动",
    connecting: "连接中",
    waitingStream: "正在等待实时数据流。",
    noCallsViewTitle: "这个视图看不到工具调用",
    noKindTitle: "没有这一类的调用",
    noCallsTitle: "还没有调用",
    resumeNew: (n) => `恢复（${n} 条新的）`,
    filterByKind: "按类别筛选",

    copyId: "复制 id",
    justNow: (op) => `刚刚${op}`,
    fGrade: "等级",
    fSource: "来源",
    fDocument: "文档",
    fChunks: "分块",
    fChunk: "分块",
    fProducer: "写入方",
    fRecorded: "记录于",
    byWho: (who) => `由 ${who}`,
    fConfidence: "置信度",
    fInferred: "推断",
    byRule: (rule) => `依据规则 ${rule}。`,
    inferredYes: "是，并非直接陈述。",
    fContradicts: "矛盾于",
    fDecisions: "决定",
    decision: "决定",
    onWhen: (at) => `于 ${at}`,
    replacesWord: "替换",
    loadingEntity: "正在加载实体",
    noEntityTitle: "未选择实体",
    allChanges: "全部变更",
    nothingKnown: "对这个 id 一无所知：它不在图谱中，也没有任何变更提到它。",
    notInGraph: "这个实体目前不在图谱中。它可能已被合并、撤回或删除；下面是关于它的已知信息。",
    noLiveRelations: "这个服务器无法描述实时关系，所以只显示历史。",
    incomingRelations: "入向关系",
    outgoingRelations: "出向关系",
    incoming: "入向",
    outgoing: "出向",
    nothingPointsHere: "没有指向这里的关系。",
    nothingLeaves: "没有从这里出发的关系。",
    nIn: (n) => `${n} 入`,
    nOut: (n) => `${n} 出`,
    mentionedWith: (n) => `与 ${n} 个实体一同被提及`,
    mentionedAside: "出现在同一段落中；并非明确陈述的关系。",
    mergedFrom: "合并自",
    whereFrom: "来源",
    recentChanges: "最近的变更",
    nShown: (n) => `显示 ${n} 条`,
    noEntityChanges: "这个实体没有记录到变更",
    readingFeed: "正在读取变更流。",
  },
};
