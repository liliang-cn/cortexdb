import { atom, read, update } from 'claude-code'
import type { EngineInterface, PluginOptions, Register } from 'claude-code'

import type { CaptureMark, CapturedMemory, Recall, RecallHit, RecallPlan } from '../types'

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
    nodes: (n: string) => `${n} 节点`,
    recallTime: (s: string) => `召回 ${s}s`,
    skipped: '这条不用召回',
    unreachable: '连不上',
    recallFailed: '召回失败',
    denied: (tools: string) => `没有调用 CortexDB 工具的权限，请在 settings.json 的 permissions.allow 里允许：${tools}`,
    local: '本地大脑',
    captured: (n: number) => `CortexDB 记下了 ${n} 条新记忆`,
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
    nodes: (n: string) => `${n} nodes`,
    recallTime: (s: string) => `recall ${s}s`,
    skipped: 'no recall needed',
    unreachable: 'unreachable',
    recallFailed: 'recall failed',
    denied: (tools: string) => `not allowed to call CortexDB tools; allow them in settings.json permissions.allow: ${tools}`,
    local: 'local brain',
    captured: (n: number) => `CortexDB saved ${n} new memories`,
    showCommand: "Show what CortexDB recalled for the last prompt again (after Hide)",
    shown: 'The recall is shown above the prompt again.',
    nothingShown: 'The last prompt recalled nothing.',
  },
}

type Lang = keyof typeof STRINGS
type Brain = { where?: string; nodes?: number; error?: string; recallMs?: number; skipped?: boolean }

let server: string | undefined
let lang: Lang = 'en'
let brain: Brain = {}
let idleTimer: { cancel: () => void } | undefined
let isCapturing = false

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

async function callTool($: EngineInterface, tool: string, args: Record<string, unknown>) {
  const result = await $.mcp.call(await serverOf($), tool, args)
  const text = result.content.find(block => block.type === 'text')?.text ?? ''
  if (result.isError) throw new Error(text.slice(0, 200) || `${tool} failed`)
  return JSON.parse(text) as unknown
}

function showStatus($: EngineInterface) {
  const parts = [`🧠 ${brain.where ?? t().local}`]
  if (brain.error) parts.push(`⚠ ${brain.error}`)
  else {
    if (brain.nodes !== undefined) parts.push(t().nodes(brain.nodes.toLocaleString('en-US')))
    if (brain.skipped) parts.push(t().skipped)
    else if (brain.recallMs !== undefined) parts.push(t().recallTime((brain.recallMs / 1000).toFixed(1)))
  }
  $.ui.status(parts.join(' · '))
}

