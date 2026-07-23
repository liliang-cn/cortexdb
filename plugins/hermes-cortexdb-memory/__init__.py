"""Native Hermes MemoryProvider backed by the CortexDB gRPC sidecar."""

from __future__ import annotations

import json
import logging
import os
import threading
import uuid
from typing import Any, Dict, List, Optional

from agent.memory_provider import MemoryProvider
from cortexdb_client import CortexClient, proto

logger = logging.getLogger(__name__)


def _tool_error(message: str) -> str:
    return json.dumps({"error": message})


class CortexDBMemoryProvider(MemoryProvider):
    def __init__(self) -> None:
        self._client: Optional[CortexClient] = None
        self._session_id = ""
        self._endpoint = "127.0.0.1:47821"
        self._user_id = "default"
        self._scope = "user"
        self._namespace = "hermes"
        self._top_k = 8
        self._max_context_chars = 12000
        self._auto_capture = True
        self._threads: List[threading.Thread] = []
        self._lock = threading.Lock()

    @property
    def name(self) -> str:
        return "cortexdb"

    def is_available(self) -> bool:
        return bool(os.environ.get("CORTEXDB_GRPC_ENDPOINT", "127.0.0.1:47821"))

    def initialize(self, session_id: str, **kwargs) -> None:
        del kwargs
        self._session_id = session_id
        self._endpoint = os.environ.get("CORTEXDB_GRPC_ENDPOINT", "127.0.0.1:47821")
        self._user_id = os.environ.get("CORTEXDB_USER_ID", "default")
        self._scope = os.environ.get("CORTEXDB_MEMORY_SCOPE", "user")
        self._namespace = os.environ.get("CORTEXDB_MEMORY_NAMESPACE", "hermes")
        self._top_k = max(1, int(os.environ.get("CORTEXDB_MEMORY_TOP_K", "8")))
        self._max_context_chars = max(256, int(os.environ.get("CORTEXDB_MAX_CONTEXT_CHARS", "12000")))
        self._auto_capture = os.environ.get("CORTEXDB_AUTO_CAPTURE", "true").lower() not in {"0", "false", "off", "no"}
        token = os.environ.get("CORTEXDB_GRPC_TOKEN")
        self._client = CortexClient.connect(self._endpoint, token=token)

    def system_prompt_block(self) -> str:
        return (
            "# CortexDB Memory\n"
            "CortexDB is the active long-term memory provider. Relevant context is "
            "prefetched automatically. Use cortexdb_search for explicit recall and "
            "cortexdb_remember when durable information should survive future sessions."
        )

    def _call(self, name: str, args: dict) -> dict:
        if self._client is None:
            raise RuntimeError("CortexDB provider is not initialized")
        response = self._client.tools.CallTool(proto.CallToolRequest(
            name=name, args_json=json.dumps(args),
        ))
        return json.loads(response.result_json or "{}")

    def _recall(self, query: str, session_id: str = "") -> dict:
        return self._call("knowledge_memory_recall", {
            "query": query,
            "user_id": self._user_id,
            "session_id": session_id or self._session_id,
            "scope": self._scope,
            "namespace": self._namespace,
            "top_k_memories": self._top_k,
            "top_k_knowledge": self._top_k,
            "max_context_chars": self._max_context_chars,
        })

    def prefetch(self, query: str, *, session_id: str = "") -> str:
        if not query.strip() or self._client is None:
            return ""
        try:
            context = self._recall(query, session_id).get("context_pack", {}).get("text", "")
            if not context:
                return ""
            return (
                "<cortexdb-context>\n"
                "Use this long-term memory silently when relevant. Do not claim certainty "
                "beyond the stored sources.\n\n"
                f"{context}\n</cortexdb-context>"
            )
        except Exception:
            logger.debug("CortexDB prefetch failed", exc_info=True)
            return ""

    def _store(self, content: str, *, session_id: str = "", metadata: Optional[dict] = None) -> dict:
        memory_id = f"hermes-{uuid.uuid4().hex}"
        return self._call("knowledge_memory_remember", {
            "memory_id": memory_id,
            "user_id": self._user_id,
            "session_id": session_id or self._session_id,
            "scope": self._scope,
            "namespace": self._namespace,
            "content": content,
            "metadata": {"source": "hermes", **(metadata or {})},
        })

    def sync_turn(self, user_content: str, assistant_content: str, *,
                  session_id: str = "", messages=None) -> None:
        del messages
        if not self._auto_capture or self._client is None:
            return
        user = (user_content or "").strip()
        assistant = (assistant_content or "").strip()
        if not user and not assistant:
            return
        content = f"User: {user}\nAssistant: {assistant}".strip()

        def run() -> None:
            try:
                self._store(content, session_id=session_id, metadata={"type": "conversation_turn"})
            except Exception:
                logger.debug("CortexDB turn sync failed", exc_info=True)

        thread = threading.Thread(target=run, daemon=False, name="cortexdb-memory-sync")
        with self._lock:
            self._threads = [item for item in self._threads if item.is_alive()]
            self._threads.append(thread)
        thread.start()

    def on_memory_write(self, action: str, target: str, content: str,
                        metadata=None) -> None:
        if action != "add" or not (content or "").strip() or self._client is None:
            return
        try:
            self._store(content.strip(), metadata={"target": target, **(metadata or {})})
        except Exception:
            logger.debug("CortexDB explicit memory mirror failed", exc_info=True)

    def get_tool_schemas(self) -> List[Dict[str, Any]]:
        return [
            {
                "name": "cortexdb_search",
                "description": "Recall fused CortexDB memory, knowledge, and graph facts.",
                "parameters": {
                    "type": "object",
                    "properties": {"query": {"type": "string"}},
                    "required": ["query"],
                },
            },
            {
                "name": "cortexdb_remember",
                "description": "Store durable information in CortexDB memory.",
                "parameters": {
                    "type": "object",
                    "properties": {"content": {"type": "string"}},
                    "required": ["content"],
                },
            },
            {
                "name": "cortexdb_forget",
                "description": "Delete one CortexDB memory by exact memory ID.",
                "parameters": {
                    "type": "object",
                    "properties": {"memory_id": {"type": "string"}},
                    "required": ["memory_id"],
                },
            },
        ]

    def handle_tool_call(self, tool_name: str, args: Dict[str, Any], **kwargs) -> str:
        del kwargs
        try:
            if tool_name == "cortexdb_search":
                query = str(args.get("query") or "").strip()
                if not query:
                    return _tool_error("query is required")
                response = self._recall(query)
                return json.dumps({
                    "context": response.get("context_pack", {}).get("text", ""),
                    "memories": response.get("memories", []),
                    "knowledge": response.get("knowledge", []),
                    "graph_facts": response.get("graph_facts", []),
                })
            if tool_name == "cortexdb_remember":
                content = str(args.get("content") or "").strip()
                if not content:
                    return _tool_error("content is required")
                return json.dumps(self._store(content, metadata={"type": "explicit_memory"}))
            if tool_name == "cortexdb_forget":
                memory_id = str(args.get("memory_id") or "").strip()
                if not memory_id:
                    return _tool_error("memory_id is required")
                return json.dumps(self._call("memory_delete", {"memory_id": memory_id}))
            return _tool_error(f"Unknown tool: {tool_name}")
        except Exception as exc:
            return _tool_error(str(exc))

    def shutdown(self) -> None:
        with self._lock:
            threads = list(self._threads)
            self._threads = []
        for thread in threads:
            if thread.is_alive():
                thread.join(timeout=5.0)
        if self._client is not None:
            self._client.close()
            self._client = None


def register(ctx) -> None:
    ctx.register_memory_provider(CortexDBMemoryProvider())
