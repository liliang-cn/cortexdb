import { atom, read, update } from 'claude-code'
import type { EngineInterface, PluginOptions, Register } from 'claude-code'

import type { CaptureMark, CapturedMemory, HitOrigin, HitWhy, Recall, RecallHit, RecallPlan } from '../types'

const last = atom({ plugin: 'cortexdb-live', key: 'last' } as const, null)
const isOpen = atom({ plugin: 'cortexdb-live', key: 'isOpen' } as const, false)
const isHidden = atom({ plugin: 'cortexdb-live', key: 'isHidden' } as const, false)
const captureMark = atom({ plugin: 'cortexdb-live', key: 'capture' } as const, null)

// The server the cortexdb plugin's manifest starts. Inside that plugin
// $.mcp.connect answers with it; from a mod of its own it is this name.
const FALLBACK_SERVER = 'plugin:cortexdb:cortexdb'
// The shell hook's block starts with this; when the recall here answered, that
// one is the same question asked twice.
const SHELL_HEADER = 'Relevant CortexDB memories for this prompt'
const TOP_K = 3
const SNIPPET = 220
// Planning is worth a second, not more: past this the lexical plan stands in.
const PLAN_TIMEOUT_MS = 2500
// A recall runs before every prompt: past this the prompt goes on without it.
const RECALL_BUDGET_MS = 6000
const STATS_EVERY_MS = 5 * 60 * 1000
const CONNECT_RETRY_MS = 5 * 1000
// session.end gets 1.5 s, too little for a model call, so a session is
// captured while it is idle instead: this long after its last turn.
const CAPTURE_IDLE_MS = 90 * 1000
const CAPTURE_MIN_USER_TURNS = 2
const PLANNER = 'haiku'
const SHOW_COMMAND = 'cortexdb-show'

const STRINGS = {
  zh: {
    recalled: (n: number) => `🧠 已召回 ${n} 条`,
    detail: (m: number, k: number, s: string) => `（记忆 ${m} · 知识 ${k} · ${s}s）`,
    expand: '展开',
    collapse: '收起',
    hide: '隐藏',
    memory: '记忆',
    knowledge: '知识',
    query: '查询',
    entities: '实体',
    project: '项目',
    why: '依据',
    via: { semantic: '语义', keyword: '关键词', hybrid: '语义+关键词', graph: '图谱' },
    matched: '命中',
    from: '来源',
    session: '会话',
    key: 'key',
    nodes: (n: string) => `${n} 节点`,
    recallTime: (s: string) => `召回 ${s}s`,
    skipped: '这条不用召回',
    keywordOnly: '向量服务连不上，只用了关键词',
    unverified: (grade: string) => `⚠ 未验证（${grade}）`,
    verified: (why?: string) => (why ? `✓ 已验证：${why}` : '✓ 已验证'),
    unreachable: '连不上',
    recallFailed: '召回失败',
    denied: (tools: string) => `没有调用 CortexDB 工具的权限，请在 settings.json 的 permissions.allow 里允许：${tools}`,
    local: '本地大脑',
    captured: (n: number, retired: number) => `CortexDB 记下了 ${n} 条新记忆${retired > 0 ? `，替换了 ${retired} 条过时的` : ''}`,
    showCommand: '重新显示上一条 prompt 的 CortexDB 召回结果（隐藏之后用）',
    shown: '已在输入框上方重新显示召回结果。',
    nothingShown: '上一条 prompt 没有召回到内容。',
  },
  en: {
    recalled: (n: number) => `🧠 Recalled ${n}`,
    detail: (m: number, k: number, s: string) => ` (memories ${m} · knowledge ${k} · ${s}s)`,
    expand: 'Expand',
    collapse: 'Collapse',
    hide: 'Hide',
    memory: 'memory',
    knowledge: 'knowledge',
    query: 'Query',
    entities: 'Entities',
    project: 'Project',
    why: 'why',
    via: { semantic: 'semantic', keyword: 'keyword', hybrid: 'semantic+keyword', graph: 'graph' },
    matched: 'matched',
    from: 'from',
    session: 'session',
    key: 'key',
    nodes: (n: string) => `${n} nodes`,
    recallTime: (s: string) => `recall ${s}s`,
    skipped: 'no recall needed',
    keywordOnly: 'embedder unreachable, keyword only',
    unverified: (grade: string) => `⚠ unverified (${grade})`,
    verified: (why?: string) => (why ? `✓ verified: ${why}` : '✓ verified'),
    unreachable: 'unreachable',
    recallFailed: 'recall failed',
    denied: (tools: string) => `not allowed to call CortexDB tools; allow them in settings.json permissions.allow: ${tools}`,
    local: 'local brain',
    captured: (n: number, retired: number) => `CortexDB saved ${n} new memories${retired > 0 ? `, retired ${retired} outdated` : ''}`,
    showCommand: "Show what CortexDB recalled for the last prompt again (after Hide)",
    shown: 'The recall is shown above the prompt again.',
    nothingShown: 'The last prompt recalled nothing.',
  },
}

type Lang = keyof typeof STRINGS
type Brain = { where?: string; nodes?: number; error?: string; recallMs?: number; skipped?: boolean; degraded?: string[] }

let server: string | undefined
let lang: Lang = 'en'
let brain: Brain = {}
let idleTimer: { cancel: () => void } | undefined
let isCapturing = false
// Recorded on every memory a capture writes, so a recall can say which machine
// and which project a memory came from.
let host: string | undefined

const t = () => STRINGS[lang]

// zh when anything the person chose says Chinese; an explicit Claude Code
// language setting that says something else wins over the locale.
async function resolveLang($: EngineInterface, options: PluginOptions): Promise<Lang> {
  if (options.language === 'zh' || options.language === 'en') return options.language
  const isZh = (value: unknown) => typeof value === 'string' && /^zh|chinese|中文|汉语|简体|繁體/i.test(value.trim())
  // Claude Code's own language row reads English until someone sets it, so
  // only a Chinese value there decides; anything else falls to the locale.
  const setting = (await $.config.list().catch(() => [])).find(row => row.key === 'language')?.value
  if (isZh(setting)) return 'zh'
  for (const value of [await $.env.get('LC_ALL'), await $.env.get('LC_MESSAGES'), await $.env.get('LANG')]) {
    if (isZh(value)) return 'zh'
  }
  // macOS keeps the UI language here, while Terminal's LANG often says en_US.
  const apple = await $.process.run(['defaults', 'read', '-g', 'AppleLanguages']).catch(() => undefined)
  const first = apple?.exitCode === 0 ? apple.stdout.match(/[A-Za-z]{2,3}(-[A-Za-z0-9]+)*/)?.[0] : undefined
  return isZh(first) ? 'zh' : 'en'
}

