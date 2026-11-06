// Every word the library says, in both of its languages. The page it replaced
// spoke Chinese as well as English, and so does this one: ?lang= picks, and the
// browser's language decides otherwise (lib/i18n.ts).

import type { Strings } from "@/lib/i18n";

export interface LibraryText {
  title: string;
  loading: string;
  books: string;
  shelves: string;
  cards: string;
  docs: string;
  find: string;
  findLabel: string;
  clearFind: string;
  hBooks: string;
  hCards: string;
  noMatch: string;
  stacks: string;
  stacksSub: string;
  annex: string;
  annexSub: string;
  reference: string;
  catalog: string;
  lectern: string;
  wingSub: (shelves: number, books: number) => string;
  feed: string;
  quiet: string;
  live: string;
  notLive: string;
  reading: string;
  close: string;
  prev: string;
  next: string;
  page: (at: number, of: number) => string;
  shelf: string;
  index: string;
  seeAlso: string;
  shared: (n: number) => string;
  text: string;
  provenance: string;
  noRecords: string;
  cardBooks: (n: number) => string;
  rels: string;
  more: (n: number) => string;
  whereBooks: string;
  none: string;
  notFound: string;
  findOff: string;
  docsOff: string;
  grade: string;
  written: string;
  source: string;
  producer: string;
  scope: string;
  relTable: string;
  empty: string;
  emptyHint: string;
  failed: (why: string) => string;
  retry: string;
  home: string;
  closePanel: string;
  document: (chunks: number) => string;
}

export const LIBRARY_TEXT: Strings<LibraryText> = {
  en: {
    title: "Library",
    loading: "Opening the library",
    books: "books",
    shelves: "shelves",
    cards: "index cards",
    docs: "documents",
    find: "Find a book or an index card…",
    findLabel: "Find a book or an index card",
    clearFind: "Clear the search",
    hBooks: "Books",
    hCards: "Index cards",
    noMatch: "Nothing on the shelves by that name.",
    stacks: "General stacks",
    stacksSub: "memories that name no project",
    annex: "Annex",
    annexSub: "small projects",
    reference: "Reference",
    catalog: "Card catalogue",
    lectern: "Lectern",
    wingSub: (p, b) => `${p} shelves · ${b} books`,
    feed: "What is happening",
    quiet: "Nothing has happened yet.",
    live: "Live",
    notLive: "Not connected",
    reading: "reading",
    close: "Close, put it back",
    prev: "Previous page",
    next: "Next page",
    page: (a, b) => `page ${a} / ${b}`,
    shelf: "shelf",
    index: "Index · what this book mentions",
    seeAlso: "See also · books sharing the most index terms",
    shared: (n) => `${n} shared`,
    text: "Text",
    provenance: "Provenance",
    noRecords: "This view's source cannot open a book; it can only show where it stands.",
    cardBooks: (n) => (n === 1 ? "1 book mentions it" : `${n} books mention it`),
    rels: "Relations",
    more: (n) => `and ${n} more`,
    whereBooks: "Where its books are",
    none: "nothing",
    notFound: "Not in this snapshot.",
    findOff: "This source cannot be searched for index cards.",
    docsOff: "documents: the source cannot be asked",
    grade: "grade",
    written: "written",
    source: "source",
    producer: "producer",
    scope: "scope",
    relTable: "Relation table",
    empty: "The library is empty",
    emptyHint: "Memories are shelved here as they are written.",
    failed: (why) => `Could not read the library: ${why}`,
    retry: "Try again",
    home: "Back to the whole room",
    closePanel: "Close",
    document: (n) => (n === 1 ? "document · 1 chunk" : `document · ${n} chunks`),
  },
  zh: {
    title: "记忆图书馆",
    loading: "正在打开图书馆",
    books: "本记忆",
    shelves: "个书架",
    cards: "张索引卡",
    docs: "篇文档",
    find: "找一本书，或一张索引卡…",
    findLabel: "找一本书，或一张索引卡",
    clearFind: "清空搜索",
    hBooks: "书",
    hCards: "索引卡",
    noMatch: "书架上没有这个名字。",
    stacks: "总书库",
    stacksSub: "没写明项目的记忆",
    annex: "小项目回廊",
    annexSub: "零散的小项目",
    reference: "参考书区",
    catalog: "卡片目录",
    lectern: "讲台",
    wingSub: (p, b) => `${p} 个书架 · ${b} 本`,
    feed: "正在发生",
    quiet: "还没有动静。",
    live: "实时",
    notLive: "未连接",
    reading: "正在读",
    close: "合上，放回书架",
    prev: "上一页",
    next: "下一页",
    page: (a, b) => `第 ${a} / ${b} 页`,
    shelf: "书架",
    index: "书末索引 · 这本书提到的",
    seeAlso: "参见 · 共用索引词最多的书",
    shared: (n) => `共 ${n} 个索引词`,
    text: "正文",
    provenance: "来历",
    noRecords: "这个数据源不能打开书，只能看到书在哪个书架上。",
    cardBooks: (n) => `${n} 本书提到它`,
    rels: "关系",
    more: (n) => `另有 ${n} 条`,
    whereBooks: "提到它的书在哪",
    none: "没有",
    notFound: "这次快照里没有它。",
    findOff: "这个数据源不能搜索索引卡。",
    docsOff: "文档：数据源无法查询",
    grade: "等级",
    written: "写入",
    source: "出处",
    producer: "写入者",
    scope: "范围",
    relTable: "关系桌",
    empty: "图书馆还是空的",
    emptyHint: "记忆写进来，就会上架。",
    failed: (why) => `读不到图书馆：${why}`,
    retry: "再试一次",
    home: "回到整个大厅",
    closePanel: "关闭",
    document: (n) => `文档 · ${n} 段`,
  },
};
