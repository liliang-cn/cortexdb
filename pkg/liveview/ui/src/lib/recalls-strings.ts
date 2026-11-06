// Everything the Recalls page says, in English and Chinese. The old page
// spoke both, chosen by ?lang= or the browser (see lib/i18n), and this keeps
// every sentence in both rather than translating some and leaving the rest.
//
// A sentence that holds something set apart (an environment variable in
// code type) carries it as a {slot}; splitSlots cuts it there so the page can
// put an element in its place without assembling HTML from strings.

import type { Strings } from "@/lib/i18n";

export interface RecallsText {
  title: string;
  description: string;
  source: string;

  timeline: string;
  records: string;

  filterPlaceholder: string;
  filterLabel: string;
  emptyOnly: string;
  byRecord: string;
  byTrace: string;
  byKey: string;
  byProject: string;
  clearFilter: string;
  clearAll: string;
  badTrace: (raw: string) => string;

  pick: string;
  detailLabel: string;
  noHits: string;
  /** Has {on} and {days}. */
  offHow: string;
  offTitle: string;
  noSourceTitle: string;
  noSourceBody: string;
  errorTitle: string;
  retry: string;
  none: string;
  noneHint: string;
  noMatch: string;
  noMatchHint: string;

  older: string;
  loadingOlder: string;
  oldest: string;
  newRecalls: (n: number) => string;
  rebuilt: string;
  dismiss: string;
  loading: string;

  hits: (n: number) => string;
  ms: (n: number) => string;
  returnedNothing: string;
  degraded: string;
  trace: string;
  span: string;
  marks: (useful: number, noise: number) => string;

  replay: string;
  heat: string;
  golden: string;
  goldenTitle: string;
  goldenNone: string;
  goldenDone: (n: number) => string;
  goldenFailed: (message: string) => string;

  plan: string;
  hitsHeading: string;
  candidates: string;
  dropped: string;
  droppedMore: (n: number) => string;
  query: string;
  keywords: string;
  alternates: string;
  entities: string;
  mode: string;
  planner: string;
  asked: string;
  host: string;
  agent: string;
  requestId: string;
  traceId: string;
  when: string;
  latency: string;
  graphFacts: string;
  filterBy: (what: string) => string;

  useful: string;
  noise: string;
  markHit: string;
  markFailed: (message: string) => string;

  retriever: string;
  rank: string;
  rankIn: (rank: number, kind: string) => string;
  score: string;
  by: string;
  threshold: string;
  id: string;
  noExplain: string;
  askedFor: (mode: string) => string;
  sideMemory: string;
  sideKnowledge: string;
  kind: (kind: string) => string;

  unverified: string;
  verified: string;
  onGraph: string;

  detailMissing: string;
  detailMissingHint: string;
  detailError: string;
  close: string;

  totals: (days: number) => string;
  recallsN: string;
  emptyN: string;
  degradedN: string;
  reached: string;
  most: string;
  mostNone: string;
  never: string;
  noneNever: string;
  confinedNote: string;
  count: (n: number) => string;
  lastRecalled: (when: string) => string;
  saved: (when: string) => string;
  showRecalls: string;
  statsError: string;

  degradedWhy: Record<string, string>;
}