async function serverOf($: EngineInterface) {
  if (server) return server
  const own = await $.mcp.connect('cortexdb').catch(() => undefined)
  server = own?.isConnected ? own.server : FALLBACK_SERVER
  return server
}

// A reply that is not JSON is the host's, not the server's: Claude Code
// swaps a tool result past its token limit for a line of text that starts
// "Error:" and does not flag it as an error. Its opening words say which.
// The error says what arrived — its size and first bytes — and the request id
// when the reply still carried one; the debug log keeps the rest, with the
// time, so the call can be found in the brain's trace by host, project and
// time even when the host left no id.
class NotJSON extends Error {
  constructor(tool: string, readonly reply: string, readonly requestId?: string) {
    const size = new TextEncoder().encode(reply).length
    super(`${tool}: not JSON (${size} bytes${requestId ? `, request_id ${requestId}` : ''}): ${reply.slice(0, 120).replace(/\s+/g, ' ') || 'empty reply'}`)
  }
  get tooLarge() {
    return /exceeds maximum allowed tokens/i.test(this.reply)
  }
}

// The id the brain gave the call (cortexdb.MCPRequestIDMetaKey), when the
// host passed the result's _meta through.
const requestIdOf = (result: unknown) => {
  const meta = (result as { _meta?: Record<string, unknown> })?._meta
  const id = meta?.['cortexdb/request_id']
  return typeof id === 'string' ? id : undefined
}

async function callTool($: EngineInterface, tool: string, args: Record<string, unknown>) {
  const result = await $.mcp.call(await serverOf($), tool, args)
  const text = result.content.find(block => block.type === 'text')?.text ?? ''
  const requestId = requestIdOf(result)
  if (result.isError) throw new Error(`${text.slice(0, 200) || `${tool} failed`}${requestId && !text.includes(requestId) ? ` (request_id ${requestId})` : ''}`)
  try {
    return JSON.parse(text) as unknown
  } catch {
    const err = new NotJSON(tool, text, requestId)
    $.ui.log(`cortexdb-live: ${err.message} | at ${new Date(await $.clock.now()).toISOString()} | received: ${text.slice(0, 2000)}`, { to: 'debug' })
    throw err
  }
}

function showStatus($: EngineInterface) {
  const parts = [`🧠 ${brain.where ?? t().local}`]
  if (brain.error) parts.push(`⚠ ${brain.error}`)
  else {
    if (brain.nodes !== undefined) parts.push(t().nodes(brain.nodes.toLocaleString('en-US')))
    if (brain.skipped) parts.push(t().skipped)
    else if (brain.recallMs !== undefined) parts.push(t().recallTime((brain.recallMs / 1000).toFixed(1)))
    if (!brain.skipped && brain.degraded?.includes('embedder_unreachable')) parts.push(`⚠ ${t().keywordOnly}`)
  }
  $.ui.status(parts.join(' · '))
}

async function refreshStats($: EngineInterface) {
  try {
    const stats = (await callTool($, 'graph_statistics', {})) as { node_count?: number }
    brain = { ...brain, nodes: stats.node_count, error: undefined }
  } catch (err) {
    if (isConnecting(err)) {
      // The session starts before its MCP servers finish connecting: the
      // first ask can come too early, which says nothing about the brain.
      $.clock.after(CONNECT_RETRY_MS, () => void refreshStats($))
      return
    }
    brain = { ...brain, error: isDenied(err) ? t().denied(TOOLS_TO_ALLOW) : `${t().unreachable}: ${messageOf(err)}` }
  }
  showStatus($)
}

// The switches `cortexdb-recall` and `cortexdb-session-end` write, shared with
// the shell hooks so one command turns a feature off everywhere.
async function sentinel($: EngineInterface, name: 'autorecall' | 'autocapture') {
  const cache = (await $.env.get('XDG_CACHE_HOME')) ?? `${await $.env.get('HOME')}/.cache`
  try {
    return String(await $.fs.read(`${cache}/cortexdb/${name}`)).trim()
  } catch {
    return undefined
  }
}

// --- recall -----------------------------------------------------------------

const PLAN_SYSTEM = `You plan a lookup in a long-term memory and knowledge-graph store that a coding assistant keeps across sessions: user preferences, project decisions, deployments, hosts, versions, lessons.

Given the user's new message, the project the session works in and the recent conversation, answer with JSON only:
{"skip":false,"query":"...","keywords":["..."],"alternate_queries":["..."],"entity_names":["..."],"retrieval_mode":"auto"}

- skip: true only when the message needs nothing remembered: a bare acknowledgement or go-ahead ("ok", "好", "发", "继续", "👌") whose subject the previous reply already settles.
- The message often points into the conversation instead of naming its subject: 这个, 那个, 它, 上面, 刚才, 按建议, 这个修复, this, that, it, the fix, the suggestion. Resolve every such pointer from the recent conversation and name the concrete thing it means (project, component, bug, command, file). A lookup that keeps the bare pointer word finds nothing, or finds the wrong project.
- query: the message restated as a self-contained lookup that names its subject and, when the message is about the work in it, the project.
- keywords: up to 12 terms the stored text would contain: names, project and host names, versions, abbreviations, synonyms, and BOTH Chinese and English forms of each concept.
- alternate_queries: up to 3 other phrasings.
- entity_names: up to 6 exact names of concrete things (projects, services, hosts, people, tools), the project among them when the message is about it.
- retrieval_mode: "graph" for relational questions (who uses X, what X depends on, how A relates to B), else "auto".`

type PlanReply = {
  skip?: boolean
  query?: string
  keywords?: string[]
  alternate_queries?: string[]
  entity_names?: string[]
  retrieval_mode?: string
}

type Planned = { args: Record<string, unknown>; plan: RecallPlan; skip: boolean }

// What a message is said against: the project the session works in and the
// last few turns. "这个修复前后可以验证吗" names nothing by itself; asked
// bare, it recalls whatever other project once fixed and verified something.
type Situation = { project?: string; turns: Message[] }

const RECENT_TURNS = 4

async function situationOf($: EngineInterface, prompt: string): Promise<Situation> {
  let project: string | undefined
  try {
    const root = await $.session.root()
    // A session started in the home directory works in no project.
    if (root && root !== (await $.env.get('HOME'))) project = root.split('/').filter(Boolean).at(-1)
  } catch {
    // A build without session.root: no project to name.
  }
  const history = await $.session.messages().catch(() => [])
  const turns = (Array.isArray(history) ? history : [])
    .map(m => ({ ...m, text: m.text.replace(NOISE, '').trim() }))
    .filter(m => (m.role === 'user' || m.role === 'assistant') && m.text !== '')
  // The prompt being answered may already be the last turn.
  if (turns.at(-1)?.role === 'user' && turns.at(-1)?.text === prompt) turns.pop()
  return { project, turns: turns.slice(-RECENT_TURNS) }
}

