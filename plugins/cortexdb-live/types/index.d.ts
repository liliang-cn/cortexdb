/** Which retriever found a hit, and how strongly. score is that retriever's own, not the fused rank value. */
export type HitWhy = { via: 'semantic' | 'keyword' | 'hybrid' | 'graph'; vector?: number; lexical?: number; matched: string[] }

/** Who wrote a hit and where it came from, as far as the store recorded it. */
export type HitOrigin = {
  date?: string
  /** The key the brain authenticated the save with. */
  writer?: string
  source?: string
  model?: string
  agent?: string
  host?: string
  project?: string
  session?: string
  url?: string
}

export type RecallHit = { kind: 'memory' | 'knowledge'; title: string; snippet: string; why?: HitWhy; origin?: HitOrigin }

/** query is what was asked after the conversation resolved the message; project is the session's. */
export type RecallPlan = {
  by: 'haiku' | 'lexical'
  query: string
  keywords: string[]
  entities: string[]
  mode: string
  ms: number
  project?: string
}

export type Recall = { hits: RecallHit[]; ms: number; plan: RecallPlan; degraded?: string[] }

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
