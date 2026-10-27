import assert from "node:assert/strict";
import test from "node:test";
import { SNIPPET_RUNES, formatRecall, keywordsFromPrompt, recallArgs, recallHits, snippetOf } from "../lib/recall.js";

test("keywords keep a CJK run whole, drop one-letter terms and repeats", () => {
  assert.deepEqual(keywordsFromPrompt("Why uv, not pip? 用 uv 建环境 a uv"), ["why", "uv", "not", "pip", "建环境"]);
});

test("a long memory is cut to a snippet on a character boundary", () => {
  const long = "记".repeat(SNIPPET_RUNES + 10);
  const snippet = snippetOf(long);
  assert.equal([...snippet].length, SNIPPET_RUNES + 1);
  assert.ok(snippet.endsWith("…"));
  assert.equal(snippetOf("  two\n  lines "), "two lines");
});

test("recall reads the whole brain unless the config narrows it", () => {
  const base = { recallTopK: 3, userId: "", scope: "", namespace: "" };
  const args = recallArgs("deploy host", base);
  assert.deepEqual(Object.keys(args).sort(), ["graph_light", "keywords", "query", "top_k_knowledge", "top_k_memories"]);
  const narrowed = recallArgs("deploy host", { ...base, scope: "user", namespace: "dsh", userId: "li" });
  assert.equal(narrowed.scope, "user");
  assert.equal(narrowed.namespace, "dsh");
  assert.equal(narrowed.user_id, "li");
});

test("memories come before knowledge, and an older server's results still count", () => {
  const hits = recallHits({
    knowledge: [{ knowledge_id: "k1", title: "Deploy", snippet: "node-b" }],
    memories: [{ memory: { id: "m1", content: "Primary is node-b" }, score: 0.5 }],
    results: [{ knowledge_id: "k2", snippet: "legacy" }],
  });
  assert.deepEqual(hits.map((h) => h.id), ["m1", "k1", "k2"]);
});

test("the injected block names each hit, and nothing is injected for no hits", () => {
  const block = formatRecall([{ id: "m1", title: "", snippet: "Primary is node-b" }, { id: "k1", title: "Deploy", snippet: "" }]);
  assert.match(block, /^Relevant CortexDB memories/);
  assert.match(block, /- m1: Primary is node-b/);
  assert.doesNotMatch(block, /Deploy/);
  assert.equal(formatRecall([]), "");
  assert.equal(formatRecall([{ id: "x", snippet: "" }]), "");
});

test("a recalled guess is marked unverified and a checked hit names its check", () => {
  const hits = recallHits({
    memories: [
      { memory: { id: "cause", content: "the switch probably dropped quorum", grade: "asserted" } },
      { memory: { id: "fix", content: "the fix holds", grade: "verified", grade_why: "drill passed" } },
      { memory: { id: "plain", content: "lunch at noon" } },
    ],
  });
  const block = formatRecall(hits);
  assert.match(block, /- cause \[UNVERIFIED \(asserted\)\]: the switch/);
  assert.match(block, /- fix \[verified: drill passed\]: the fix holds/);
  assert.match(block, /- plain: lunch at noon/);
});