const conversationOf = (s: Situation) =>
  s.turns.map(m => `${m.role === 'user' ? 'USER' : 'ASSISTANT'}: ${clip(m.text, m.role === 'user' ? 300 : 800)}`).join('\n\n')

// The fallback when no plan came back: the message with the project and the
// start of the last reply after it, so the embedding lands near what the
// conversation is about and not near every other project's "fix" and "verify".
// The server embeds the query alone, so the context has to be in it.
const situated = (prompt: string, s: Situation) => {
  const previous = s.turns.filter(m => m.role === 'assistant').at(-1)?.text
  const lines = [s.project ? `Project: ${s.project}` : '', previous ? `Context: ${clip(oneLine(previous), 300)}` : '']
  const context = lines.filter(Boolean).join('\n')
  return context ? `${prompt}\n\n${context}` : prompt
}

async function planRecall($: EngineInterface, prompt: string): Promise<Planned> {
  const started = await $.clock.now()
  const situation = await situationOf($, prompt)
  const project = situation.project ? [situation.project] : []
  const lexical = [...new Set([...keywordsOf(prompt), ...project])]
  const query = situated(prompt, situation)
  const fallback = (why: string): Planned => ({
    args: { query, keywords: lexical, ...(project.length > 0 ? { entity_names: project } : {}), planner: `fallback: ${why}` },
    plan: { by: 'lexical', query, keywords: lexical, entities: project, mode: 'auto', ms: 0, project: situation.project },
    skip: false,
  })

  const conversation = conversationOf(situation)
  const reply = await $.model.complete({
    model: PLANNER,
    system: PLAN_SYSTEM,
    prompt: [
      situation.project ? `Project: ${situation.project}` : '',
      conversation ? `Recent conversation (oldest first):\n${conversation}` : '',
      `New user message:\n${clip(prompt, 2000)}`,
    ]
      .filter(Boolean)
      .join('\n\n'),
    maxTokens: 500,
    timeoutMs: PLAN_TIMEOUT_MS,
  })
  if (!reply.isAnswered) return fallback('no answer from the planner')
  const planned = parseJSON<PlanReply>(reply.text)
  if (!planned) return fallback('the plan was not JSON')

  const keywords = [...new Set([...strings(planned.keywords, 12), ...lexical])].slice(0, 24)
  const entities = strings(planned.entity_names, 6)
  const mode = planned.retrieval_mode === 'graph' || planned.retrieval_mode === 'lexical' ? planned.retrieval_mode : 'auto'
  const asked = typeof planned.query === 'string' && planned.query.trim() !== '' ? planned.query : query
  return {
    args: {
      query: asked,
      keywords,
      alternate_queries: strings(planned.alternate_queries, 3),
      entity_names: entities,
      retrieval_mode: mode,
      planner: 'model',
    },
    plan: {
      by: 'haiku',
      query: asked,
      keywords: strings(planned.keywords, 12),
      entities,
      mode,
      ms: (await $.clock.now()) - started,
      project: situation.project,
    },
    skip: planned.skip === true,
  }
}

async function recall($: EngineInterface, prompt: string): Promise<Recall | 'skip'> {
  const started = await $.clock.now()
  const { args, plan, skip } = await planRecall($, prompt)
  if (skip) return 'skip'
  const asked = { ...args, top_k_memories: TOP_K, top_k_knowledge: TOP_K, graph_light: true }
  // A hit that is one long document can take the whole reply past the host's
  // limit. The index of the same recall is a line per hit: it loses who wrote
  // each one, and keeps the recall.
  const payload = (await callTool($, 'knowledge_memory_recall', asked).catch(err => {
    if (err instanceof NotJSON && err.tooLarge) return callTool($, 'knowledge_memory_recall', { ...asked, detail: 'index' })
    throw err
  })) as RecallPayload
  const terms = [...plan.keywords, ...plan.entities]
  const memoryMode = payload.memory_decision?.effective_mode
  const knowledgeMode = payload.knowledge_decision?.effective_mode
  const hits: RecallHit[] = [
    ...(payload.memories ?? []).map(m => {
      const content = m.memory?.content ?? ''
      return {
        kind: 'memory' as const,
        title: m.memory?.id ?? '',
        snippet: snippetOf(content),
        why: { ...memoryVia(m, memoryMode), matched: matchedIn(content, terms) },
        origin: memoryOrigin(m.memory),
        grade: text(m.memory?.grade),
        gradeWhy: text(m.memory?.grade_why),
      }
    }),
    ...(payload.knowledge ?? payload.results ?? []).map(k => ({
      kind: 'knowledge' as const,
      title: k.title || k.knowledge_id || '',
      snippet: snippetOf(k.snippet ?? ''),
      why: { ...knowledgeVia(k, knowledgeMode), matched: matchedIn(`${k.title ?? ''} ${k.snippet ?? ''}`, terms) },
      origin: knowledgeOrigin(k),
      grade: text(k.metadata?._grade)?.toLowerCase(),
      gradeWhy: text(k.metadata?._why),
    })),
  ].filter(hit => hit.snippet !== '')
  return { hits, ms: (await $.clock.now()) - started, plan, degraded: payload.degraded ?? [] }
}

// The plan's terms a hit actually contains: the part of "why this came back"
// a person can check by reading the line.
const matchedIn = (text: string, terms: string[]) => {
  const lower = text.toLowerCase()
  return [...new Set(terms.filter(term => term.length >= 2 && lower.includes(term.toLowerCase())))].slice(0, 4)
}

// The retrievers that found a memory, as the brain names them, with the raw
// similarity when meaning was one of them. The hit's score is that similarity
// after importance and recency boosts, so it is never shown as one. A brain
// before v2.134 names neither: its decision says which mode ran.
const memoryVia = (m: MemoryHit, mode: string | undefined): Omit<HitWhy, 'matched'> => {
  const via = m.via ?? []
  const vector = typeof m.similarity === 'number' && m.similarity > 0 ? { vector: m.similarity } : {}
  if (via.includes('semantic') && via.includes('keyword')) return { via: 'hybrid', ...vector }
  if (via.includes('semantic')) return { via: 'semantic', ...vector }
  if (via.includes('graph')) return { via: 'graph' }
  if (via.includes('keyword')) return { via: 'keyword' }
  if (mode === 'graph') return { via: 'graph' }
  return { via: mode === 'lexical' ? 'keyword' : 'semantic' }
}

