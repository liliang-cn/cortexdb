// What the Analysis page says, in both languages.

import type { BucketUnit, Measure, RowKind } from "@/api/analysis";
import type { Strings } from "@/lib/i18n";

export interface AnalysisText {
  title: string;
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
  memoriesEmpty: string;
  documentsTitle: string;
  documentsEmpty: string;
  recallsTitle: string;
  recallsEmpty: string;
  recallsOffTitle: string;
  latencyTitle: string;
  writesTitle: string;
  writesEmpty: string;
  graphTitle: string;
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
  count: string;
  of: string;
  ofKinds: Record<RowKind, string>;
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
  sampleFailed: string;
  kinds: Record<"number" | "date" | "text", string>;
  needField: (measure: string) => string;
  badRange: string;
  badName: string;

  // the builder's answer
  asking: string;
  askFailed: string;
  noRows: string;
  resultOne: (kind: string) => string;
  rows: string;
  skipped: string;
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
}

export const ANALYSIS_TEXT: Strings<AnalysisText> = {
  en: {
    title: "Analysis",
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
    memoriesEmpty: "No memory was written in this range.",
    documentsTitle: "Documents added",
    documentsEmpty: "No document was added in this range.",
    recallsTitle: "Recalls",
    recallsEmpty: "No recall was logged in this range.",
    recallsOffTitle: "The recall log is off on this brain",
    latencyTitle: "Recall latency",
    writesTitle: "Writes by kind",
    writesEmpty: "The change feed holds no write in this range.",
    graphTitle: "Graph by type",
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

    count: "Count",
    of: "Rows",
    ofKinds: { documents: "Documents", chunks: "Chunks" },
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
    sampleFailed: "The fields could not be listed",
    kinds: { number: "number", date: "date", text: "text" },
    needField: (measure) => `Pick the field to take the ${measure.toLowerCase()} of.`,
    badRange: "The end date is before the start date.",
    badName: "A field name starts with a letter or underscore and holds only letters, digits, underscores, dots and hyphens.",

    asking: "Counting",
    askFailed: "The question could not be answered",
    noRows: "Nothing matched",
    resultOne: (kind) => `over ${kind}`,
    rows: "Rows",
    skipped: "Skipped",
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
  },
  zh: {
    title: "分析",
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
    memoriesEmpty: "这个范围内没有写入记忆。",
    documentsTitle: "新增的文档",
    documentsEmpty: "这个范围内没有新增文档。",
    recallsTitle: "召回",
    recallsEmpty: "这个范围内没有记录到召回。",
    recallsOffTitle: "这个大脑没有开召回日志",
    latencyTitle: "召回延迟",
    writesTitle: "各类写入",
    writesEmpty: "变更日志在这个范围内没有写入。",
    graphTitle: "图谱按类型",
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

    count: "计数",
    of: "行",
    ofKinds: { documents: "文档", chunks: "分块" },
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
    sampleFailed: "字段列表没有读出来",
    kinds: { number: "数值", date: "日期", text: "文本" },
    needField: (measure) => `选一个字段来计算${measure}。`,
    badRange: "结束日期早于开始日期。",
    badName: "字段名以字母或下划线开头，只能包含字母、数字、下划线、点和连字符。",

    asking: "正在统计",
    askFailed: "这个问题没有得到回答",
    noRows: "没有匹配的行",
    resultOne: (kind) => `范围：${kind}`,
    rows: "行数",
    skipped: "跳过",
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
  },
};
