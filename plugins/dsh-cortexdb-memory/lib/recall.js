// Recall before a prompt, the way the Claude Code and Codex hooks do it
// (cmd/cortexdb-mcp-stdio/recall_remote.go): the same question to the same
// tool, the same snippets, the same block of text. An agent should not see a
// different brain because it runs in a different harness.

// SNIPPET_RUNES caps how much of one memory is injected. This runs before
// every message: it owes the model a pointer, not the file.
export const SNIPPET_RUNES = 220;

const HEADER = "Relevant CortexDB memories for this prompt (retrieved automatically — verify before relying on them):";
const FOOTER = "(If this exchange states a durable preference, decision, or fact, save it with cortexdb_memory_store.)";

// keywordsFromPrompt splits a prompt into deduplicated lexical terms. A run of
// CJK stays one token, as the FTS5 unicode61 tokenizer keeps it.
export function keywordsFromPrompt(prompt) {
  const seen = new Set();
  const out = [];
  for (const field of prompt.split(/[^\p{L}\p{N}]+/u)) {
    const term = field.toLowerCase();
    if ([...term].length < 2 || seen.has(term)) continue;
    seen.add(term);
    out.push(term);
  }
  return out;
}

export function snippetOf(content) {
  const flat = String(content ?? "").split(/\s+/).filter(Boolean).join(" ");
  const runes = [...flat];
  if (runes.length <= SNIPPET_RUNES) return flat;
  return `${runes.slice(0, SNIPPET_RUNES).join("").trim()}…`;
}

// recallArgs is the knowledge_memory_recall request for one prompt. Scope,
// namespace and user are only sent when configured: left out, recall reads
// the whole brain, which is where every other agent's captures live.
export function recallArgs(prompt, config) {
  const args = {
    query: prompt,
    keywords: keywordsFromPrompt(prompt),
    top_k_memories: config.recallTopK,
    top_k_knowledge: config.recallTopK,
    graph_light: true,
  };
  if (config.userId) args.user_id = config.userId;
  if (config.scope) args.scope = config.scope;
  if (config.namespace) args.namespace = config.namespace;
  return args;
}

// recallHits reads hits out of the tool's answer: memories first, they are the
// layer that changes. `results` is what an older, knowledge-only server sends.
export function recallHits(payload) {
  const hits = [];
  for (const hit of payload?.memories ?? []) {
    const memory = hit?.memory ?? {};
    hits.push({ id: memory.id ?? "", title: "", snippet: snippetOf(memory.content), score: Number(hit?.score ?? 0), source: "memory", grade: memory.grade ?? "", gradeWhy: memory.grade_why ?? "" });
  }
  for (const hit of [...(payload?.knowledge ?? []), ...(payload?.results ?? [])]) {
    hits.push({ id: hit?.knowledge_id ?? "", title: hit?.title ?? "", snippet: String(hit?.snippet ?? "").trim(), score: Number(hit?.score ?? 0), source: "knowledge", grade: String(hit?.metadata?._grade ?? "").toLowerCase(), gradeWhy: hit?.metadata?._why ?? "" });
  }
  return hits;
}

// gradeMark is how a hit says it is known, in the words the Claude Code hooks
// use: a guess is marked before its text, a verified hit names its check, an
// untagged one says nothing.
export function gradeMark(grade, why) {
  if (grade === "asserted" || grade === "held") return `UNVERIFIED (${grade})`;
  if (grade === "verified") return why ? `verified: ${[...String(why).trim()].slice(0, 80).join("")}` : "verified";
  return grade || "";
}

// formatRecall is the block injected before the prompt, or "" when nothing is
// worth injecting.
export function formatRecall(hits) {
  const lines = [];
  for (const hit of hits) {
    if (!hit.snippet) continue;
    const mark = gradeMark(hit.grade, hit.gradeWhy);
    lines.push(`- ${hit.title || hit.id}${mark ? ` [${mark}]` : ""}: ${hit.snippet}`);
  }
  if (lines.length === 0) return "";
  return [HEADER, ...lines, FOOTER].join("\n");
}
