import assert from "node:assert/strict";
import test from "node:test";

import { parseToolResult } from "../lib/grpc.js";

test("a reply that is not JSON is reported by its size, first bytes and request id", () => {
  const reply = "Error: result (61,203 tokens) exceeds maximum allowed tokens (25,000).";
  assert.throws(() => parseToolResult("knowledge_memory_recall", reply, "abc123"), (error) => {
    assert.match(error.message, /^knowledge_memory_recall: reply is not JSON \(\d+ bytes, request_id abc123\): Error: result/);
    assert.equal(error.requestId, "abc123");
    assert.equal(error.received, reply);
    return true;
  });
});

test("a JSON reply parses, and an empty one is an empty object", () => {
  assert.deepEqual(parseToolResult("t", '{"a":1}'), { a: 1 });
  assert.deepEqual(parseToolResult("t", ""), {});
});
