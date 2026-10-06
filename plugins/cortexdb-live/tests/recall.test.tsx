import type { On } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const SHELL = 'Relevant CortexDB memories for this prompt (retrieved automatically — verify before relying on them):\n- old: from the shell hook'

const recallPayload = {
  memories: [{ memory: { id: 'pref-tabs', content: 'Alice   prefers\n tabs.' } }],
  knowledge: [{ knowledge_id: 'k1', title: 'Indent policy', snippet: 'Tabs in Go.' }],
}

const PLAN = {
  skip: false,
  query: 'What indentation does Alice prefer?',
  keywords: ['Alice', 'indentation', '缩进', 'tabs'],
  alternate_queries: ['Alice tabs or spaces'],
  entity_names: ['Alice'],
  retrieval_mode: 'auto',
}

type World = {
  autorecall?: string
  autocapture?: string
  plan?: object | 'fail'
  captured?: object
  // existing answers the recall a capture makes to find what it may retire.
  existing?: object
  retire?: object
  messages?: { role: 'user' | 'assistant'; text: string; toolUses: never[] }[]
  isBrainDown?: boolean
  isDenied?: boolean
  connectsAfter?: number
}

type Seen = {
  calls: { tool: string; args: Record<string, unknown> }[]
  status: string[]
  toasts: string[]
  prompts: string[]
  clock: ReturnType<typeof mock.clock>
}

const answered = (text: string) => ({
  value: {
    isAnswered: true as const,
    text,
    usage: { input_tokens: 1, output_tokens: 1, cache_creation_input_tokens: 0, cache_read_input_tokens: 0 },
  },
})

