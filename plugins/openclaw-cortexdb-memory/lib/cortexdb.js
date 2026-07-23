import clientPackage from "cortexdb-client";
import { randomUUID } from "node:crypto";

const { CortexClient } = clientPackage;

function integer(value, fallback, min = 0) {
  const parsed = Number.parseInt(String(value ?? ""), 10);
  return Number.isFinite(parsed) ? Math.max(min, parsed) : fallback;
}

export function resolveConfig(config = {}, env = process.env) {
  return {
    endpoint: config.endpoint || env.CORTEXDB_GRPC_ENDPOINT || "127.0.0.1:47821",
    token: config.token || env.CORTEXDB_GRPC_TOKEN || "",
    userId: config.userId || env.CORTEXDB_USER_ID || "default",
    scope: config.scope || env.CORTEXDB_MEMORY_SCOPE || "user",
    namespace: config.namespace || env.CORTEXDB_MEMORY_NAMESPACE || "openclaw",
    topKMemories: integer(config.topKMemories, 8, 1),
    topKKnowledge: integer(config.topKKnowledge, 5, 0),
    maxContextChars: integer(config.maxContextChars, 12000, 256),
  };
}

export function createCortexDB(config = {}, connect = CortexClient.connect) {
  const resolved = resolveConfig(config);
  const client = connect(resolved.endpoint, resolved.token ? { token: resolved.token } : {});

  async function call(name, args) {
    const response = await client.tools.CallTool({ name, argsJson: JSON.stringify(args) });
    return JSON.parse(response.resultJson || "{}");
  }

  async function recall(query, overrides = {}) {
    return call("knowledge_memory_recall", {
      query,
      user_id: overrides.userId || resolved.userId,
      session_id: overrides.sessionId || "",
      scope: overrides.scope || resolved.scope,
      namespace: overrides.namespace || resolved.namespace,
      top_k_memories: overrides.topKMemories ?? resolved.topKMemories,
      top_k_knowledge: overrides.topKKnowledge ?? resolved.topKKnowledge,
      max_context_chars: overrides.maxContextChars ?? resolved.maxContextChars,
    });
  }

  async function remember(content, overrides = {}) {
    const memoryId = overrides.memoryId || `openclaw-${randomUUID()}`;
    const response = await call("knowledge_memory_remember", {
      memory_id: memoryId,
      user_id: overrides.userId || resolved.userId,
      session_id: overrides.sessionId || "",
      scope: overrides.scope || resolved.scope,
      namespace: overrides.namespace || resolved.namespace,
      content,
      importance: overrides.importance || 0,
      metadata: { source: "openclaw", ...(overrides.metadata || {}) },
    });
    return response.memory || { id: memoryId, content };
  }

  async function forget(memoryId) {
    return call("memory_delete", { memory_id: memoryId });
  }

  return { client, config: resolved, call, recall, remember, forget, close: () => client.close() };
}

export function recallHits(response) {
  const hits = [];
  for (const hit of response.memories || []) {
    const memory = hit.memory || {};
    hits.push({
      path: `cortexdb/memory/${memory.id || memory.memory_id || "unknown"}`,
      score: Number(hit.score || 0),
      snippet: memory.content || "",
      source: "memory",
    });
  }
  for (const hit of response.knowledge || []) {
    hits.push({
      path: `cortexdb/knowledge/${hit.knowledge_id || hit.id || "unknown"}`,
      score: Number(hit.score || 0),
      snippet: hit.snippet || hit.content || "",
      source: "memory",
    });
  }
  return hits;
}
