// What the Analysis page says, in both languages.

import type { BucketUnit, Measure, RowKind } from "@/api/analysis";
import type { Strings } from "@/lib/i18n";

export interface AnalysisText {
  title: string;
  description: string;
  source: string;
  overview: string;
  explore: string;
  loading: string;
  refresh: string;
  retry: string;
  readFailed: string;

  // the range control
  range: string;
  days: (n: number) => string;
  allTime: string;
  per: Record<BucketUnit, string>;
  /** "by day, in Asia/Shanghai" under the range control. */
  bucketedBy: (unit: string, zone: string) => string;
  /** A chart that is bucketed apart from the others says how. */
  barPer: (unit: string) => string;

  // a source that cannot answer
  unavailableTitle: string;
  unavailableChart: string;

  // headline numbers
  statMemories: string;
  statDocuments: string;
  statRecalls: string;
  statNodes: string;
  statEdges: string;
  inRange: string;
  stored: string;
  storedAll: (n: string) => string;
  atLeast: (n: string) => string;
  emptyShare: (share: string) => string;
  typesCount: (n: string) => string;
  notCounted: string;

  // the charts
  memoriesTitle: string;
  memoriesNote: string;
  memoriesEmpty: string;
  documentsTitle: string;
  documentsNote: string;
  documentsEmpty: string;
  recallsTitle: string;
  recallsNote: string;
  recallsEmpty: string;
  recallsOffTitle: string;
  recallsOffBody: string;
  latencyTitle: string;
  latencyNote: string;
  writesTitle: string;
  writesNote: string;
  writesEmpty: string;
  graphTitle: string;
  graphNote: string;
  graphEmpty: string;
  nodeTypes: string;
  edgeTypes: string;
  noNodes: string;
  noEdges: string;

  // series names
  withHits: string;
  empty: string;
  median: string;
  p95: string;
  noScope: string;
  other: string;
  otherOf: (n: number) => string;
  total: string;
  writeKinds: Record<string, string>;
  scopes: Record<string, string>;

  /** A scan that stopped at its cap: the chart is complete from this date on. */
  coveredSince: (when: string) => string;
  /** The screen reader's sentence for a time chart. */
  chartSummary: (title: string, buckets: number, from: string, to: string, total: string) => string;
  chartHint: string;
  noValue: string;

  // the builder
  exploreIntro: string;
  exploreScope: string;
  noTools: string;
  count: string;
  of: string;
  ofKinds: Record<RowKind, string>;
  ofHelp: Record<RowKind, string>;
  measure: string;
  measures: Record<Measure, string>;
  field: string;
  fieldPlaceholder: string;
  groupBy: string;
  groupPlaceholder: string;
  noGroup: string;
  bucket: string;
  noBucket: string;
  units: Record<BucketUnit, string>;
  bucketOn: string;
  storedTime: string;
  collection: string;
  anyCollection: string;
  since: string;
  until: string;
  reset: string;
  sampled: (n: number) => string;
  sampleEmpty: string;
  sampleFailed: string;
  kinds: Record<"number" | "date" | "text", string>;
  needField: (measure: string) => string;
  badRange: string;
  badName: string;

  // the builder's answer
  asking: string;
  askFailed: string;
  noRows: string;
  noRowsHint: string;
  resultOne: (kind: string) => string;
  rows: string;
  skipped: string;
  skippedNote: (n: string) => string;
  truncated: (n: string) => string;
  leftOut: (n: number) => string;
  undated: (n: string) => string;
  missingGroup: string;
  noNumber: string;
  table: string;
  tableRows: (shown: string, all: string) => string;
  showAll: string;
  copyCsv: string;
  downloadCsv: string;
  copied: string;
  copyFailed: string;
  linkNote: string;
}

