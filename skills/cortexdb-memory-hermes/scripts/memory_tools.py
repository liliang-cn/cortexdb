"""Drop-in CortexDB memory tools for a Python agent (Hermes-style).

Each function is a thin wrapper over the cortexdb-client gRPC client, returning
plain Python values so they slot straight into a tool-calling loop.

Prereqs:
    pip install cortexdb-client
    # and a running sidecar, e.g.:
    #   CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc

Config via env: CORTEXDB_GRPC_ENDPOINT (default 127.0.0.1:47821),
CORTEXDB_GRPC_TOKEN.
"""

from __future__ import annotations

import os
import json
import uuid
from typing import Optional

from cortexdb_client import CortexClient, proto

_client: Optional[CortexClient] = None


def _c() -> CortexClient:
    global _client
    if _client is None:
        endpoint = os.environ.get("CORTEXDB_GRPC_ENDPOINT", "127.0.0.1:47821")
        token = os.environ.get("CORTEXDB_GRPC_TOKEN")
        _client = CortexClient.connect(endpoint, token=token)
    return _client


def call_tool(name: str, args: dict) -> dict:
    """Call one CortexDB generic tool and decode its JSON response."""
    res = _c().tools.CallTool(proto.CallToolRequest(
        name=name, args_json=json.dumps(args),
    ))
    return json.loads(res.result_json or "{}")


def remember(content: str, user_id: str = "default", scope: str = "user",
             importance: float = 0.0) -> str:
    """Store a memory about the user. Returns the memory id."""
    mid = f"mem-{uuid.uuid4().hex[:12]}"
    _c().memory.SaveMemory(proto.SaveMemoryRequest(
        memory_id=mid, user_id=user_id, scope=scope,
        content=content, importance=importance,
    ))
    return mid


def recall(query: str, user_id: str = "default", scope: str = "user",
           top_k: int = 5) -> list[dict]:
    """Recall memories by meaning. Returns [{content, score}]."""
    res = recall_context(query, user_id=user_id, scope=scope,
                         top_k_memories=top_k)
    return [{"id": h.get("memory", {}).get("id"),
             "content": h.get("memory", {}).get("content", ""),
             "score": h.get("score", 0)} for h in res.get("memories", [])]


def recall_context(query: str, user_id: str = "default", session_id: str = "",
                   scope: str = "user", namespace: str = "",
                   top_k_memories: int = 5, top_k_knowledge: int = 5,
                   max_context_chars: int = 8000) -> dict:
    """Recall fused memory, knowledge, graph facts, and prompt-ready context."""
    return call_tool("knowledge_memory_recall", {
        "query": query, "user_id": user_id, "session_id": session_id,
        "scope": scope, "namespace": namespace,
        "top_k_memories": top_k_memories,
        "top_k_knowledge": top_k_knowledge,
        "max_context_chars": max_context_chars,
    })


def remember_context(content: str, memory_id: str = "", user_id: str = "default",
                     session_id: str = "", scope: str = "user",
                     namespace: str = "", importance: float = 0.0,
                     metadata: Optional[dict] = None) -> str:
    """Store through the unified KnowledgeMemory facade."""
    mid = memory_id or f"mem-{uuid.uuid4().hex[:12]}"
    res = call_tool("knowledge_memory_remember", {
        "memory_id": mid, "user_id": user_id, "session_id": session_id,
        "scope": scope, "namespace": namespace, "content": content,
        "importance": importance, "metadata": metadata or {},
    })
    memory = res.get("memory", {})
    return memory.get("id") or memory.get("memory_id") or mid


def save_knowledge(content: str, title: str = "", knowledge_id: str = "") -> str:
    """Store a durable knowledge document (chunked + indexed). Returns its id."""
    kid = knowledge_id or f"kn-{uuid.uuid4().hex[:12]}"
    _c().knowledge.SaveKnowledge(proto.SaveKnowledgeRequest(
        knowledge_id=kid, title=title, content=content,
    ))
    return kid


def search_knowledge(query: str, top_k: int = 5) -> list[dict]:
    """GraphRAG search over saved knowledge. Returns [{id, title, snippet, score}]."""
    res = _c().knowledge.SearchKnowledge(proto.SearchKnowledgeRequest(
        query=query, top_k=top_k,
    ))
    return [{"id": h.knowledge_id, "title": h.title, "snippet": h.snippet,
             "score": h.score} for h in res.results]


def relate(subject: str, predicate: str, obj: str,
           namespace: str = "https://example.com/", prefix: str = "ex") -> None:
    """Add an entity-relation triple to the knowledge graph.

    Pass CURIEs like "ex:alice" or full IRIs. Strings without ':' are treated as
    literals on the object position.
    """
    _c().graph.UpsertNamespace(proto.UpsertNamespaceRequest(prefix=prefix, uri=namespace))

    def term(v: str, allow_literal: bool = False) -> proto.RdfTerm:
        if allow_literal and ":" not in v:
            return proto.RdfTerm(kind="literal", value=v)
        return proto.RdfTerm(kind="iri", value=v)

    _c().graph.UpsertKnowledgeGraph(proto.UpsertKnowledgeGraphRequest(triples=[
        proto.RdfTriple(subject=term(subject), predicate=term(predicate),
                        object=term(obj, allow_literal=True)),
    ]))


def ask_graph(sparql: str) -> dict:
    """Run a SPARQL SELECT/ASK over the knowledge graph. Returns a summary dict."""
    res = _c().graph.QuerySparql(proto.QuerySparqlRequest(query=sparql)).result
    bindings = [{k: v.value for k, v in b.vars.items()} for b in res.bindings]
    return {"count": res.count, "vars": list(res.vars), "bindings": bindings,
            "boolean": res.boolean}


if __name__ == "__main__":
    # tiny smoke run against a lexical-mode sidecar
    print("remember:", remember("User prefers dark roast coffee.", user_id="alice"))
    print("recall:", recall("what does the user drink?", user_id="alice"))
    relate("ex:alice", "ex:knows", "ex:bob")
    print("ask_graph:", ask_graph(
        "SELECT ?o WHERE { <https://example.com/alice> <https://example.com/knows> ?o . }"))
