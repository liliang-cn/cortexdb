export type RecallHit = { kind: 'memory' | 'knowledge'; title: string; snippet: string }

export type RecallPlan = { by: 'haiku' | 'lexical'; keywords: string[]; entities: string[]; mode: string; ms: number }

export type Recall = { hits: RecallHit[]; ms: number; plan: RecallPlan }

export type CapturedMemory = { slug: string; content: string }

/** How far this session has been captured: the last message read, and what it wrote. */
export type CaptureMark = { sessionId: string; lastKey: string; memories: CapturedMemory[] }

declare module 'claude-code' {
  interface PluginState {
    'cortexdb-live': {
      last: Recall | null
      isOpen: boolean
      isHidden: boolean
      capture: CaptureMark | null
    }
  }
}