const world = (on: On, w: World): Seen => {
  const seen: Seen = { calls: [], status: [], toasts: [], prompts: [], clock: mock.clock(on) }
  mock.env(on, { HOME: '/home/a', CORTEXDB_REMOTE: '10.0.0.9:47821', LANG: 'en_US.UTF-8' })
  on('env.set', () => ({ value: undefined }))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('turn.complete', ($, e) => ({ text: e.answer }))
  on('command.register', () => ({ value: undefined as never }))
  on('fs.read', ($, e) => {
    const value = e.path.endsWith('autorecall') ? w.autorecall : w.autocapture
    if (value === undefined) throw new Error('ENOENT')
    return { value }
  })
  on('mcp.call', ($, e) => {
    seen.calls.push({ tool: e.tool, args: e.args })
    if ((w.connectsAfter ?? 0) > 0) {
      w.connectsAfter! -= 1
      return { deny: `$.mcp.call: no connected MCP tool "${e.tool}" on a server` }
    }
    if (w.isDenied) return { deny: 'Claude requested permissions to use this tool, but you have not granted it yet.' }
    if (w.isBrainDown) return { value: { content: [{ type: 'text', text: 'dial tcp: refused' }], isError: true } }
    const isCaptureLookup = e.tool === 'knowledge_memory_recall' && e.args.disable_knowledge === true
    const body =
      e.tool === 'graph_statistics'
        ? { node_count: 4493 }
        : e.tool === 'memory_save'
          ? { ok: true }
          : isCaptureLookup && w.existing
            ? w.existing
            : recallPayload
    return { value: { content: [{ type: 'text', text: JSON.stringify(body) }], isError: false } }
  })
  on('model.complete', ($, e) => {
    seen.prompts.push(e.prompt)
    if (e.system?.startsWith('You plan')) {
      if (w.plan === 'fail') return { value: { isAnswered: false, reason: 'api-error', status: 529, error: 'overloaded' } as never }
      return answered('```json\n' + JSON.stringify(w.plan ?? PLAN) + '\n```')
    }
    if (e.system?.startsWith('You maintain')) return answered(JSON.stringify(w.retire ?? { retire: [] }))
    return answered(JSON.stringify(w.captured ?? { memories: [] }))
  })
  on('session.messages', () => ({ value: (w.messages ?? []) as never }))
  on('session.id', () => ({ value: 'abcdef1234567890' }))
  on('config.list', () => ({ value: [] }))
  on('process.run', () => ({ value: { exitCode: 1, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }))
  on('ui.status', ($, e) => {
    seen.status.push(String(e.text))
    return { value: undefined }
  })
  on('ui.toast', ($, e) => {
    seen.toasts.push(e.text)
    return { value: undefined }
  })
  on('classic.UserPromptSubmit', () => ({ additionalContext: [SHELL, 'another plugin'] }))
  return seen
}

test('a haiku plan drives the recall, and its block replaces the shell one', async ($, on) => {
  const seen = world(on, { autorecall: 'on' })

  const out = await $.classic.UserPromptSubmit({ prompt: 'what does Alice prefer?' })

  const recall = seen.calls.find(c => c.tool === 'knowledge_memory_recall')
  expect(recall?.args.query).toBe('What indentation does Alice prefer?')
  expect(recall?.args.entity_names).toEqual(['Alice'])
  expect(recall?.args.alternate_queries).toEqual(['Alice tabs or spaces'])
  expect(recall?.args.keywords).toContain('缩进')
  expect(recall?.args.keywords).toContain('prefer')
  expect(out.additionalContext?.[0]).toBe('another plugin')
  const mine = out.additionalContext?.[1] ?? ''
  expect(mine).toContain('- pref-tabs: Alice prefers tabs.')
  expect(mine).toContain('- Indent policy: Tabs in Go.')
  expect(mine).not.toContain('from the shell hook')
  expect(seen.status.at(-1)).toMatch(/^🧠 .* · recall \d+\.\ds$/)
})

test('a failed plan falls back to the lexical keywords', async ($, on) => {
  const seen = world(on, { autorecall: 'on', plan: 'fail' })

  const out = await $.classic.UserPromptSubmit({ prompt: 'what does Alice prefer?' })

  const recall = seen.calls.find(c => c.tool === 'knowledge_memory_recall')
  expect(recall?.args.query).toBe('what does Alice prefer?')
  expect(recall?.args.keywords).toEqual(['what', 'does', 'alice', 'prefer'])
  expect(out.additionalContext?.[1]).toContain('pref-tabs')
})

test('a go-ahead haiku marks skip asks nothing and drops the shell block', async ($, on) => {
  const seen = world(on, { autorecall: 'on', plan: { skip: true } })

  const out = await $.classic.UserPromptSubmit({ prompt: '发' })

  expect(seen.calls.some(c => c.tool === 'knowledge_memory_recall')).toBe(false)
  expect(out.additionalContext).toEqual(['another plugin'])
  expect(seen.status.at(-1)).toContain('no recall needed')
})

test('auto-recall switched off leaves the prompt as the hooks beneath made it', async ($, on) => {
  const seen = world(on, { autorecall: 'off' })

  const out = await $.classic.UserPromptSubmit({ prompt: 'what does Alice prefer?' })

  expect(seen.calls.some(c => c.tool === 'knowledge_memory_recall')).toBe(false)
  expect(out.additionalContext).toEqual([SHELL, 'another plugin'])
})

test('a denied tool says which permissions to add, not that the brain is down', async ($, on) => {
  const seen = world(on, { autorecall: 'on', isDenied: true })

  const out = await $.classic.UserPromptSubmit({ prompt: 'what does Alice prefer?' })

  expect(out.additionalContext).toEqual([SHELL, 'another plugin'])
  expect(seen.status.at(-1)).toContain('permissions.allow: mcp__plugin_cortexdb_cortexdb__knowledge_memory_recall')
})

test('a server still connecting at start is asked again, not reported down', async ($, on) => {
  const seen = world(on, { autorecall: 'on', connectsAfter: 1 })
  await $.session.start({ cwd: '/w', surface: 'terminal' } as never)
  await seen.clock.advance(1)

  expect(seen.status.join('\n')).not.toContain('unreachable')
  await seen.clock.advance(5 * 1000)
  expect(seen.status.at(-1)).toBe('🧠 10.0.0.9 · 4,493 nodes')
})

test('a brain that fails keeps the shell block', async ($, on) => {
  world(on, { autorecall: 'on', isBrainDown: true })

  const out = await $.classic.UserPromptSubmit({ prompt: 'anything' })

  expect(out.additionalContext).toEqual([SHELL, 'another plugin'])
})

const props = { hasSurvey: false, isWorking: false, maxRows: 10, bodyColumns: 80 } as never

for (const surface of ['terminal', 'desktop'] as const) {
  test(`the band shows what was recalled and the plan on ${surface}`, async ($, on) => {
    world(on, { autorecall: 'on' })
    on('ui.render', { component: 'AbovePrompt' }, ($, e) => {
      const { Text } = $.ui.resolve(e)
      return <Text>engine</Text>
    })
    await $.classic.UserPromptSubmit({ prompt: 'what does Alice prefer?' })

    const band = await $.ui.mount({ plugin: 'cortexdb-live', surface, component: 'AbovePrompt', props })
    expect((await band.findAll({ text: /Recalled 2/ })).length).toBeGreaterThan(0)
    expect(await band.find({ text: /pref-tabs/ })).toBeUndefined()

    await band.press({ key: 'toggle' })
    expect(await band.find({ text: /pref-tabs/ }), 'expanded hit').toBeDefined()
    expect(await band.find({ text: /Query \(haiku\): Alice · indentation · 缩进 · tabs \| Entities: Alice/ }), 'plan').toBeDefined()

    await band.press({ key: 'hide' })
    expect(await band.drawn()).toEqual({ type: 'Text', children: ['engine'] })

    const shown = await $.command.run({ command: 'cortexdb-show', args: '' } as never)
    expect(shown).toMatchObject({ text: 'The recall is shown above the prompt again.' })
    expect(await band.find({ text: /pref-tabs/ }), 'shown again, expanded').toBeDefined()
  })
}

test('language zh draws the band in Chinese', { options: { language: 'zh' } }, async ($, on) => {
  world(on, { autorecall: 'on' })
  await $.session.start({ cwd: '/w', surface: 'terminal' } as never)
  await $.classic.UserPromptSubmit({ prompt: 'what does Alice prefer?' })

  const band = await $.ui.mount({ plugin: 'cortexdb-live', surface: 'terminal', component: 'AbovePrompt', props })
  expect((await band.findAll({ text: /已召回 2 条/ })).length).toBeGreaterThan(0)
  expect((await band.findAll({ text: /（记忆 1 · 知识 1 · \d+\.\ds）/ })).length).toBeGreaterThan(0)
  expect(await band.find({ text: '展开' })).toBeDefined()
})

const turnDone = { answer: 'done', durationMs: 1000, isAborted: false, turnId: 't1', reason: 'answer' as const }

const conversation = [
  { role: 'user' as const, text: 'Deploy v2.120.2 to the cluster', toolUses: [] },
  { role: 'assistant' as const, text: 'Deployed to node-a..e.', toolUses: [] },
  { role: 'user' as const, text: '<system-reminder>noise</system-reminder>Also bump the docs', toolUses: [] },
  { role: 'assistant' as const, text: 'Docs bumped.', toolUses: [] },
]

test('an idle session is captured once, with stable ids, and not again', async ($, on) => {
  const seen = world(on, {
    messages: conversation,
    captured: {
      memories: [
        { slug: 'cortexdb-2120-2-deployed', content: 'CortexDB v2.120.2 deployed to node-a..e on 2026-10-06.', importance: 0.6, type: 'fact', entities: [{ name: 'CortexDB', type: 'project' }] },
        { slug: '', content: '' },
      ],
    },
  })
  const clock = seen.clock

  await $.turn.complete(turnDone)
  await clock.advance(89 * 1000)
  expect(seen.calls.some(c => c.tool === 'memory_save')).toBe(false)
  await clock.advance(2 * 1000)

  const saves = seen.calls.filter(c => c.tool === 'memory_save')
  expect(saves.map(s => s.args.memory_id)).toEqual(['auto:abcdef12:cortexdb-2120-2-deployed'])
  expect(saves[0]?.args.metadata).toMatchObject({ source: 'auto-capture', session: 'abcdef1234567890', model: 'haiku' })
  const digest = seen.prompts.at(-1) ?? ''
  expect(digest).toContain('USER: Also bump the docs')
  expect(digest).not.toContain('noise')
  expect(seen.toasts).toEqual(['CortexDB saved 1 new memories'])

  // Nothing new since: the next idle stretch writes nothing.
  await $.turn.complete(turnDone)
  await clock.advance(91 * 1000)
  expect(seen.calls.filter(c => c.tool === 'memory_save').length).toBe(1)
})

test('a capture retires the older memory it made untrue, never a later one', async ($, on) => {
  const seen = world(on, {
    messages: conversation,
    captured: {
      memories: [
        { slug: 'deploy-node-b', content: 'CortexDB deploys go to node-b since 2026-10-06.', entities: [{ name: 'CortexDB', type: 'project' }] },
      ],
    },
    existing: {
      memories: [
        { memory: { id: 'auto:11111111:deploy-node-a', content: 'CortexDB deploys go to node-a.', metadata: { date: '2026-09-01' } } },
        { memory: { id: 'later', content: 'CortexDB deploys go to node-c.', metadata: { date: '2099-01-01' } } },
        { memory: { id: 'auto:abcdef12:own', content: 'from this session', metadata: { date: '2026-10-06' } } },
        { memory: { id: 'undated', content: 'no date' } },
      ],
    },
    retire: { retire: [{ new: 'deploy-node-b', old: ['auto:11111111:deploy-node-a (2026-09-01)', 'later', 'made-up'], reason: 'moved to node-b' }] },
  })
  const clock = seen.clock

  await $.turn.complete(turnDone)
  await clock.advance(91 * 1000)

  const asked = seen.prompts.find(p => p.includes('EXISTING memories')) ?? ''
  expect(asked).toContain('old "auto:11111111:deploy-node-a", recorded 2026-09-01')
  expect(asked).not.toContain('auto:abcdef12:own')
  expect(asked).not.toContain('undated')
  const save = seen.calls.find(c => c.tool === 'memory_save')
  expect(save?.args.supersedes).toEqual(['auto:11111111:deploy-node-a'])
  expect(save?.args.metadata).toMatchObject({ supersede_reason: 'moved to node-b' })
  expect(seen.toasts).toEqual(['CortexDB saved 1 new memories, retired 1 outdated'])
})

test('capture switched off writes nothing', async ($, on) => {
  const seen = world(on, { autocapture: 'off', messages: conversation })
  const clock = seen.clock

  await $.turn.complete(turnDone)
  await clock.advance(91 * 1000)

  expect(seen.calls.some(c => c.tool === 'memory_save')).toBe(false)
})
