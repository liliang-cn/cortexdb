'use strict';

// Integration test against a real cortexdb-grpc sidecar.
// Set CORTEXDB_GRPC_BIN or place the binary at clients/target/cortexdb-grpc.
// Skips (exit 0) when not found.

const path = require('path');
const fs = require('fs');
const net = require('net');
const { spawn } = require('child_process');
const assert = require('assert');
const { CortexClient, grpc } = require('../src/index.js');

function binary() {
  const env = process.env.CORTEXDB_GRPC_BIN;
  if (env && fs.existsSync(env)) return env;
  const fallback = path.join(__dirname, '..', '..', 'target', 'cortexdb-grpc');
  return fs.existsSync(fallback) ? fallback : null;
}

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
    srv.on('error', reject);
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  const bin = binary();
  if (!bin) {
    console.log('SKIP: cortexdb-grpc binary not found (set CORTEXDB_GRPC_BIN)');
    return;
  }
  const port = await freePort();
  const db = path.join('/tmp', `cortexdb-node-it-${process.pid}.db`);
  if (fs.existsSync(db)) fs.unlinkSync(db);

  const env = { ...process.env };
  delete env.OPENAI_BASE_URL; // force lexical mode
  const proc = spawn(bin, ['-db', db, '-addr', `127.0.0.1:${port}`, '-token', 'node-it'], { env });

  try {
    let client = null;
    for (let i = 0; i < 50; i++) {
      try {
        const c = CortexClient.connect(`127.0.0.1:${port}`, { token: 'node-it' });
        await c.admin.Health({});
        client = c;
        break;
      } catch (_) { await sleep(100); }
    }
    assert(client, 'sidecar did not become healthy');

    // wrong token rejected
    const bad = CortexClient.connect(`127.0.0.1:${port}`, { token: 'nope' });
    let rejected = false;
    try { await bad.admin.Health({}); }
    catch (e) { rejected = e.code === grpc.status.UNAUTHENTICATED; }
    assert(rejected, 'expected UNAUTHENTICATED for wrong token');

    const info = await client.admin.Info({});
    assert(info.version, 'empty version');
    assert(!info.hasEmbedder, 'expected lexical mode');

    await client.knowledge.SaveKnowledge({
      knowledgeId: 'k1',
      title: 'Node event loop',
      content: 'Node.js runs JavaScript on a single-threaded event loop with libuv for async I/O.',
    });
    const res = await client.knowledge.SearchKnowledge({ query: 'single threaded event loop', topK: 3 });
    assert(res.results.length > 0, 'no search results');
    assert.strictEqual(res.results[0].knowledgeId, 'k1');

    await client.memory.SaveMemory({
      memoryId: 'm1', userId: 'u1', scope: 'user',
      content: 'User runs OpenClaw locally and prefers TypeScript.',
    });

    await client.graph.UpsertNamespace({ prefix: 'ex', uri: 'https://example.com/' });
    const iri = (v) => ({ kind: 'iri', value: v });
    await client.graph.UpsertKnowledgeGraph({
      triples: [{ subject: iri('ex:alice'), predicate: iri('ex:knows'), object: iri('ex:bob') }],
    });
    const q = await client.graph.QuerySparql({
      query: 'SELECT ?o WHERE { <https://example.com/alice> <https://example.com/knows> ?o . }',
    });
    assert.strictEqual(q.result.count, 1);

    const tools = await client.tools.ListTools({});
    assert(tools.tools.some((t) => t.name === 'knowledge_search'), 'knowledge_search tool missing');

    client.close();
    console.log('NODE E2E OK: admin/auth + knowledge + memory + sparql + tools');
  } finally {
    proc.kill();
    if (fs.existsSync(db)) fs.unlinkSync(db);
  }
}

main().catch((e) => { console.error(e); process.exit(1); });
