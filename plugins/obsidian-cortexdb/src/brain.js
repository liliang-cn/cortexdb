// The connection to a CortexDB brain, and the bulk reads an export needs.
//
// The client is the DeepSeek Harness plugin's: unary gRPC on node:http2 and
// nothing else, which is also what an Obsidian plugin wants — one bundled
// file, no protobuf runtime.

import { createClient, parseToolResult } from "../../dsh-cortexdb-memory/lib/grpc.js";

export const DEFAULT_ENDPOINT = "127.0.0.1:47821";

export class Brain {
  constructor(endpoint, token = "") {
    this.endpoint = String(endpoint || DEFAULT_ENDPOINT).trim();
    this.client = createClient(this.endpoint, { token });
  }

  // call runs one CortexDB tool and returns its parsed answer.
  async call(name, args = {}, timeoutMs = 30000) {
    const { resultJson, requestId } = await this.client.tools.CallTool({ name, argsJson: JSON.stringify(args) }, timeoutMs);
    return parseToolResult(name, resultJson, requestId);
  }

  info(timeoutMs = 5000) {
    return this.client.admin.Info({}, timeoutMs);
  }

  close() {
    this.client.close();
  }
}

// readBrain reads every memory, every graph entity and relation, and which
// entities each memory mentions. `call` is Brain.call; `progress` is told
// what is being read.
export async function readBrain(call, progress = () => {}) {
  const memories = [];
  for (let cursor = ""; ; ) {
    const page = await call("memory_list_all", { limit: 1000, ...(cursor ? { cursor } : {}) }, 120000);
    memories.push(...(page.memories ?? []));
    progress(`Read ${memories.length} memories…`);
    cursor = page.next_cursor ?? "";
    if (!cursor) break;
  }

  const nodes = [];
  const edges = [];
  for (let cursor = ""; ; ) {
    const page = await call("graph_list_all", { order: "id", limit: 2000, ...(cursor ? { cursor } : {}) }, 120000);
    nodes.push(...(page.nodes ?? []));
    edges.push(...(page.edges ?? []));
    progress(`Read ${nodes.length} graph nodes…`);
    cursor = page.next_cursor ?? "";
    if (!cursor) break;
  }

  // The walk returns memory and document nodes beside the entities; a
  // memory's links to the entities it mentions are among its edges.
  const mentions = edges.filter((e) => e.type === "mentions" && String(e.from).startsWith("memory:")).map((e) => [e.from, e.to]);
  return { memories, nodes: nodes.filter((n) => String(n.id).startsWith("entity:")), edges, mentions };
}