// The retriever that found a knowledge hit, read off the scores each one gave
// it. The fused score beside them only orders the list.
const knowledgeVia = (k: KnowledgeHit, mode: string | undefined): Omit<HitWhy, 'matched'> => {
  const vector = k.vector_score && k.vector_score > 0 ? k.vector_score : undefined
  const lexical = k.lexical_score && k.lexical_score > 0 ? k.lexical_score : undefined
  if (vector !== undefined && lexical !== undefined) return { via: 'hybrid', vector, lexical }
  if (vector !== undefined) return { via: 'semantic', vector }
  if (lexical !== undefined) return { via: 'keyword', lexical }
  if (mode === 'graph') return { via: 'graph' }
  return { via: mode === 'lexical' ? 'keyword' : 'semantic' }
}

// The single shared token every client of an unkeyed brain speaks with names
// nobody in particular, so it is left out rather than shown on every line.
const SHARED_KEY = 'legacy-token'

const text = (value: unknown) => (typeof value === 'string' && value.trim() !== '' ? value.trim() : undefined)

const compact = (origin: HitOrigin): HitOrigin | undefined => {
  const kept = Object.fromEntries(Object.entries(origin).filter(([, v]) => v !== undefined)) as HitOrigin
  return Object.keys(kept).length > 0 ? kept : undefined
}

const memoryOrigin = (m: RecallMemory | undefined) => {
  const meta = m?.metadata ?? {}
  const writer = text(meta.written_by)
  return compact({
    date: text(meta.date) ?? text(m?.created_at)?.slice(0, 10),
    writer: writer === SHARED_KEY ? undefined : writer,
    source: text(meta.source),
    model: text(meta.model),
    agent: text(meta.agent),
    host: text(meta.host),
    project: text(meta.project),
    session: text(meta.session)?.slice(0, 8),
  })
}

const knowledgeOrigin = (k: KnowledgeHit) => {
  const meta = k.metadata ?? {}
  const writer = text(meta.written_by)
  return compact({
    date: (text(meta.updated) ?? text(meta.date))?.slice(0, 10),
    writer: writer === SHARED_KEY ? undefined : writer,
    source: text(k.author) ?? text(meta.source),
    agent: text(meta.agent),
    host: text(meta.host),
    project: text(meta.project),
    session: text(meta.session)?.slice(0, 8),
    url: text(k.source_url),
  })
}

// --- capture ----------------------------------------------------------------

// The shell capture's prompt (cmd/cortexdb-mcp-stdio/capture_session.go), so a
// memory reads the same whichever wrote it, plus what this session already gave.
type Graded = { grade: 'verified' | 'asserted'; grade_why?: string }

// The grade a captured memory is saved with, as the shell capture's
// capturedGrade: verified that names no check is saved as asserted, since
// the store refuses it and a claim nobody can point to is what asserted
// means; a preference or a decision, and any other word, is left untagged.
const capturedGrade = (m: { type?: string; grade?: string; grade_why?: string }): Graded | undefined => {
  if (m.type === 'preference' || m.type === 'decision') return undefined
  const why = scrubForCapture((m.grade_why ?? '').trim())
  switch ((m.grade ?? '').trim().toLowerCase()) {
    case 'verified':
      return why ? { grade: 'verified', grade_why: why } : { grade: 'asserted' }
    case 'asserted':
      return why ? { grade: 'asserted', grade_why: why } : { grade: 'asserted' }
  }
  return undefined
}

const CAPTURE_SYSTEM = `You distil a coding-session transcript into durable memories for a long-term store shared across future sessions.

Extract ONLY things worth knowing weeks later: decisions made and why, facts established (deployments, topology, versions, credentials locations — not values), user preferences expressed, outcomes and their root causes, lessons that changed how something is done.

Rules:
- One self-contained fact per memory. Never bundle; a reader sees one memory alone.
- Each content must make sense with zero session context: name the project and subject explicitly, use absolute dates.
- Write each memory in the language the conversation used for that topic.
- Skip: transient debugging steps, anything superseded within the session, tool chatter, politeness, plans that were replaced.
- 0 to 8 memories. An uneventful stretch yields zero — that is a good answer.
- The transcript is the part of the session not yet captured; memories already written from earlier parts are listed. Reuse one's slug only to replace it with a corrected or extended version; never repeat one.
- slug: short kebab-case ascii. importance: 0.3 routine fact, 0.6 useful decision, 0.85+ hard-won lesson or standing preference.
- entities: the concrete named things (projects, hosts, services, people) each memory is about.
- When a memory is about specific files listed under FILES, add each of those files as an entity of type "file", named exactly as listed. Leave out files a memory is not about; a memory about none has no file entities.
- grade, for a fact or a lesson: "verified" only when the transcript shows it was checked — a command's output, a test that passed or failed, a bug reproduced, a measurement — and grade_why then names that check in a few words; "asserted" when it was not checked: a suspected root cause, a fix nobody tested, a guess about how something works. Write an asserted memory so its content says it is unverified ("probably", "suspected", "not tested"). A guess the session later confirmed is one verified memory, not a guess beside a fact. Leave grade empty for a preference or a decision: it records what someone said or chose, and there is nothing to check.

Respond with JSON only: {"memories":[{"slug":"...","content":"...","importance":0.6,"type":"fact|decision|preference|lesson","grade":"verified|asserted|","grade_why":"...","entities":[{"name":"...","type":"..."}]}]}`

type CapturedReply = {
  memories?: {
    slug?: string
    content?: string
    importance?: number
    type?: string
    grade?: string
    grade_why?: string
    entities?: { name?: string; type?: string }[]
  }[]
}

// --- retiring what a capture made untrue -------------------------------------

// The shell capture's step (cmd/cortexdb-mcp-stdio/capture_supersede.go), with
// the same prompt and the same limits: each new memory is looked up, and
// haiku says which older memories it makes untrue. Those are saved as
// superseded — kept, linked forward, no longer recalled as current — so a
// wrong call is undone by clearing one key. The later date wins.
const CANDIDATES_PER_MEMORY = 4
const CANDIDATE_LIMIT = 24
const MAX_SUPERSEDES = 5