export const ANALYSIS_TEXT: Strings<AnalysisText> = {
  en: {
    title: "Analysis",
    description: "The brain in numbers: how much it holds, how fast it grows, and how recall is doing.",
    source: "Source",
    overview: "Overview",
    explore: "Explore",
    loading: "Reading the numbers",
    refresh: "Refresh",
    retry: "Try again",
    readFailed: "The numbers could not be read",

    range: "Time range",
    days: (n) => `${n} days`,
    allTime: "All time",
    per: { day: "day", week: "week", month: "month", year: "year" },
    bucketedBy: (unit, zone) => `One bar per ${unit}, in ${zone} time.`,
    barPer: (unit) => `Here, one bar per ${unit}.`,

    unavailableTitle: "This view cannot be analysed",
    unavailableChart: "Not available here",

    statMemories: "Memories",
    statDocuments: "Documents",
    statRecalls: "Recalls",
    statNodes: "Graph nodes",
    statEdges: "Graph edges",
    inRange: "in this range",
    stored: "everything stored",
    storedAll: (n) => `${n} in all`,
    atLeast: (n) => `${n}+`,
    emptyShare: (share) => `${share} came back empty`,
    typesCount: (n) => `${n} types`,
    notCounted: "not counted",

    memoriesTitle: "Memories written",
    memoriesNote: "By the scope they were saved in.",
    memoriesEmpty: "No memory was written in this range.",
    documentsTitle: "Documents added",
    documentsNote: "Knowledge documents by the time they were stored; a re-saved document counts when it was last saved.",
    documentsEmpty: "No document was added in this range.",
    recallsTitle: "Recalls",
    recallsNote: "How many recalls ran, and how many returned nothing.",
    recallsEmpty: "No recall was logged in this range.",
    recallsOffTitle: "The recall log is off on this brain",
    recallsOffBody: "Start the brain with CORTEXDB_RECALL_LOG=1 to keep one. It stores the queries agents ask.",
    latencyTitle: "Recall latency",
    latencyNote: "Median and 95th percentile per bucket. A bucket with no recalls has no point.",
    writesTitle: "Writes by kind",
    writesNote: "Every write the change feed kept. The feed keeps about a week, so a longer range starts where it does.",
    writesEmpty: "The change feed holds no write in this range.",
    graphTitle: "Graph by type",
    graphNote: "Entities and relations as they are typed now. The store's own records (documents, chunks, memories and their mentions) are left out.",
    graphEmpty: "The graph holds no entity yet.",
    nodeTypes: "Nodes",
    edgeTypes: "Edges",
    noNodes: "No nodes.",
    noEdges: "No edges.",

    withHits: "Returned something",
    empty: "Returned nothing",
    median: "Median",
    p95: "95th percentile",
    noScope: "(no scope)",
    other: "Other",
    otherOf: (n) => `Other (${n})`,
    total: "Total",
    writeKinds: { node: "Node", edge: "Edge", triple: "Triple", memory: "Memory", knowledge: "Document", ontology_schema: "Ontology" },
    scopes: { global: "Global", user: "User", session: "Session" },

    coveredSince: (when) => `Counted from ${when}; the brain holds more before that than one read covers.`,
    chartSummary: (title, buckets, from, to, total) => `${title}: ${buckets} points from ${from} to ${to}, ${total} in all.`,
    chartHint: "Use the left and right arrow keys to read each point.",
    noValue: "no value",

    exploreIntro: "Count or measure the brain's documents by any metadata field, over time or by group.",
    exploreScope:
      "This reads the knowledge store: documents and the chunks they are cut into. Memories are kept in a separate table the aggregation does not reach; they are counted in the overview.",
    noTools: "This view's source cannot run the brain's tools, so there is nothing to ask here.",
    count: "Count",
    of: "Rows",
    ofKinds: { documents: "Documents", chunks: "Chunks" },
    ofHelp: {
      documents: "One row per document, so a document's metadata is counted once.",
      chunks: "One row per chunk, so a long document weighs more.",
    },
    measure: "Measure",
    measures: { count: "Count", sum: "Sum", avg: "Average", median: "Median", p95: "95th percentile", min: "Minimum", max: "Maximum" },
    field: "Of field",
    fieldPlaceholder: "a numeric field",
    groupBy: "Group by",
    groupPlaceholder: "a field, or leave empty",
    noGroup: "No grouping",
    bucket: "Over time",
    noBucket: "Not over time",
    units: { day: "By day", week: "By week", month: "By month", year: "By year" },
    bucketOn: "Time of",
    storedTime: "When it was stored",
    collection: "Collection",
    anyCollection: "Every collection",
    since: "From",
    until: "To",
    reset: "Reset",
    sampled: (n) => `Fields found in a sample of ${n} ${n === 1 ? "document" : "documents"}. A field that is not listed can be typed.`,
    sampleEmpty: "No document was found to read fields from. A field name can still be typed.",
    sampleFailed: "The fields could not be listed. A field name can still be typed.",
    kinds: { number: "number", date: "date", text: "text" },
    needField: (measure) => `Pick the field to take the ${measure.toLowerCase()} of.`,
    badRange: "The end date is before the start date.",
    badName: "A field name starts with a letter or underscore and holds only letters, digits, underscores, dots and hyphens.",

    asking: "Counting",
    askFailed: "The question could not be answered",
    noRows: "Nothing matched",
    noRowsHint: "No row passes these filters. Widen the date range or pick another collection.",
    resultOne: (kind) => `over ${kind}`,
    rows: "Rows",
    skipped: "Skipped",
    skippedNote: (n) => `${n} values are not numbers and were left out, not read as 0.`,
    truncated: (n) => `More than ${n} groups: only the first ${n} are here.`,
    leftOut: (n) => `${n} smaller ${n === 1 ? "group is" : "groups are"} not drawn; the table has every one.`,
    undated: (n) => `${n} rows have no readable time and are not on the chart.`,
    missingGroup: "(not set)",
    noNumber: "no number",
    table: "Table",
    tableRows: (shown, all) => `Showing ${shown} of ${all} rows.`,
    showAll: "Show all",
    copyCsv: "Copy CSV",
    downloadCsv: "Download CSV",
    copied: "Copied",
    copyFailed: "The browser refused the copy; download instead.",
    linkNote: "The address of this page holds the whole question, so it can be shared as a link.",
  },
  zh: {
    title: "分析",
    description: "用数字看大脑：存了多少，长得多快，召回的表现如何。",
    source: "数据源",
    overview: "概览",
    explore: "探索",
    loading: "正在读取数字",
    refresh: "刷新",
    retry: "重试",
    readFailed: "数字没有读出来",

    range: "时间范围",
    days: (n) => `${n} 天`,
    allTime: "全部",
    per: { day: "天", week: "周", month: "月", year: "年" },
    bucketedBy: (unit, zone) => `每${unit}一根柱，按 ${zone} 时区划分。`,
    barPer: (unit) => `这张图每${unit}一根柱。`,

    unavailableTitle: "这个视图无法分析",
    unavailableChart: "这里读不到",

    statMemories: "记忆",
    statDocuments: "文档",
    statRecalls: "召回",
    statNodes: "图谱节点",
    statEdges: "图谱边",
    inRange: "在这个范围内",
    stored: "全部存量",
    storedAll: (n) => `共 ${n}`,
    atLeast: (n) => `${n}+`,
    emptyShare: (share) => `${share} 没有返回结果`,
    typesCount: (n) => `${n} 种类型`,
    notCounted: "未统计",

    memoriesTitle: "写入的记忆",
    memoriesNote: "按保存时的作用域分。",
    memoriesEmpty: "这个范围内没有写入记忆。",
    documentsTitle: "新增的文档",
    documentsNote: "知识文档，按存入时间统计；重新保存过的文档算在最后一次保存的时间。",
    documentsEmpty: "这个范围内没有新增文档。",
    recallsTitle: "召回",
    recallsNote: "跑了多少次召回，其中多少次什么都没返回。",
    recallsEmpty: "这个范围内没有记录到召回。",
    recallsOffTitle: "这个大脑没有开召回日志",
    recallsOffBody: "用 CORTEXDB_RECALL_LOG=1 启动大脑就会记录。日志会保存 agent 提出的查询。",
    latencyTitle: "召回延迟",
    latencyNote: "每个时间段的中位数和 95 分位。没有召回的时间段没有点。",
    writesTitle: "各类写入",
    writesNote: "变更日志保留下来的每一次写入。日志大约保留一周，范围再长也只从它开始的地方算起。",
    writesEmpty: "变更日志在这个范围内没有写入。",
    graphTitle: "图谱按类型",
    graphNote: "实体和关系现在的类型分布。存储自己的记录（文档、分块、记忆以及它们的提及关系）不算在内。",
    graphEmpty: "图谱里还没有实体。",
    nodeTypes: "节点",
    edgeTypes: "边",
    noNodes: "没有节点。",
    noEdges: "没有边。",

    withHits: "有结果",
    empty: "没有结果",
    median: "中位数",
    p95: "95 分位",
    noScope: "（无作用域）",
    other: "其他",
    otherOf: (n) => `其他（${n} 个）`,
    total: "合计",
    writeKinds: { node: "节点", edge: "边", triple: "三元组", memory: "记忆", knowledge: "文档", ontology_schema: "本体" },
    scopes: { global: "全局", user: "用户", session: "会话" },

    coveredSince: (when) => `从 ${when} 起统计；更早的数据超出了一次读取的范围。`,
    chartSummary: (title, buckets, from, to, total) => `${title}：从 ${from} 到 ${to} 共 ${buckets} 个点，合计 ${total}。`,
    chartHint: "用左右方向键逐个读取每个点。",
    noValue: "没有数值",

    exploreIntro: "按任意元数据字段统计或度量大脑里的文档，可以按时间，也可以分组。",
    exploreScope: "这里读的是知识库：文档，以及文档切成的分块。记忆存在另一张表里，聚合读不到；记忆的数量在概览里。",
    noTools: "这个视图的数据源不能运行大脑的工具，所以这里没有可问的。",
    count: "计数",
    of: "行",
    ofKinds: { documents: "文档", chunks: "分块" },
    ofHelp: {
      documents: "每个文档一行，文档的元数据只算一次。",
      chunks: "每个分块一行，长文档的权重更大。",
    },
    measure: "度量",
    measures: { count: "计数", sum: "求和", avg: "平均值", median: "中位数", p95: "95 分位", min: "最小值", max: "最大值" },
    field: "字段",
    fieldPlaceholder: "一个数值字段",
    groupBy: "分组",
    groupPlaceholder: "一个字段，或留空",
    noGroup: "不分组",
    bucket: "按时间",
    noBucket: "不按时间",
    units: { day: "按天", week: "按周", month: "按月", year: "按年" },
    bucketOn: "时间取自",
    storedTime: "存入的时间",
    collection: "集合",
    anyCollection: "所有集合",
    since: "从",
    until: "到",
    reset: "重置",
    sampled: (n) => `字段来自 ${n} 个文档的抽样。没列出来的字段可以直接输入。`,
    sampleEmpty: "没有找到可以读取字段的文档。字段名仍然可以直接输入。",
    sampleFailed: "字段列表没有读出来。字段名仍然可以直接输入。",
    kinds: { number: "数值", date: "日期", text: "文本" },
    needField: (measure) => `选一个字段来计算${measure}。`,
    badRange: "结束日期早于开始日期。",
    badName: "字段名以字母或下划线开头，只能包含字母、数字、下划线、点和连字符。",

    asking: "正在统计",
    askFailed: "这个问题没有得到回答",
    noRows: "没有匹配的行",
    noRowsHint: "没有行满足这些条件。放宽日期范围，或者换一个集合。",
    resultOne: (kind) => `范围：${kind}`,
    rows: "行数",
    skipped: "跳过",
    skippedNote: (n) => `有 ${n} 个值不是数字，已被跳过，没有当作 0。`,
    truncated: (n) => `分组超过 ${n} 个：这里只有前 ${n} 个。`,
    leftOut: (n) => `还有 ${n} 个较小的分组没有画出来；表格里有全部分组。`,
    undated: (n) => `有 ${n} 行读不出时间，没有画在图上。`,
    missingGroup: "（未设置）",
    noNumber: "没有数字",
    table: "表格",
    tableRows: (shown, all) => `显示 ${all} 行中的 ${shown} 行。`,
    showAll: "显示全部",
    copyCsv: "复制 CSV",
    downloadCsv: "下载 CSV",
    copied: "已复制",
    copyFailed: "浏览器拒绝了复制；请改用下载。",
    linkNote: "这个页面的地址里带着完整的问题，可以当链接分享。",
  },
};
