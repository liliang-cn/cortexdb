# Real usage record — OpenClaw agent using CortexDB as its memory

Captured 2026-06-12 on a local Docker setup (Apple-silicon, linux/arm64):

- **cortexdb** — the `cortexdb-grpc` sidecar (this repo), vectors via host Ollama
  `embeddinggemma` (768-dim).
- **openclaw** — `ghcr.io/openclaw/openclaw:latest`, model = host Ollama
  `qwen3.5`, with the `cortexdb-memory-openclaw` skill installed.

Everything is local: one SQLite file, a local LLM, zero external API keys.

## 0. The skill is recognized by OpenClaw

```
$ openclaw skills list | grep cortexdb
│ ✓ ready │ cortexdb-memory-openclaw │ Give a Node.js agent … CortexDB … │ openclaw-managed │
```

## 1. Default behavior — the small model picks its built-in memory (honest finding)

> **User:** Please remember two facts about me … (1) I prefer dark roast coffee,
> (2) I am allergic to peanuts.

> **Agent (qwen3.5):** Done! I've stored your two facts … in `memory/0323.md`
> and curated in `MEMORY.md`.

Independent check of CortexDB afterwards: **empty** — the agent used OpenClaw's
bundled `memory-core` plugin, not CortexDB. Takeaway: to make CortexDB the memory
layer you either instruct the agent to use the skill, or wire it as the backend.

## 2. Explicit CortexDB use — agent stores via the skill helper

> **User:** Use the cortexdb-memory-openclaw skill (NOT your built-in memory) …
> call remember() twice for userId 'alice' …

> **Agent:** The memories have been successfully stored in cortexdb. Here are the
> memory IDs:
> - **mem-fa21b5653b68** — "Alice prefers dark roast coffee"
> - **mem-87af7d69bf43** — "Alice is allergic to peanuts"

Independent verification (queried CortexDB directly from the host, not trusting
the agent):

```
=== CortexDB memories for user=alice ===
  • Alice is allergic to peanuts        (score 1.000)
  • Alice prefers dark roast coffee     (score 0.600)
```

The agent's reported IDs were real; the facts are in CortexDB, retrievable by
semantic search (Ollama embeddings).

## 3. Fresh session — agent recalls from CortexDB and reasons (the actual value)

A brand-new session (no shared context with turns 1–2):

> **User:** I'm thinking about what to order for Alice at a cafe. Use the
> cortexdb recall() helper to look up what you know about Alice, then recommend a
> coffee and warn me about any allergy.

> **Agent:** What I know about Alice: **Coffee preference:** Dark roast ☕ ·
> **Allergy:** Peanuts. … A dark roast coffee would be perfect … ⚠️ make sure the
> coffee is **peanut-free** … double-check with the barista …

Cross-session memory works: the agent retrieved Alice's stored preferences from
CortexDB and reasoned over them correctly.

## How to reproduce

```bash
ollama pull qwen3.5 embeddinggemma
docker compose -f deploy/agents-demo/docker-compose.yml up -d --build cortexdb openclaw
# configure Ollama provider + install skill (see compose + this dir's notes), then:
docker compose -f deploy/agents-demo/docker-compose.yml exec openclaw \
  node openclaw.mjs agent --session-key agent:default:demo --thinking off \
  --message "<your message>"
```