const SUPERSEDE_SYSTEM = `You maintain an AI agent's long-term memory. A session just produced NEW memories. EXISTING memories are already stored, each with the date it was recorded.

Decide, for each NEW memory, whether it makes an EXISTING memory untrue: the same thing, with a different current value (a host moved, a version changed, a decision was reversed, a preference changed, a plan was dropped, something "not done yet" is now done). The memory dated later wins:
- "retire": a NEW memory replaces EXISTING memories dated on or before the session.
- "outdated": a NEW memory is itself contradicted by an EXISTING memory dated after the session; it should not be saved.

Be strict. Being about the same topic is not enough; adding detail is not a contradiction; two facts that can both be true are not one. When unsure, leave it out — an outdated memory left in place is a smaller harm than a true one retired.

Name memories exactly as listed: a NEW memory by its "new" name, an EXISTING one by its "old" id — never by a date.

Respond with JSON only: {"retire":[{"new":"<new name>","old":["<old id>"],"reason":"..."}],"outdated":[{"new":"<new name>","by":"<old id>"}]}`

type Fresh = { slug: string; content: string; entities: { name: string; type: string }[] }
type Candidate = { id: string; content: string; date: string }
type Retirement = { retire: Map<string, string[]>; reason: Map<string, string>; outdated: Set<string> }

const noRetirement = (): Retirement => ({ retire: new Map(), reason: new Map(), outdated: new Set() })

// A memory speaks for the day of its session when it says, else the day it
// was stored. One with neither is never compared: no one can say which is later.
const dateOf = (memory: { metadata?: { date?: unknown }; created_at?: string }) => {
  const date = typeof memory.metadata?.date === 'string' ? memory.metadata.date : memory.created_at ?? ''
  return /^\d{4}-\d{2}-\d{2}/.test(date) ? date.slice(0, 10) : ''
}

async function candidatesFor($: EngineInterface, fresh: Fresh[], ownPrefix: string) {
  const seen = new Set<string>()
  const out: Candidate[] = []
  for (const m of fresh) {
    let payload: RecallPayload
    try {
      payload = (await callTool($, 'knowledge_memory_recall', {
        query: clip(m.content, 400),
        entity_names: m.entities.map(e => e.name),
        top_k_memories: CANDIDATES_PER_MEMORY,
        disable_knowledge: true,
        graph_light: true,
      })) as RecallPayload
    } catch {
      continue // finding nothing to retire is the safe failure
    }
    for (const hit of payload.memories ?? []) {
      const id = hit.memory?.id ?? ''
      const date = hit.memory ? dateOf(hit.memory) : ''
      // This session's own memories are rewritten by slug, not superseded.
      if (id === '' || date === '' || seen.has(id) || id.startsWith(ownPrefix)) continue
      seen.add(id)
      out.push({ id, content: hit.memory?.content ?? '', date })
      if (out.length >= CANDIDATE_LIMIT) return out
    }
  }
  return out
}

type RetireReply = {
  retire?: { new?: string; old?: unknown; reason?: string }[]
  outdated?: { new?: string; by?: string }[]
}

// Models embellish names ("later-deploy (2026-11-02)"); read back only the
// names that were given, never one made up or a date.
const nameIn = (said: string, names: string[]) => {
  const s = said.trim()
  let best = ''
  for (const n of names) {
    if (s === n) return n
    if (n.length > best.length && (s.startsWith(`${n} `) || s.startsWith(`${n}(`) || s.includes(`"${n}"`))) best = n
  }
  return best
}

async function planRetirement($: EngineInterface, fresh: Fresh[], ownPrefix: string, session: string) {
  const plan = noRetirement()
  const candidates = await candidatesFor($, fresh, ownPrefix)
  if (fresh.length === 0 || candidates.length === 0) return plan
  const prompt = [
    `The session is dated ${session}.`,
    '',
    'NEW memories:',
    ...fresh.map(m => `- new ${JSON.stringify(m.slug)}: ${clip(m.content, 400)}`),
    '',
    'EXISTING memories:',
    ...candidates.map(c => `- old ${JSON.stringify(c.id)}, recorded ${c.date}: ${clip(c.content.trim(), 400)}`),
  ].join('\n')
  const reply = await $.model.complete({ model: PLANNER, system: SUPERSEDE_SYSTEM, prompt, maxTokens: 1500, timeoutMs: 60 * 1000 })
  if (!reply.isAnswered) return plan
  const out = parseJSON<RetireReply>(reply.text)
  if (!out) return plan

  const slugs = fresh.map(m => m.slug)
  const ids = candidates.map(c => c.id)
  const byId = new Map(candidates.map(c => [c.id, c]))
  for (const o of out.outdated ?? []) {
    const slug = nameIn(o.new ?? '', slugs)
    const later = byId.get(nameIn(o.by ?? '', ids))
    if (slug && later && later.date > session) plan.outdated.add(slug)
  }
  let budget = MAX_SUPERSEDES
  const retired = new Set<string>()
  for (const r of out.retire ?? []) {
    const slug = nameIn(r.new ?? '', slugs)
    if (!slug || plan.outdated.has(slug)) continue
    const chosen = strings(r.old, CANDIDATE_LIMIT)
      .map(said => nameIn(said, ids))
      .filter(id => {
        const c = byId.get(id)
        if (!c || retired.has(id) || c.date > session || budget === 0) return false
        retired.add(id)
        budget -= 1
        return true
      })
    if (chosen.length === 0) continue
    plan.retire.set(slug, [...(plan.retire.get(slug) ?? []), ...chosen])
    if (r.reason?.trim()) plan.reason.set(slug, clip(r.reason.trim(), 300))
  }
  return plan
}

type Message = { role: string; text: string; toolUses: readonly unknown[] }

const keyOf = (m: Message) => `${m.role}:${m.toolUses.length}:${m.text.slice(0, 160)}`

