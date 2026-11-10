// What the frame around every page says (the top bar, its menu, the command
// palette, the theme and language toggles, the connection pill), in both
// languages. The pages keep their own (recalls-strings.ts, libraryStrings.ts,
// and the rest); this is only the shell's.

import type { Lang, Strings } from "@/lib/i18n";

/** The nav's pages, by route. */
export type NavKey = "/" | "/changes" | "/activity" | "/recalls" | "/library" | "/ontology" | "/ontology-history";

export interface ShellText {
  nav: Record<NavKey, string>;

  home: string;
  pages: string;
  menu: string;
  navigation: string;
  graphSize: string;
  nodes: string;
  edges: string;

  search: string;
  searchLabel: string;
  searchHint: string;
  searchPlaceholder: string;
  nothingMatches: string;
  entities: string;
  searching: string;

  theme: string;
  light: string;
  dark: string;
  auto: string;

  /** The language toggle's accessible name. */
  language: string;
  /** Each language's name in itself, so it can be found by someone who cannot read the other. */
  langName: Record<Lang, string>;

  loading: string;

  // the connection pill
  source: (source: string) => string;
  offline: string;
  offlineDetail: string;
  connecting: string;
  connectingDetail: string;
  reconnecting: string;
  reconnectingDetail: string;
  pinnedTo: (when: string) => string;
  aPastInstant: string;
  pinnedDetail: string;
  live: string;
  pinRefused: (reason: string) => string;
  structureOnly: string;
  structureDetail: string;
  receiving: string;

  notFound: string;
  backToGraph: string;
}

export const SHELL_TEXT: Strings<ShellText> = {
  en: {
    nav: {
      "/": "Graph",
      "/changes": "Changes",
      "/activity": "Activity",
      "/recalls": "Recalls",
      "/library": "Library",
      "/ontology": "Ontology",
      "/ontology-history": "Versions",
    },

    home: "CortexDB, graph",
    pages: "Pages",
    menu: "Menu",
    navigation: "Navigation",
    graphSize: "Graph size",
    nodes: "nodes",
    edges: "edges",

    search: "Search",
    searchLabel: "Search entities and pages",
    searchHint: "Jump to an entity or a page",
    searchPlaceholder: "Find an entity or go to a page",
    nothingMatches: "Nothing matches that.",
    entities: "Entities",
    searching: "Searching...",

    theme: "Theme",
    light: "Light",
    dark: "Dark",
    auto: "Match system",

    language: "Language",
    langName: { en: "English", zh: "中文" },

    loading: "Loading",

    source: (source) => `Source: ${source}.`,
    offline: "Offline",
    offlineDetail: "Cannot reach the live view. Retrying.",
    connecting: "Connecting",
    connectingDetail: "Opening the live stream.",
    reconnecting: "Reconnecting",
    reconnectingDetail: "The stream dropped; the browser is retrying and will resume from the last change.",
    pinnedTo: (when) => `Pinned to ${when}`,
    aPastInstant: "a past instant",
    pinnedDetail: "Showing the brain as it was then; no live updates.",
    live: "Live",
    pinRefused: (reason) => `The pin was refused: ${reason}.`,
    structureOnly: "Structure only",
    structureDetail: "This source reports the graph but no activity or change events.",
    receiving: "Receiving updates.",

    notFound: "No such page",
    backToGraph: "Back to the graph",
  },
  zh: {
    nav: {
      "/": "图谱",
      "/changes": "变更",
      "/activity": "活动",
      "/recalls": "召回",
      "/library": "图书馆",
      "/ontology": "本体",
      "/ontology-history": "版本",
    },

    home: "CortexDB，图谱",
    pages: "页面",
    menu: "菜单",
    navigation: "导航",
    graphSize: "图谱规模",
    nodes: "个节点",
    edges: "条边",

    search: "搜索",
    searchLabel: "搜索实体和页面",
    searchHint: "跳到一个实体或页面",
    searchPlaceholder: "查找实体，或跳到某个页面",
    nothingMatches: "没有匹配的结果。",
    entities: "实体",
    searching: "搜索中...",

    theme: "主题",
    light: "浅色",
    dark: "深色",
    auto: "跟随系统",

    language: "语言",
    langName: { en: "English", zh: "中文" },

    loading: "加载中",

    source: (source) => `数据源：${source}。`,
    offline: "离线",
    offlineDetail: "连不上实时视图，正在重试。",
    connecting: "连接中",
    connectingDetail: "正在打开实时数据流。",
    reconnecting: "重连中",
    reconnectingDetail: "数据流断开了；浏览器正在重试，恢复后会从上一条变更接着读。",
    pinnedTo: (when) => `固定在 ${when}`,
    aPastInstant: "过去某一刻",
    pinnedDetail: "显示的是大脑当时的样子，不会实时更新。",
    live: "实时",
    pinRefused: (reason) => `固定被拒绝：${reason}。`,
    structureOnly: "仅结构",
    structureDetail: "这个数据源只报告图谱，没有活动和变更事件。",
    receiving: "正在接收更新。",

    notFound: "没有这个页面",
    backToGraph: "回到图谱",
  },
};
