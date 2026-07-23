'use strict';

// Drop-in CortexDB memory tools for a Node agent (OpenClaw-style).
// Each function wraps the cortexdb-client gRPC client and returns plain values
// so they slot straight into a tool-calling loop.
//
// Prereqs:
//   npm install cortexdb-client
//   # and a running sidecar, e.g.:
//   #   CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc
//
// Config via env: CORTEXDB_GRPC_ENDPOINT (default 127.0.0.1:47821),
// CORTEXDB_GRPC_TOKEN.

const crypto = require('crypto');
const { CortexClient } = require('cortexdb-client');

let _client = null;
function client() {
  if (!_client) {
    const endpoint = process.env.CORTEXDB_GRPC_ENDPOINT || '127.0.0.1:47821';
    const token = process.env.CORTEXDB_GRPC_TOKEN;
    _client = CortexClient.connect(endpoint, token ? { token } : {});
  }
  return _client;
}

const rid = (p) => `${p}-${crypto.randomBytes(6).toString('hex')}`;

async function callTool(name, args) {
  const res = await client().tools.CallTool({ name, argsJson: JSON.stringify(args) });
  return JSON.parse(res.resultJson || '{}');
}

/** Store a memory about the user. Returns the memory id. */
async function remember(content, { userId = 'default', scope = 'user', importance = 0 } = {}) {
  const memoryId = rid('mem');
  await client().memory.SaveMemory({ memoryId, userId, scope, content, importance });
  return memoryId;
}

/** Recall memories by meaning. Returns [{ content, score }]. */
async function recall(query, { userId = 'default', scope = 'user', topK = 5 } = {}) {
  const res = await recallContext(query, { userId, scope, topKMemories: topK });
  return (res.memories || []).map((h) => ({
    id: h.memory && (h.memory.id || h.memory.memory_id),
    content: h.memory && h.memory.content,
    score: h.score,
  }));
}

/** Fused recall across memory, knowledge, and graph facts. */
async function recallContext(query, {
  userId = 'default', sessionId = '', scope = 'user', namespace = '',
  topKMemories = 5, topKKnowledge = 5, maxContextChars = 8000,
} = {}) {
  return callTool('knowledge_memory_recall', {
    query, user_id: userId, session_id: sessionId, scope, namespace,
    top_k_memories: topKMemories, top_k_knowledge: topKKnowledge,
    max_context_chars: maxContextChars,
  });
}

/** Store through the unified KnowledgeMemory facade. */
async function rememberContext(content, {
  memoryId = '', userId = 'default', sessionId = '', scope = 'user',
  namespace = '', importance = 0, metadata = {},
} = {}) {
  const id = memoryId || rid('mem');
  const res = await callTool('knowledge_memory_remember', {
    memory_id: id, user_id: userId, session_id: sessionId, scope,
    namespace, content, importance, metadata,
  });
  return (res.memory && (res.memory.id || res.memory.memory_id)) || id;
}

/** Store a durable knowledge document (chunked + indexed). Returns its id. */
async function saveKnowledge(content, { title = '', knowledgeId = '' } = {}) {
  const id = knowledgeId || rid('kn');
  await client().knowledge.SaveKnowledge({ knowledgeId: id, title, content });
  return id;
}

/** GraphRAG search over saved knowledge. Returns [{ id, title, snippet, score }]. */
async function searchKnowledge(query, { topK = 5 } = {}) {
  const res = await client().knowledge.SearchKnowledge({ query, topK });
  return res.results.map((h) => ({
    id: h.knowledgeId, title: h.title, snippet: h.snippet, score: h.score,
  }));
}

/** Add an entity-relation triple. CURIEs ("ex:alice") or IRIs; object may be a literal. */
async function relate(subject, predicate, object, {
  namespace = 'https://example.com/', prefix = 'ex',
} = {}) {
  await client().graph.UpsertNamespace({ prefix, uri: namespace });
  const term = (v, allowLiteral = false) =>
    allowLiteral && !v.includes(':') ? { kind: 'literal', value: v } : { kind: 'iri', value: v };
  await client().graph.UpsertKnowledgeGraph({
    triples: [{ subject: term(subject), predicate: term(predicate), object: term(object, true) }],
  });
}

/** Run a SPARQL SELECT/ASK over the knowledge graph. Returns a summary object. */
async function askGraph(sparql) {
  const { result } = await client().graph.QuerySparql({ query: sparql });
  const bindings = (result.bindings || []).map((b) =>
    Object.fromEntries(Object.entries(b.vars).map(([k, v]) => [k, v.value])));
  return { count: result.count, vars: result.vars, bindings, boolean: result.boolean };
}

module.exports = {
  remember, recall, rememberContext, recallContext,
  saveKnowledge, searchKnowledge, relate, askGraph, callTool, client,
};

// tiny smoke run against a lexical-mode sidecar: node memory-tools.js
if (require.main === module) {
  (async () => {
    console.log('remember:', await remember('User prefers dark roast coffee.', { userId: 'alice' }));
    console.log('recall:', await recall('what does the user drink?', { userId: 'alice' }));
    await relate('ex:alice', 'ex:knows', 'ex:bob');
    console.log('askGraph:', await askGraph(
      'SELECT ?o WHERE { <https://example.com/alice> <https://example.com/knows> ?o . }'));
    client().close();
  })().catch((e) => { console.error(e); process.exit(1); });
}
