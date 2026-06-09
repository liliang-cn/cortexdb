"""Minimal 'memory layer for a Python agent' example (Hermes-style).

Start a sidecar first:
    go run ./cmd/cortexdb-grpc -db agent.db
Then:
    uv run --with .. python examples/agent_memory.py
Env: CORTEXDB_GRPC_ENDPOINT (default 127.0.0.1:47821), CORTEXDB_GRPC_TOKEN
"""

import os

from cortexdb_client import CortexClient, proto


def main():
    endpoint = os.environ.get("CORTEXDB_GRPC_ENDPOINT", "127.0.0.1:47821")
    token = os.environ.get("CORTEXDB_GRPC_TOKEN")
    client = CortexClient.connect(endpoint, token=token)

    info = client.admin.Info(proto.InfoRequest())
    print(f"connected: cortexdb {info.version} (embedder={info.has_embedder})")

    # The agent remembers something about the user.
    client.memory.SaveMemory(proto.SaveMemoryRequest(
        memory_id="pref-editor",
        user_id="alice",
        scope="user",
        content="Alice prefers Python and dislikes heavy frameworks.",
    ))

    # Later turn: recall it.
    recall = client.memory.SearchMemory(proto.SearchMemoryRequest(
        query="what does the user like to work with?",
        user_id="alice", scope="user", top_k=3,
    ))
    print("recalled:")
    for hit in recall.results:
        print(f"  - {hit.memory.content}  (score {hit.score:.3f})")

    client.close()


if __name__ == "__main__":
    main()
