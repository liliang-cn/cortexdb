#!/usr/bin/env bash
set -e

echo "[hermes] locating CLI..."
HERMES="$(command -v hermes || true)"
if [ -z "$HERMES" ]; then
    echo "[hermes] 'hermes' not on PATH — checking common locations..."
    for p in /root/.hermes/bin/hermes /root/.local/bin/hermes /usr/local/bin/hermes; do
        [ -x "$p" ] && HERMES="$p" && break
    done
fi
if [ -z "$HERMES" ]; then
    echo "[hermes] install did not produce a CLI; dropping to a shell so you can inspect."
    exec sleep infinity
fi
echo "[hermes] using $HERMES"

# Configure a local OpenAI-compatible model (Ollama qwen3.5) — no OAuth portal.
mkdir -p /root/.hermes
"$HERMES" config set model.provider custom            2>/dev/null || true
"$HERMES" config set model.base_url "$OPENAI_BASE_URL" 2>/dev/null || true
"$HERMES" config set model.name qwen3.5               2>/dev/null || true
printf 'OPENAI_API_KEY=%s\n' "${OPENAI_API_KEY:-local}" > /root/.hermes/.env

# Install the CortexDB memory skill from the mounted repo (agentskills.io format).
if [ -d /skills/cortexdb-memory-hermes ]; then
    mkdir -p /root/.hermes/skills/cortexdb
    cp -r /skills/cortexdb-memory-hermes /root/.hermes/skills/cortexdb/ 2>/dev/null || true
    echo "[hermes] installed skill: cortexdb-memory-hermes"
fi
pip3 install --quiet --break-system-packages cortexdb-client 2>/dev/null || \
    pip3 install --quiet cortexdb-client 2>/dev/null || true

echo "[hermes] ready. Try:  docker compose exec hermes hermes chat"
echo "[hermes] model=qwen3.5 via $OPENAI_BASE_URL  · sidecar=$CORTEXDB_GRPC_ENDPOINT"
exec sleep infinity
