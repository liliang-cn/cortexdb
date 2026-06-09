'use strict';

// Minimal 'memory layer for a Node agent' example (OpenClaw-style).
//
// Start a sidecar first:
//   go run ./cmd/cortexdb-grpc -db agent.db
// Then:
//   node examples/agent_memory.js
// Env: CORTEXDB_GRPC_ENDPOINT (default 127.0.0.1:47821), CORTEXDB_GRPC_TOKEN

const { CortexClient } = require('../src/index.js');

async function main() {
  const endpoint = process.env.CORTEXDB_GRPC_ENDPOINT || '127.0.0.1:47821';
  const token = process.env.CORTEXDB_GRPC_TOKEN;
  const client = CortexClient.connect(endpoint, token ? { token } : {});

  const info = await client.admin.Info({});
  console.log(`connected: cortexdb ${info.version} (embedder=${info.hasEmbedder})`);

  // The agent remembers something about the user.
  await client.memory.SaveMemory({
    memoryId: 'pref-stack',
    userId: 'alice',
    scope: 'user',
    content: 'Alice runs OpenClaw locally and prefers TypeScript over Python.',
  });

  // Later turn: recall it.
  const recall = await client.memory.SearchMemory({
    query: 'what stack does the user prefer?',
    userId: 'alice', scope: 'user', topK: 3,
  });
  console.log('recalled:');
  for (const hit of recall.results) {
    console.log(`  - ${hit.memory.content}  (score ${hit.score.toFixed(3)})`);
  }

  client.close();
}

main().catch((e) => { console.error(e); process.exit(1); });