async function capture($: EngineInterface) {
  if (isCapturing || (await sentinel($, 'autocapture'))?.startsWith('off')) return
  isCapturing = true
  try {
    const sessionId = await $.session.id()
    const history = await $.session.messages()
    if (!Array.isArray(history) || history.length === 0) return
    const kept = await read($, captureMark)
    const mark: CaptureMark = kept?.sessionId === sessionId ? kept : { sessionId, lastKey: '', memories: [] }
    const from = mark.lastKey === '' ? -1 : history.map(keyOf).lastIndexOf(mark.lastKey)
    const fresh = history.slice(from + 1)

    let root = ''
    try {
      root = await $.session.root()
    } catch {
      // A build without session.root, or one that cannot name it: the digest
      // keeps absolute paths.
    }
    const { digest, userTurns } = digestOf(fresh, root)
    const home = await $.env.get('HOME')
    const project = root && root !== home ? root.split('/').filter(Boolean).at(-1) : undefined
    if (userTurns < CAPTURE_MIN_USER_TURNS) return

    const already = mark.memories.map(m => `- ${m.slug}: ${clip(m.content, 300)}`).join('\n')
    const reply = await $.model.complete({
      model: PLANNER,
      system: CAPTURE_SYSTEM,
      prompt: `Today is ${today()}.\n\nAlready captured from this session:\n${already || '(none)'}\n\nTranscript not yet captured:\n${digest}`,
      maxTokens: 3000,
      timeoutMs: 90 * 1000,
    })
    if (!reply.isAnswered) return
    const out = parseJSON<CapturedReply>(reply.text)
    if (!out) return

    // Stable per session and slug, as the shell capture's: capturing again
    // replaces a memory instead of stacking a second copy.
    const ownPrefix = `auto:${sessionId.slice(0, 8)}:`
    const drafts: (Fresh & { importance?: number; type?: string; grade?: Graded })[] = []
    for (const m of out.memories ?? []) {
      const content = (m.content ?? '').trim()
      const slug = slugOf(m.slug || content)
      if (content === '' || slug === '') continue
      const entities = (m.entities ?? [])
        .map(entity => ({ name: (entity.name ?? '').trim(), type: entity.type ?? '' }))
        .filter(entity => entity.name !== '')
      drafts.push({ slug, content, entities, importance: m.importance, type: m.type, grade: capturedGrade(m) })
    }
    // Failing here saves everything and retires nothing, as capture always did.
    const plan = await planRetirement($, drafts, ownPrefix, today()).catch(noRetirement)

    const written: CapturedMemory[] = []
    let retiredCount = 0
    for (const m of drafts) {
      if (plan.outdated.has(m.slug)) continue
      const supersedes = plan.retire.get(m.slug) ?? []
      const reason = plan.reason.get(m.slug)
      await callTool($, 'memory_save', {
        memory_id: ownPrefix + m.slug,
        scope: 'global',
        content: m.content,
        importance: Math.min(1, Math.max(0, m.importance ?? 0.5)),
        metadata: {
          source: 'auto-capture',
          agent: 'claude-code',
          session: sessionId,
          ...(project ? { project } : {}),
          ...(host ? { host } : {}),
          date: today(),
          type: m.type || 'fact',
          model: PLANNER,
          ...(reason ? { supersede_reason: reason } : {}),
        },
        entities: m.entities,
        ...(supersedes.length > 0 ? { supersedes } : {}),
        ...(m.grade ?? {}),
      })
      written.push({ slug: m.slug, content: m.content })
      retiredCount += supersedes.length
    }

    const end = fresh.at(-1)
    const memories = [...mark.memories.filter(m => !written.some(w => w.slug === m.slug)), ...written]
    await update($, captureMark, () => ({ sessionId, lastKey: end ? keyOf(end) : mark.lastKey, memories }))
    if (written.length > 0) $.ui.toast(t().captured(written.length, retiredCount))
  } catch (err) {
    $.ui.log(`cortexdb-live: capture failed: ${messageOf(err)}`)
  } finally {
    isCapturing = false
  }
}

function scheduleCapture($: EngineInterface) {
  idleTimer?.cancel()
  idleTimer = $.clock.after(CAPTURE_IDLE_MS, () => void capture($))
}

// --- hooks ------------------------------------------------------------------

export const register: Register = (on, options) => {
  on('session.start', async ($, e, next) => {
    lang = await resolveLang($, options)
    const remote = await $.env.get('CORTEXDB_REMOTE')
    const path = await $.env.get('CORTEXDB_PATH')
    brain = { where: remote ? remote.replace(/:\d+$/, '') : path ? path.split('/').pop() : undefined }
    // The plugin's shell hooks stand down for a session this module answers.
    await $.env.set('CORTEXDB_RECALL_BY_MOD', '1')
    await $.env.set('CORTEXDB_CAPTURE_BY_MOD', '1')
    await $.command.register({ name: SHOW_COMMAND, description: t().showCommand })
    const named = await $.process.run(['hostname', '-s']).catch(() => undefined)
    host = named?.exitCode === 0 ? named.stdout.trim() || undefined : undefined
    void refreshStats($)
    $.clock.every(STATS_EVERY_MS, () => void refreshStats($))
    return next(e)
  })

  // Hide lasts until the next recall; this brings back the one hidden now.
  on('command.run', { command: SHOW_COMMAND }, async $ => {
    const found = await read($, last)
    if (!found || found.hits.length === 0) return { text: t().nothingShown }
    await update($, isHidden, () => false)
    await update($, isOpen, () => true)
    return { text: t().shown }
  })

  on('classic.UserPromptSubmit', async ($, e, next) => {
    const prompt = e.prompt.trim()
    const asked =
      prompt !== '' && (await sentinel($, 'autorecall'))?.startsWith('on')
        ? Promise.race([
            recall($, prompt).catch(err => {
              brain = { ...brain, error: isDenied(err) ? t().denied(TOOLS_TO_ALLOW) : `${t().recallFailed}: ${messageOf(err)}` }
              return undefined
            }),
            $.clock.sleep(RECALL_BUDGET_MS).then(() => undefined),
          ])
        : Promise.resolve(undefined)

    const [result, found] = await Promise.all([next(e), asked])
    if (!found) {
      showStatus($)
      return result
    }

    const others = (result.additionalContext ?? []).filter(text => !text.startsWith(SHELL_HEADER))
    if (found === 'skip') {
      brain = { ...brain, error: undefined, skipped: true }
      showStatus($)
      await update($, last, () => null)
      return { ...result, additionalContext: others }
    }

    brain = { ...brain, error: undefined, skipped: false, recallMs: found.ms, degraded: found.degraded }
    if (brain.nodes === undefined) void refreshStats($)
    showStatus($)
    await update($, last, () => found)
    await update($, isHidden, () => false)
    const mine = contextOf(found.hits)
    return { ...result, additionalContext: mine ? [...others, mine] : others }
  }).catch(($, e, next) => next(e))

  on('turn.complete', ($, e, next) => {
    if (e.agentId === undefined) scheduleCapture($)
    return next(e)
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const found = await read($, last)
    if (e.props.hasSurvey || !found || found.hits.length === 0 || (await read($, isHidden))) {
      return next(e)
    }

    const { Box, Button, Text } = $.ui.resolve(e)
    const s = t()
    const open = await read($, isOpen)
    const memories = found.hits.filter(hit => hit.kind === 'memory').length
    const knowledge = found.hits.length - memories
    const plan = found.plan

    return (
      <Box flexDirection="column">
        <Box flexDirection="row" gap={1}>
          <Text>
            {s.recalled(found.hits.length)}
            <Text dimColor>{s.detail(memories, knowledge, (found.ms / 1000).toFixed(1))}</Text>
          </Text>
          <Button key="toggle" plain label={open ? s.collapse : s.expand} onPress={() => update($, isOpen, v => !v)} />
          <Button key="hide" plain label={s.hide} onPress={() => update($, isHidden, () => true)} />
        </Box>
        {open && (
          <Text wrap="truncate-end" dimColor>
            {'  '}
            {s.query} ({plan.by}): {plan.keywords.join(' · ')}
            {plan.entities.length > 0 ? ` | ${s.entities}: ${plan.entities.join(', ')}` : ''}
          </Text>
        )}
        {open && plan.query && (
          <Text wrap="truncate-end" dimColor>
            {'  '}
            {plan.project ? `${s.project} ${plan.project} · ` : ''}
            {oneLine(plan.query)}
          </Text>
        )}
        {open &&
          found.hits.flatMap(hit => {
            const explained = explainOf(hit, s)
            return [
              <Text wrap="truncate-end" dimColor>
                {'  '}
                {hit.kind === 'memory' ? s.memory : s.knowledge} <Text bold>{hit.title}</Text> {hit.snippet}
              </Text>,
              ...(explained
                ? [
                    <Text wrap="truncate-end" dimColor>
                      {'      '}
                      {explained}
                    </Text>,
                  ]
                : []),
            ]
          })}
      </Box>
    )
  })
}

