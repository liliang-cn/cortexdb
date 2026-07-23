import assert from "node:assert/strict";
import test from "node:test";
import { createCortexDB, recallHits, resolveConfig } from "../lib/cortexdb.js";

test("resolveConfig applies safe defaults", () => {
  const config = resolveConfig({}, {});
  assert.equal(config.endpoint, "127.0.0.1:47821");
  assert.equal(config.namespace, "openclaw");
  assert.equal(config.scope, "user");
  assert.equal(config.autoStart, true);
});

test("recall uses unified KnowledgeMemory tool", async () => {
  const calls = [];
  const fake = {
    tools: {
      async CallTool(request) {
        calls.push(request);
        return { resultJson: JSON.stringify({ context_pack: { text: "remembered" } }) };
      },
    },
    close() {},
  };
  const sidecar = {
    state: { connected: false, managed: false, error: "", info: null },
    async start() { this.state.connected = true; },
    async stop() {},
  };
  const db = createCortexDB({ userId: "alice" }, () => fake, () => sidecar);
  const result = await db.recall("coffee");
  assert.equal(result.context_pack.text, "remembered");
  assert.equal(calls[0].name, "knowledge_memory_recall");
  assert.equal(JSON.parse(calls[0].argsJson).user_id, "alice");
  assert.equal(sidecar.state.connected, true);
});

test("recallHits maps memory and knowledge", () => {
  const hits = recallHits({
    memories: [{ memory: { id: "m1", content: "dark roast" }, score: 0.9 }],
    knowledge: [{ knowledge_id: "k1", snippet: "project brief", score: 0.8 }],
  });
  assert.deepEqual(hits.map((hit) => hit.path), ["cortexdb/memory/m1", "cortexdb/knowledge/k1"]);
});