async function refreshStats($: EngineInterface) {
  try {
    const stats = (await callTool($, 'graph_statistics', {})) as { node_count?: number }
    brain = { ...brain, nodes: stats.node_count, error: undefined }
  } catch (err) {
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

Given the user's new message (and the assistant's previous reply for context), answer with JSON only:
{"skip":false,"query":"...","keywords":["..."],"alternate_queries":["..."],"entity_names":["..."],"retrieval_mode":"auto"}

- skip: true only when the message needs nothing remembered: a bare acknowledgement or go-ahead ("ok", "好", "发", "继续", "👌") whose subject the previous reply already settles.
- query: the message restated as a self-contained lookup.
- keywords: up to 12 terms the stored text would contain: names, project and host names, versions, abbreviations, synonyms, and BOTH Chinese and English forms of each concept.
- alternate_queries: up to 3 other phrasings.
- entity_names: up to 6 exact names of concrete things (projects, services, hosts, people, tools).
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

async function planRecall($: EngineInterface, prompt: string): Promise<Planned> {
  const started = await $.clock.now()
  const lexical = keywordsOf(prompt)
  const fallback: Planned = {
    args: { query: prompt, keywords: lexical },
    plan: { by: 'lexical', keywords: lexical, entities: [], mode: 'auto', ms: 0 },
    skip: false,
  }

  const history = await $.session.messages().catch(() => [])
  const previous = Array.isArray(history)
    ? history.filter(m => m.role === 'assistant' && m.text.trim() !== '').at(-1)?.text
    : undefined
  const reply = await $.model.complete({
    model: PLANNER,
    system: PLAN_SYSTEM,
    prompt: `${previous ? `Previous assistant reply:\n${clip(previous, 800)}\n\n` : ''}New user message:\n${clip(prompt, 2000)}`,
    maxTokens: 500,
    timeoutMs: PLAN_TIMEOUT_MS,
  })
  if (!reply.isAnswered) return fallback
  const planned = parseJSON<PlanReply>(reply.text)
  if (!planned) return fallback

  const keywords = [...new Set([...strings(planned.keywords, 12), ...lexical])].slice(0, 24)
  const entities = strings(planned.entity_names, 6)
  const mode = planned.retrieval_mode === 'graph' || planned.retrieval_mode === 'lexical' ? planned.retrieval_mode : 'auto'
  return {
    args: {
      query: typeof planned.query === 'string' && planned.query.trim() !== '' ? planned.query : prompt,
      keywords,
      alternate_queries: strings(planned.alternate_queries, 3),
      entity_names: entities,
      retrieval_mode: mode,
    },
    plan: { by: 'haiku', keywords: strings(planned.keywords, 12), entities, mode, ms: (await $.clock.now()) - started },
    skip: planned.skip === true,
  }
}

async function recall($: EngineInterface, prompt: string): Promise<Recall | 'skip'> {
  const started = await $.clock.now()
  const { args, plan, skip } = await planRecall($, prompt)
  if (skip) return 'skip'
  const payload = (await callTool($, 'knowledge_memory_recall', {
    ...args,
    top_k_memories: TOP_K,
    top_k_knowledge: TOP_K,
    graph_light: true,
  })) as RecallPayload
  const hits: RecallHit[] = [
    ...(payload.memories ?? []).map(m => ({
      kind: 'memory' as const,
      title: m.memory?.id ?? '',
      snippet: snippetOf(m.memory?.content ?? ''),
    })),
    ...(payload.knowledge ?? payload.results ?? []).map(k => ({
      kind: 'knowledge' as const,
      title: k.title || k.knowledge_id || '',
      snippet: snippetOf(k.snippet ?? ''),
    })),
  ].filter(hit => hit.snippet !== '')
  return { hits, ms: (await $.clock.now()) - started, plan }
}

// --- capture ----------------------------------------------------------------

// The shell capture's prompt (cmd/cortexdb-mcp-stdio/capture_session.go), so a
// memory reads the same whichever wrote it, plus what this session already gave.
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

Respond with JSON only: {"memories":[{"slug":"...","content":"...","importance":0.6,"type":"fact|decision|preference|lesson","entities":[{"name":"...","type":"..."}]}]}`

type CapturedReply = {
  memories?: {
    slug?: string
    content?: string
    importance?: number
    type?: string
    entities?: { name?: string; type?: string }[]
  }[]
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

    const { digest, userTurns } = digestOf(fresh)
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

    const short = sessionId.slice(0, 8)
    const written: CapturedMemory[] = []
    for (const m of out.memories ?? []) {
      const content = (m.content ?? '').trim()
      const slug = slugOf(m.slug || content)
      if (content === '' || slug === '') continue
      await callTool($, 'memory_save', {
        // Stable per session and slug, as the shell capture's: capturing again
        // replaces a memory instead of stacking a second copy.
        memory_id: `auto:${short}:${slug}`,
        scope: 'global',
        content,
        importance: Math.min(1, Math.max(0, m.importance ?? 0.5)),
        metadata: { source: 'auto-capture', session: sessionId, date: today(), type: m.type || 'fact', model: PLANNER },
        entities: (m.entities ?? [])
          .map(entity => ({ name: (entity.name ?? '').trim(), type: entity.type ?? '' }))
          .filter(entity => entity.name !== ''),
      })
      written.push({ slug, content })
    }

    const end = fresh.at(-1)
    const memories = [...mark.memories.filter(m => !written.some(w => w.slug === m.slug)), ...written]
    await update($, captureMark, () => ({ sessionId, lastKey: end ? keyOf(end) : mark.lastKey, memories }))
    if (written.length > 0) $.ui.toast(t().captured(written.length))
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

    brain = { ...brain, error: undefined, skipped: false, recallMs: found.ms }
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
        {open &&
          found.hits.map(hit => (
            <Text wrap="truncate-end" dimColor>
              {'  '}
              {hit.kind === 'memory' ? s.memory : s.knowledge} <Text bold>{hit.title}</Text> {hit.snippet}
            </Text>
          ))}
      </Box>
    )
  })
}

// --- text -------------------------------------------------------------------

type RecallPayload = {
  memories?: { memory?: { id?: string; content?: string } }[]
  knowledge?: { knowledge_id?: string; title?: string; snippet?: string }[]
  results?: { knowledge_id?: string; title?: string; snippet?: string }[]
}

// The text the model reads: the same block the shell hook writes, so an agent
// sees one thing whichever of the two answered.
const contextOf = (hits: RecallHit[]) => {
  if (hits.length === 0) return ''
  const lines = hits.map(hit => `- ${hit.title}: ${hit.snippet}`)
  return [
    `${SHELL_HEADER} (retrieved automatically — verify before relying on them):`,
    ...lines,
    '(If this exchange states a durable preference, decision, or fact, save it with memory_save / knowledge_save.)',
  ].join('\n')
}

// The shell capture's digestTranscript: what the person typed and what the
// assistant said, injected machinery stripped, head kept and the tail favoured.
const NOISE =
  /<(system-reminder|local-command-caveat|command-name|command-message|command-args|local-command-stdout|task-notification)>[\s\S]*?<\/\1>|\[SYSTEM NOTIFICATION[^\]]*\][\s\S]*/g

const digestOf = (messages: readonly Message[]) => {
  const lines: string[] = []
  let userTurns = 0
  for (const m of messages) {
    if (m.role === 'user') {
      const text = m.text.replace(NOISE, '').trim()
      if (text === '') continue
      userTurns += 1
      lines.push(`USER: ${clip(text, 2000)}`)
    } else if (m.text.trim() !== '') {
      lines.push(`ASSISTANT: ${clip(m.text, 1200)}`)
    }
  }
  const head = 6000
  const tail = 22000
  const all = lines.join('\n')
  const digest = all.length > head + tail ? `${all.slice(0, head)}\n[...trimmed...]\n${all.slice(-tail)}` : all
  return { digest, userTurns }
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