// --- text -------------------------------------------------------------------

type RecallMemory = { id?: string; content?: string; metadata?: Record<string, unknown>; created_at?: string; grade?: string; grade_why?: string }

type KnowledgeHit = {
  knowledge_id?: string
  title?: string
  snippet?: string
  author?: string
  source_url?: string
  vector_score?: number
  lexical_score?: number
  metadata?: Record<string, string>
}

type MemoryHit = { memory?: RecallMemory; score?: number; similarity?: number; via?: string[] }

type RecallPayload = {
  // What did not run: "embedder_unreachable" means keyword search only.
  degraded?: string[]
  memory_decision?: { effective_mode?: string }
  knowledge_decision?: { effective_mode?: string }
  memories?: MemoryHit[]
  knowledge?: KnowledgeHit[]
  results?: KnowledgeHit[]
}

// The text the model reads: the same block the shell hook writes, so an agent
// sees one thing whichever of the two answered.
// Cut whole lines from the end under the budget the shell hook keeps
// (hook_budget.go): an oversized block reaches the model as a short preview.
const CONTEXT_BUDGET = 9000
const contextOf = (hits: RecallHit[]) => {
  if (hits.length === 0) return ''
  const header = `${SHELL_HEADER} (retrieved automatically — verify before relying on them):`
  const footer = '(If this exchange states a durable preference, decision, or fact, save it with memory_save / knowledge_save.)'
  const all = hits.map(hit => {
    const trail = trailOf(hit)
    return `- ${hit.title}${trail ? ` [${trail}]` : ''}: ${hit.snippet}`
  })
  let kept = all
  let note = ''
  const size = (parts: string[]) => parts.reduce((n, p) => n + [...p].length + 1, 0)
  while (kept.length > 0 && size([header, footer, note, ...kept]) > CONTEXT_BUDGET) {
    kept = kept.slice(0, -1)
    note = `(${all.length - kept.length} more not shown here; ask CortexDB for them with knowledge_memory_recall.)`
  }
  return [header, ...kept, ...(note ? [note] : []), footer].join('\n')
}

// Where a hit came from and why it came back, as one bracket the model can
// weigh before relying on it: the date, who wrote it, from which session and
// project, and the retriever's score. The same words the shell hook writes.
// What a trail says first about how a hit is known: a guess before anything
// else, the check behind a verified hit, nothing for an untagged one. The
// shell hook's gradeMark (recall_remote.go) writes the same words.
const gradeMark = (grade?: string, why?: string) => {
  if (!grade) return undefined
  if (grade === 'asserted' || grade === 'held') return `UNVERIFIED (${grade})`
  if (grade === 'verified') return why ? `verified: ${[...why.trim()].slice(0, 80).join('')}` : 'verified'
  return grade
}

const trailOf = (hit: RecallHit) => {
  const o = hit.origin ?? {}
  const by = [o.agent, o.source, o.model ? `(${o.model})` : undefined].filter(Boolean).join(' ')
  const parts = [
    gradeMark(hit.grade, hit.gradeWhy),
    o.date,
    by || undefined,
    o.writer ? `key ${o.writer}` : undefined,
    o.host ? `host ${o.host}` : undefined,
    o.project ? `project ${o.project}` : undefined,
    o.session ? `session ${o.session}` : undefined,
    o.url,
    hit.why ? scoreOf(hit.why, VIA_EN) : undefined,
  ]
  return parts.filter(Boolean).join(' · ')
}

const VIA_EN = STRINGS.en.via

const scoreOf = (why: HitWhy, via: Record<HitWhy['via'], string>) => {
  const n = (v: number) => v.toFixed(2)
  if (why.via === 'hybrid' && why.vector !== undefined && why.lexical !== undefined) {
    return `${via.semantic} ${n(why.vector)} + ${via.keyword} ${n(why.lexical)}`
  }
  if (why.via === 'hybrid' && why.vector !== undefined) return `${via.semantic} ${n(why.vector)} + ${via.keyword}`
  const score = why.vector ?? why.lexical
  return score === undefined ? via[why.via] : `${via[why.via]} ${n(score)}`
}

// The band's second line under a hit: why it came back, then where from.
const explainOf = (hit: RecallHit, s: (typeof STRINGS)[Lang]) => {
  const why = hit.why
    ? [scoreOf(hit.why, s.via), hit.why.matched.length > 0 ? `${s.matched} ${hit.why.matched.join(' / ')}` : undefined]
        .filter(Boolean)
        .join(' · ')
    : ''
  const o = hit.origin ?? {}
  const from = [
    [o.agent, o.source, o.model ? `(${o.model})` : undefined].filter(Boolean).join(' ') || undefined,
    o.writer ? `${s.key} ${o.writer}` : undefined,
    o.host,
    o.project ? `${s.project} ${o.project}` : undefined,
    o.session ? `${s.session} ${o.session}` : undefined,
    o.url,
    o.date,
  ]
    .filter(Boolean)
    .join(' · ')
  const graded =
    hit.grade === 'asserted' || hit.grade === 'held'
      ? s.unverified(hit.grade)
      : hit.grade === 'verified'
        ? s.verified(hit.gradeWhy)
        : ''
  return [graded, why ? `${s.why} ${why}` : '', from ? `${s.from} ${from}` : ''].filter(Boolean).join('  ')
}