export const RECALLS_STRINGS: Strings<RecallsText> = {
  en: {
    title: "Recalls",
    description: "Every recall the brain logged: what was asked, who asked, what came back and why.",
    source: "Source",

    timeline: "Timeline",
    records: "Records",

    filterPlaceholder: "Filter by query text",
    filterLabel: "Filter recalls by query text",
    emptyOnly: "Returned nothing",
    byRecord: "Recalls of",
    byTrace: "Trace",
    byKey: "Key",
    byProject: "Project",
    clearFilter: "Clear this filter",
    clearAll: "Clear all filters",
    badTrace: (raw) => `“${raw}” is not a W3C trace id, so the timeline is not kept to one trace.`,

    pick: "Pick a recall to see why it returned what it did.",
    detailLabel: "The recall that is open",
    noHits: "Nothing came back.",
    offTitle: "The recall log is off on this brain.",
    offHow: "Start the server with {on} to turn it on. It stores the queries people ask, kept for {days} days, 30 unless set.",
    noSourceTitle: "This view cannot read the recall log.",
    noSourceBody: "It was started over a graph alone, without the brain's tools, and the recall log is read through them.",
    errorTitle: "The recalls could not be read",
    retry: "Try again",
    none: "No recalls yet.",
    noneHint: "A recall shows up here as soon as an agent asks the brain something.",
    noMatch: "No recalls match these filters.",
    noMatchHint: "Nothing the log has kept fits. Loosen a filter, or clear them all.",

    older: "Older recalls",
    loadingOlder: "Loading older recalls",
    oldest: "That is the oldest recall the log has kept.",
    newRecalls: (n) => (n === 1 ? "1 new recall" : `${n} new recalls`),
    rebuilt: "More recalls arrived than one page holds, so the timeline was read again from the newest.",
    dismiss: "Dismiss",
    loading: "Loading recalls",

    hits: (n) => (n === 1 ? "1 hit" : `${n} hits`),
    ms: (n) => `${n} ms`,
    returnedNothing: "returned nothing",
    degraded: "degraded",
    trace: "trace",
    span: "span",
    marks: (u, n) => [u ? `${u} useful` : "", n ? `${n} noise` : ""].filter(Boolean).join(", "),

    replay: "Replay on graph",
    heat: "Recall heat on graph",
    golden: "golden.jsonl",
    goldenTitle: "Download the marks as recall-eval golden cases (JSONL)",
    goldenNone: "No marks yet. Mark a hit useful or noise first.",
    goldenDone: (n) => (n === 1 ? "Exported 1 golden case." : `Exported ${n} golden cases.`),
    goldenFailed: (m) => `The export failed: ${m}`,

    plan: "Plan",
    hitsHeading: "Hits",
    candidates: "Candidates",
    dropped: "Dropped by floors and filters",
    droppedMore: (n) => `and ${n} more not kept in the record`,
    query: "Query",
    keywords: "Keywords",
    alternates: "Alternate queries",
    entities: "Entities",
    mode: "Mode",
    planner: "Planner",
    asked: "Asked by",
    host: "host",
    agent: "agent",
    requestId: "Request id",
    traceId: "Trace id",
    when: "When",
    latency: "Took",
    graphFacts: "Graph facts",
    filterBy: (what) => `Show only recalls with ${what}`,

    useful: "Useful",
    noise: "Noise",
    markHit: "Mark this hit",
    markFailed: (m) => `The mark was not saved: ${m}`,

    retriever: "Retriever",
    rank: "Rank",
    rankIn: (r, k) => `Rank ${r} among the ${k.toLowerCase()} hits`,
    score: "Score",
    by: "By",
    threshold: "Threshold",
    id: "Id",
    noExplain: "No explain record was kept for this recall.",
    askedFor: (m) => `asked for ${m}`,
    sideMemory: "Memories",
    sideKnowledge: "Knowledge",
    kind: (k) => ({ memory: "Memory", knowledge: "Document", chunk: "Chunk" })[k] ?? k,

    unverified: "unverified",
    verified: "verified",
    onGraph: "On graph",

    detailMissing: "This recall is no longer in the log.",
    detailMissingHint: "It may have passed the log's retention, or belong to another key.",
    detailError: "The recall could not be read",
    close: "Close",

    totals: (d) => (d === 1 ? "The last day" : `The last ${d} days`),
    recallsN: "recalls",
    emptyN: "returned nothing",
    degradedN: "degraded",
    reached: "records reached",
    most: "Most recalled",
    mostNone: "No record has been recalled yet.",
    never: "Never recalled",
    noneNever: "Every memory has been recalled at least once.",
    confinedNote: "A key with a scope sees only its own recalls, and this list is left out for it.",
    count: (n) => (n === 1 ? "1 recall" : `${n} recalls`),
    lastRecalled: (w) => `last ${w}`,
    saved: (w) => `saved ${w}`,
    showRecalls: "Show the recalls that returned it",
    statsError: "The statistics could not be read",

    degradedWhy: {
      embedder_unreachable: "The query could not be embedded, so only the keyword half ran.",
      vector_search_failed: "The vector search failed; the answer is the keyword half alone.",
      keyword_search_failed: "The keyword search failed; the answer is the semantic half alone.",
    },
  },
  zh: {
    title: "召回记录",
    description: "大脑记下的每一次召回：问了什么、谁问的、返回了什么，以及为什么。",
    source: "数据源",

    timeline: "时间线",
    records: "记录",

    filterPlaceholder: "按查询文本筛选",
    filterLabel: "按查询文本筛选召回",
    emptyOnly: "无结果",
    byRecord: "召回了",
    byTrace: "链路",
    byKey: "Key",
    byProject: "项目",
    clearFilter: "清除这个筛选",
    clearAll: "清除全部筛选",
    badTrace: (raw) => `“${raw}” 不是 W3C trace id，时间线没有限定到某一条链路。`,

    pick: "选一条召回，看它为什么返回这些结果。",
    detailLabel: "打开的这条召回",
    noHits: "什么也没有返回。",
    offTitle: "这个大脑没有开启召回日志。",
    offHow: "用 {on} 启动服务即可开启。日志会保存查询文本，保留 {days} 天（默认 30）。",
    noSourceTitle: "这个视图读不了召回日志。",
    noSourceBody: "它只连着一张图，没有大脑的工具，而召回日志要通过这些工具来读。",
    errorTitle: "读取召回记录失败",
    retry: "重试",
    none: "还没有召回。",
    noneHint: "智能体一向大脑提问，这里就会出现一条。",
    noMatch: "没有符合筛选条件的召回。",
    noMatchHint: "日志里保留的召回都不符合。放宽筛选条件，或者全部清除。",

    older: "更早的召回",
    loadingOlder: "正在加载更早的召回",
    oldest: "这是日志里保留的最早一条召回。",
    newRecalls: (n) => `${n} 条新召回`,
    rebuilt: "新来的召回超过了一页，时间线已从最新的一条重新读取。",
    dismiss: "知道了",
    loading: "正在加载召回记录",

    hits: (n) => `${n} 条命中`,
    ms: (n) => `${n} 毫秒`,
    returnedNothing: "无结果",
    degraded: "降级",
    trace: "链路",
    span: "span",
    marks: (u, n) => [u ? `${u} 条有用` : "", n ? `${n} 条噪音` : ""].filter(Boolean).join("，"),

    replay: "在图上重放",
    heat: "在图上看召回热度",
    golden: "golden.jsonl",
    goldenTitle: "把标注下载为 recall-eval 的 golden 用例（JSONL）",
    goldenNone: "还没有标注。先把某条命中标为有用或噪音。",
    goldenDone: (n) => `已导出 ${n} 条 golden 用例。`,
    goldenFailed: (m) => `导出失败：${m}`,

    plan: "检索计划",
    hitsHeading: "命中",
    candidates: "候选",
    dropped: "被阈值和过滤掉的",
    droppedMore: (n) => `另有 ${n} 条没有记进解释记录`,
    query: "查询",
    keywords: "关键词",
    alternates: "改写查询",
    entities: "实体",
    mode: "模式",
    planner: "规划",
    asked: "提问方",
    host: "主机",
    agent: "智能体",
    requestId: "Request id",
    traceId: "Trace id",
    when: "时间",
    latency: "耗时",
    graphFacts: "图谱事实",
    filterBy: (what) => `只看 ${what} 的召回`,

    useful: "有用",
    noise: "噪音",
    markHit: "标注这条命中",
    markFailed: (m) => `标注没有保存：${m}`,

    retriever: "检索器",
    rank: "名次",
    rankIn: (r, k) => `在${k}命中里排第 ${r}`,
    score: "分数",
    by: "原因",
    threshold: "阈值",
    id: "Id",
    noExplain: "这次召回没有保留解释记录。",
    askedFor: (m) => `请求的是 ${m}`,
    sideMemory: "记忆",
    sideKnowledge: "知识",
    kind: (k) => ({ memory: "记忆", knowledge: "文档", chunk: "片段" })[k] ?? k,

    unverified: "未验证",
    verified: "已验证",
    onGraph: "在图上看",

    detailMissing: "日志里已经没有这条召回了。",
    detailMissingHint: "它可能已经超过了日志的保留期限，或者属于另一个 key。",
    detailError: "读取这条召回失败",
    close: "关闭",

    totals: (d) => `最近 ${d} 天`,
    recallsN: "次召回",
    emptyN: "无结果",
    degradedN: "降级",
    reached: "被召回过的记录",
    most: "最常被召回",
    mostNone: "还没有记录被召回过。",
    never: "从未被召回",
    noneNever: "每条记忆都至少被召回过一次。",
    confinedNote: "限定范围的 key 只能看到自己的召回，所以不显示这一项。",
    count: (n) => `${n} 次召回`,
    lastRecalled: (w) => `最近一次 ${w}`,
    saved: (w) => `保存于 ${w}`,
    showRecalls: "看返回了它的那些召回",
    statsError: "读取统计失败",

    degradedWhy: {
      embedder_unreachable: "查询没能向量化，只跑了关键词检索。",
      vector_search_failed: "向量检索失败，结果只来自关键词检索。",
      keyword_search_failed: "关键词检索失败，结果只来自语义检索。",
    },
  },
};

export type Slotted = Array<string | { slot: string }>;

/** Cut a sentence at its {slots}: "a {x} b" is ["a ", {slot: "x"}, " b"]. */
export function splitSlots(template: string): Slotted {
  const out: Slotted = [];
  const re = /\{([a-z]+)\}/g;
  let at = 0;
  for (let m = re.exec(template); m; m = re.exec(template)) {
    if (m.index > at) out.push(template.slice(at, m.index));
    out.push({ slot: m[1] ?? "" });
    at = m.index + m[0].length;
  }
  if (at < template.length) out.push(template.slice(at));
  return out;
}
