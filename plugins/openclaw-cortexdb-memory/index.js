import { Type } from "typebox";
import { definePluginEntry } from "openclaw/plugin-sdk/plugin-entry";
import { createCortexDB, recallHits } from "./lib/cortexdb.js";

function textResult(value) {
  return {
    content: [{ type: "text", text: typeof value === "string" ? value : JSON.stringify(value) }],
    details: value,
  };
}

export default definePluginEntry({
  id: "cortexdb-memory",
  name: "CortexDB Memory",
  description: "Local-first scoped memory, RAG, and graph-aware recall for OpenClaw.",
  register(api) {
    const db = createCortexDB(api.pluginConfig);
    const resultCache = new Map();

    api.registerService({
      id: "cortexdb-grpc-sidecar",
      async start() {
        try {
          await db.ready();
          api.logger.info(`CortexDB memory connected at ${db.config.endpoint}`);
        } catch (error) {
          api.logger.error(String(error));
        }
      },
      async stop() { await db.close(); },
    });

    api.registerMemoryCapability({
      promptBuilder({ availableTools }) {
        if (!availableTools.has("cortexdb_memory_search")) return [];
        return [
          "Use cortexdb_memory_search before answering questions about prior decisions, people, preferences, project history, or remembered facts.",
          "Use cortexdb_memory_store when the user explicitly asks to remember durable information or when a stable preference or decision should survive future sessions.",
        ];
      },
      runtime: {
        async getMemorySearchManager() {
          return {
            manager: {
              async search(query, options = {}) {
                const response = await db.recall(query, {
                  topKMemories: options.maxResults,
                  topKKnowledge: options.maxResults,
                });
                const hits = recallHits(response)
                  .filter((hit) => options.minScore === undefined || hit.score >= options.minScore)
                  .slice(0, options.maxResults || db.config.topKMemories)
                  .map((hit) => ({ ...hit, startLine: 1, endLine: 1 }));
                for (const hit of hits) resultCache.set(hit.path, hit.snippet);
                return hits;
              },
              async readFile({ relPath }) {
                const text = resultCache.get(relPath) || "";
                return { path: relPath, text, from: 1, lines: text ? 1 : 0 };
              },
              status() {
                return {
                  backend: "builtin",
                  provider: "cortexdb",
                  dirty: false,
                  sources: ["memory"],
                  custom: {
                    endpoint: db.config.endpoint,
                    namespace: db.config.namespace,
                    connected: db.sidecar.state.connected,
                    managedSidecar: db.sidecar.state.managed,
                    error: db.sidecar.state.error || undefined,
                    dbPath: db.sidecar.state.info?.dbPath,
                    version: db.sidecar.state.info?.version,
                  },
                };
              },
              async probeEmbeddingAvailability() {
                return { ok: false, checked: true, error: "CortexDB may be running in lexical mode" };
              },
              async probeVectorAvailability() { return false; },
              async close() {},
            },
          };
        },
        resolveMemoryBackendConfig() { return { backend: "builtin" }; },
        async closeMemorySearchManager() {},
        async closeAllMemorySearchManagers() { db.close(); },
      },
    });

    api.registerTool({
      name: "cortexdb_memory_search",
      label: "CortexDB Memory Search",
      description: "Recall fused CortexDB memory, durable knowledge, and graph facts.",
      parameters: Type.Object({
        query: Type.String(),
        maxResults: Type.Optional(Type.Integer({ minimum: 1, maximum: 50 })),
      }, { additionalProperties: false }),
      async execute(_id, params) {
        const response = await db.recall(params.query, {
          topKMemories: params.maxResults,
          topKKnowledge: params.maxResults,
        });
        return textResult({
          context: response.context_pack?.text || "",
          memories: response.memories || [],
          knowledge: response.knowledge || [],
          graphFacts: response.graph_facts || [],
        });
      },
    });

    api.registerTool({
      name: "cortexdb_memory_store",
      label: "CortexDB Memory Store",
      description: "Store one durable memory in CortexDB.",
      parameters: Type.Object({
        content: Type.String(),
        importance: Type.Optional(Type.Number()),
      }, { additionalProperties: false }),
      async execute(_id, params) {
        return textResult(await db.remember(params.content, { importance: params.importance }));
      },
    });

    api.registerTool({
      name: "cortexdb_memory_forget",
      label: "CortexDB Memory Forget",
      description: "Delete one CortexDB memory by exact memory ID.",
      parameters: Type.Object({ memoryId: Type.String() }, { additionalProperties: false }),
      async execute(_id, params) { return textResult(await db.forget(params.memoryId)); },
    }, { optional: true });

  },
});