// The shell capture's digestTranscript: what the person typed and what the
// assistant said, injected machinery stripped, head kept and the tail favoured.
const NOISE =
  /<(system-reminder|local-command-caveat|command-name|command-message|command-args|local-command-stdout|task-notification)>[\s\S]*?<\/\1>|\[SYSTEM NOTIFICATION[^\]]*\][\s\S]*/g

// The shell capture's scrubForCapture (cmd/cortexdb-mcp-stdio/scrub.go): text
// between <private> tags never reaches the model, and credentials in the shapes
// they are issued in become [REDACTED] before anything is distilled or stored.
const PRIVATE = /<private>[\s\S]*?(?:<\/private>|$)/gi
const SECRETS = [
  /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)/g,
  /\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}/g,
  /\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}/g,
  /\bgithub_pat_[A-Za-z0-9_]{22,}/g,
  /\bglpat-[A-Za-z0-9_-]{20,}/g,
  /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/g,
  /\bxox[abprs]-[A-Za-z0-9-]{10,}/g,
  /\bAIza[0-9A-Za-z_-]{35}/g,
  /\bnpm_[A-Za-z0-9]{36}/g,
  /\bBearer\s+[A-Za-z0-9._~+/=-]{20,}/gi,
]
const ASSIGNED_SECRET =
  /\b([A-Za-z0-9_]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)[A-Za-z0-9_]*)(\s*[:=]\s*)("[^"\s]{6,}"|'[^'\s]{6,}'|[^\s"',;]{6,})/gi
const scrubForCapture = (text: string) =>
  SECRETS.reduce((t, re) => t.replace(re, '[REDACTED]'), text.replace(PRIVATE, '')).replace(ASSIGNED_SECRET, '$1$2[REDACTED]')

// The files a session read and modified, named from the project root, as the
// shell capture's FILES block (cmd/cortexdb-mcp-stdio/files.go): the model
// links each memory to the ones it is about.
const FILE_TOOLS = new Set(['Read', 'Edit', 'Write', 'MultiEdit', 'NotebookEdit'])
const EDIT_TOOLS = new Set(['Edit', 'Write', 'MultiEdit', 'NotebookEdit'])
const filesOf = (messages: readonly Message[], root: string) => {
  const modified: string[] = []
  const read: string[] = []
  const prefix = root.endsWith('/') ? root : `${root}/`
  for (const m of messages) {
    for (const use of m.toolUses as { tool?: string; input?: Record<string, unknown> }[]) {
      const tool = use?.tool ?? ''
      const raw = use?.input?.file_path ?? use?.input?.notebook_path
      if (!FILE_TOOLS.has(tool) || typeof raw !== 'string' || raw === '') continue
      const name = root && raw.startsWith(prefix) ? raw.slice(prefix.length) : raw
      const list = EDIT_TOOLS.has(tool) ? modified : read
      if (!list.includes(name) && modified.length + read.length < 40) list.push(name)
    }
  }
  const onlyRead = read.filter(f => !modified.includes(f)).sort()
  if (modified.length + onlyRead.length === 0) return ''
  return [
    'FILES (paths from the repository root)',
    ...(modified.length ? [`modified: ${modified.join(', ')}`] : []),
    ...(onlyRead.length ? [`read: ${onlyRead.join(', ')}`] : []),
    '',
    '',
  ].join('\n')
}

const digestOf = (messages: readonly Message[], root = '') => {
  const lines: string[] = []
  let userTurns = 0
  for (const m of messages) {
    if (m.role === 'user') {
      const text = scrubForCapture(m.text.replace(NOISE, '')).trim()
      if (text === '') continue
      userTurns += 1
      lines.push(`USER: ${clip(text, 2000)}`)
    } else {
      const text = scrubForCapture(m.text).trim()
      if (text !== '') lines.push(`ASSISTANT: ${clip(text, 1200)}`)
    }
  }
  const head = 6000
  const tail = 22000
  const all = lines.join('\n')
  const body = all.length > head + tail ? `${all.slice(0, head)}\n[...trimmed...]\n${all.slice(-tail)}` : all
  return { digest: filesOf(messages, root) + body, userTurns }
}

// Models fence JSON on a whim and the odd reply loses its last closer.
function parseJSON<T>(raw: string): T | undefined {
  const start = raw.indexOf('{')
  const end = raw.lastIndexOf('}')
  if (start < 0) return undefined
  const body = raw.slice(start, end > start ? end + 1 : undefined)
  for (const candidate of [body, `${body}}`, `${body}]}`, `${body}}]}`]) {
    try {
      return JSON.parse(candidate) as T
    } catch {
      // try the next repair
    }
  }
  return undefined
}

const strings = (list: unknown, max: number) =>
  Array.isArray(list) ? list.filter((s): s is string => typeof s === 'string' && s.trim() !== '').slice(0, max) : []

const oneLine = (text: string) => text.split(/\s+/).filter(Boolean).join(' ')

const snippetOf = (content: string) => clip(content.split(/\s+/).filter(Boolean).join(' '), SNIPPET)

const clip = (text: string, max: number) => {
  const chars = Array.from(text)
  return chars.length <= max ? text : `${chars.slice(0, max).join('').trim()}…`
}

const slugOf = (text: string) =>
  text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60)

const today = () => new Date().toISOString().slice(0, 10)

const messageOf = (err: unknown) => String((err as Error)?.message ?? err).slice(0, 80)

// A call the module makes goes through the session's permission rules like
// the model's, and a hook has no prompt to ask with: without an allow rule the
// call is denied, which says nothing about the brain itself.
const isConnecting = (err: unknown) => /no connected MCP tool/i.test(String((err as Error)?.message ?? err))

const isDenied = (err: unknown) => /refused|denied|permission/i.test(String((err as Error)?.message ?? err))

// The tools this module calls, as permissions.allow spells them.
const TOOLS_TO_ALLOW = ['knowledge_memory_recall', 'graph_statistics', 'memory_save']
  .map(tool => `mcp__plugin_cortexdb_cortexdb__${tool}`)
  .join(', ')

// The shell hook's keywordsFromPrompt: letter and digit runs, lowercased,
// deduped, two characters or more; a CJK run stays one token.
const keywordsOf = (prompt: string) => {
  const seen = new Set<string>()
  for (const word of prompt.toLowerCase().split(/[^\p{L}\p{N}]+/u)) {
    if (Array.from(word).length >= 2) seen.add(word)
  }
  return [...seen]
}
