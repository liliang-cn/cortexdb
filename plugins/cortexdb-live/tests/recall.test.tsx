import type { On } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const SHELL = 'Relevant CortexDB memories for this prompt (retrieved automatically — verify before relying on them):\n- old: from the shell hook'

const recallPayload = {
  memories: [{ memory: { id: 'pref-tabs', content: 'Alice   prefers\n tabs.' } }],
  knowledge: [{ knowledge_id: 'k1', title: 'Indent policy', snippet: 'Tabs in Go.' }],
}

// A brain's answer as v2.134 gives it: each hit with the scores that found it
// and the metadata its writer left.
const explainedPayload = {
  memory_decision: { effective_mode: 'auto' },
  knowledge_decision: { effective_mode: 'hybrid' },
  memories: [
    {
      score: 0.9312,
      similarity: 0.8037,
      via: ['semantic', 'keyword'],
      memory: {
        id: 'auto:d0b123ed:verify-fix',
        content: 'linstor-server: the resize fix is verified by reverting it and watching the test go red.',
        created_at: '2026-10-09T02:25:42Z',
        metadata: {
          date: '2026-10-09',
          source: 'auto-capture',
          model: 'haiku',
          agent: 'claude-code',
          host: 'mac',
          project: 'linstor-server',
          session: 'd0b123ed-e6d6-4b5e-82ad-570cd2238f5b',
          written_by: 'legacy-token',
        },
      },
    },
  ],
  knowledge: [
    {
      knowledge_id: 'noted:note:1',
      title: 'Resize runbook',
      snippet: 'Resize a volume, then verify.',
      author: 'noted',
      source_url: 'noted://note/1',
      vector_score: 0.3026,
      lexical_score: 0.7809,
      metadata: { updated: '2026-10-18T09:00:28Z', written_by: 'hermes' },
    },
  ],
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
  root?: string
  host?: string
  payload?: object
  isBrainDown?: boolean
  hostReply?: string
  indexFits?: boolean
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
    if (w.hostReply && !(w.indexFits && e.args.detail === 'index')) {
      return { value: { content: [{ type: 'text', text: w.hostReply }], isError: false } }
    }
    const isCaptureLookup = e.tool === 'knowledge_memory_recall' && e.args.disable_knowledge === true
    const body =
      e.tool === 'graph_statistics'
        ? { node_count: 4493 }
        : e.tool === 'memory_save'
          ? { ok: true }
          : isCaptureLookup && w.existing
            ? w.existing
            : (w.payload ?? recallPayload)
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
  on('session.root', () => ({ value: w.root ?? '/home/a' }))
  on('config.list', () => ({ value: [] }))
  on('process.run', ($, e) =>
    e.argv[0] === 'hostname' && w.host
      ? { value: { exitCode: 0, stdout: `${w.host}\n`, stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
      : { value: { exitCode: 1, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } },
  )
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
  expect(mine).toContain('- pref-tabs [semantic]: Alice prefers tabs.')
  expect(mine).toContain('- Indent policy [semantic]: Tabs in Go.')
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

const TOO_LARGE = 'Error: result (31,204 tokens) exceeds maximum allowed tokens. Output has been saved to /tmp/r.txt'

test('a recall past the host limit is asked again as an index', async ($, on) => {
  const seen = world(on, { autorecall: 'on', hostReply: TOO_LARGE, indexFits: true })

  const out = await $.classic.UserPromptSubmit({ prompt: 'how do we indent Go?' })

  const recalls = seen.calls.filter(c => c.tool === 'knowledge_memory_recall')
  expect(recalls.map(c => c.args.detail)).toEqual([undefined, 'index'])
  expect((out.additionalContext ?? []).join('\n')).toContain('pref-tabs')
})

test('a reply that is not JSON is reported by what it says', async ($, on) => {
  const seen = world(on, { autorecall: 'on', hostReply: 'Error: the MCP server went away' })
  const logged: { text: string; to?: string }[] = []
  on('ui.log', async (_$, e) => {
    logged.push({ text: e.text, to: e.to })
    return { value: undefined }
  })

  const out = await $.classic.UserPromptSubmit({ prompt: 'how do we indent Go?' })

  expect(out.additionalContext).toEqual([SHELL, 'another plugin'])
  // What arrived and how big it was, not the parser's complaint about it.
  expect(seen.status.at(-1)).toContain('knowledge_memory_recall: not JSON (31 bytes): Error: the MCP server went away')
  // The whole reply goes to the debug log, with the time to find the
  // brain's record of the call by.
  const line = logged.find(l => l.to === 'debug')?.text ?? ''
  expect(line).toContain('received: Error: the MCP server went away')
  expect(line).toMatch(/at \d{4}-\d{2}-\d{2}T/)
})

test('a recall the embedder could not serve says so in the status', async ($, on) => {
  const seen = world(on, { autorecall: 'on', payload: { ...recallPayload, degraded: ['embedder_unreachable'] } })

  await $.classic.UserPromptSubmit({ prompt: 'how do we indent Go?' })

  expect(seen.status.at(-1)).toContain('embedder unreachable, keyword only')
  const recall = seen.calls.find(c => c.tool === 'knowledge_memory_recall')
  expect(String(recall?.args.planner ?? '')).not.toBe('')
})

const linstor = [
  { role: 'user' as const, text: 'linstor-server: resize leaves the volume definition at the old size', toolUses: [] as never[] },
  { role: 'assistant' as const, text: 'Root cause: VolumeDefinition resize skips the satellite update. I suggest a fix in CtrlVlmDfnModifyApiCallHandler plus a regression test.', toolUses: [] as never[] },
]

test('a message that points into the conversation is planned with the conversation and the project', async ($, on) => {
  const seen = world(on, { autorecall: 'on', messages: linstor, root: '/Users/a/Works/linstor-server' })

  await $.classic.UserPromptSubmit({ prompt: '按建议 · 这个修复前后可以验证吗' })

  const asked = seen.prompts.find(p => p.includes('New user message')) ?? ''
  expect(asked).toContain('Project: linstor-server')
  expect(asked).toContain('ASSISTANT: Root cause: VolumeDefinition resize skips the satellite update.')
  expect(asked).toContain('USER: linstor-server: resize leaves')
})

test('without a plan the pointer message still carries the project and the last reply', async ($, on) => {
  const seen = world(on, { autorecall: 'on', plan: 'fail', messages: linstor, root: '/Users/a/Works/linstor-server' })

  await $.classic.UserPromptSubmit({ prompt: '按建议 · 这个修复前后可以验证吗' })

  const recall = seen.calls.find(c => c.tool === 'knowledge_memory_recall')
  const query = String(recall?.args.query)
  expect(query.startsWith('按建议 · 这个修复前后可以验证吗\n\nProject: linstor-server\nContext: Root cause: VolumeDefinition')).toBe(true)
  expect(recall?.args.keywords).toContain('linstor-server')
  expect(recall?.args.entity_names).toEqual(['linstor-server'])
})

test('a fresh session with no project asks the message as written', async ($, on) => {
  const seen = world(on, { autorecall: 'on', plan: 'fail' })

  await $.classic.UserPromptSubmit({ prompt: '按建议 · 这个修复前后可以验证吗' })

  expect(seen.calls.find(c => c.tool === 'knowledge_memory_recall')?.args.query).toBe('按建议 · 这个修复前后可以验证吗')
})

test('each hit says why it came back, who wrote it and where from', async ($, on) => {
  const seen = world(on, { autorecall: 'on', payload: explainedPayload, plan: { ...PLAN, keywords: ['linstor-server', 'verify', 'resize'] } })

  const out = await $.classic.UserPromptSubmit({ prompt: 'how do we verify the resize fix?' })

  const mine = out.additionalContext?.[1] ?? ''
  expect(mine).toContain(
    '- auto:d0b123ed:verify-fix [2026-10-09 · claude-code auto-capture (haiku) · host mac · project linstor-server · session d0b123ed · semantic 0.80 + keyword]: linstor-server: the resize fix',
  )
  expect(mine).toContain('- Resize runbook [2026-10-18 · noted · key hermes · noted://note/1 · semantic 0.30 + keyword 0.78]: Resize a volume')
  // The shared token every unkeyed client speaks with names nobody.
  expect(mine).not.toContain('legacy-token')
  expect(seen.calls.length).toBeGreaterThan(0)
})

test('the band explains each hit in Chinese', { options: { language: 'zh' } }, async ($, on) => {
  world(on, { autorecall: 'on', payload: explainedPayload, plan: { ...PLAN, keywords: ['linstor-server', 'verify', 'resize'] } })
  await $.session.start({ cwd: '/w', surface: 'terminal' } as never)
  await $.classic.UserPromptSubmit({ prompt: 'how do we verify the resize fix?' })

  const band = await $.ui.mount({ plugin: 'cortexdb-live', surface: 'terminal', component: 'AbovePrompt', props })
  await band.press({ key: 'toggle' })
  expect(await band.find({ text: /What indentation does Alice prefer\?/ }), 'resolved query').toBeDefined()
  expect(
    await band.find({ text: /依据 语义 0\.80 \+ 关键词 · 命中 linstor-server \/ resize  来源 claude-code auto-capture \(haiku\) · mac · 项目 linstor-server · 会话 d0b123ed · 2026-10-09/ }),
    'memory explained',
  ).toBeDefined()
  expect(await band.find({ text: /依据 语义 0\.30 \+ 关键词 0\.78 · 命中 verify \/ resize  来源 noted · key hermes · noted:\/\/note\/1 · 2026-10-18/ }), 'knowledge explained').toBeDefined()
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
    expect(await band.drawn()).toMatchObject({ type: 'Text', children: ['engine'] })

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
  expect(saves[0]?.args.metadata).toMatchObject({ source: 'auto-capture', agent: 'claude-code', session: 'abcdef1234567890', model: 'haiku' })
  const digest = seen.prompts.at(-1) ?? ''
  expect(digest).toContain('USER: Also bump the docs')
  expect(digest).not.toContain('noise')
  expect(seen.toasts).toEqual(['CortexDB saved 1 new memories'])

  // Nothing new since: the next idle stretch writes nothing.
  await $.turn.complete(turnDone)
  await clock.advance(91 * 1000)
  expect(seen.calls.filter(c => c.tool === 'memory_save').length).toBe(1)
})

test('a captured memory names the project and the machine it came from', async ($, on) => {
  const seen = world(on, {
    messages: conversation,
    root: '/Users/a/Works/linstor-server',
    host: 'mac',
    captured: { memories: [{ slug: 'resize-fix', content: 'linstor-server resize fix verified on 2026-10-19.' }] },
  })
  await $.session.start({ cwd: '/Users/a/Works/linstor-server', surface: 'terminal' } as never)

  await $.turn.complete(turnDone)
  await seen.clock.advance(91 * 1000)

  const save = seen.calls.find(c => c.tool === 'memory_save')
  expect(save?.args.metadata).toMatchObject({ project: 'linstor-server', host: 'mac', agent: 'claude-code' })
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
