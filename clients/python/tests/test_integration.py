"""Integration test against a real cortexdb-grpc sidecar.

Set CORTEXDB_GRPC_BIN to the built binary (or place it at
clients/target/cortexdb-grpc). Skips when not found.

Run: uv run --extra dev python -m pytest tests/ -v   (or the bundled run below)
"""

import os
import socket
import subprocess
import time
from pathlib import Path

from cortexdb_client import CortexClient, proto


def _binary():
    env = os.environ.get("CORTEXDB_GRPC_BIN")
    if env and Path(env).exists():
        return env
    fallback = Path(__file__).resolve().parents[2] / "target" / "cortexdb-grpc"
    return str(fallback) if fallback.exists() else None


def _free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def run():
    binary = _binary()
    if not binary:
        print("SKIP: cortexdb-grpc binary not found (set CORTEXDB_GRPC_BIN)")
        return

    port = _free_port()
    db = Path("/tmp") / f"cortexdb-py-it-{os.getpid()}.db"
    db.unlink(missing_ok=True)
    env = dict(os.environ)
    env.pop("OPENAI_BASE_URL", None)  # force lexical mode
    proc = subprocess.Popen(
        [binary, "-db", str(db), "-addr", f"127.0.0.1:{port}", "-token", "py-it"],
        env=env,
    )
    try:
        client = None
        for _ in range(50):
            try:
                c = CortexClient.connect(f"127.0.0.1:{port}", token="py-it")
                c.admin.Health(proto.HealthRequest())
                client = c
                break
            except Exception:
                time.sleep(0.1)
        assert client is not None, "sidecar did not become healthy"

        # wrong token rejected
        import grpc
        bad = CortexClient.connect(f"127.0.0.1:{port}", token="nope")
        try:
            bad.admin.Health(proto.HealthRequest())
            raise AssertionError("expected UNAUTHENTICATED")
        except grpc.RpcError as e:
            assert e.code() == grpc.StatusCode.UNAUTHENTICATED

        info = client.admin.Info(proto.InfoRequest())
        assert info.version
        assert not info.has_embedder

        client.knowledge.SaveKnowledge(proto.SaveKnowledgeRequest(
            knowledge_id="k1",
            title="Python ownership",
            content="Python uses reference counting and a cyclic garbage collector to manage memory.",
        ))
        res = client.knowledge.SearchKnowledge(proto.SearchKnowledgeRequest(
            query="garbage collector memory", top_k=3,
        ))
        assert res.results, "no search results"
        assert res.results[0].knowledge_id == "k1"

        client.memory.SaveMemory(proto.SaveMemoryRequest(
            memory_id="m1", user_id="u1", scope="user",
            content="User prefers 4-space indentation.",
        ))

        client.graph.UpsertNamespace(proto.UpsertNamespaceRequest(prefix="ex", uri="https://example.com/"))
        iri = lambda v: proto.RdfTerm(kind="iri", value=v)
        client.graph.UpsertKnowledgeGraph(proto.UpsertKnowledgeGraphRequest(triples=[
            proto.RdfTriple(subject=iri("ex:alice"), predicate=iri("ex:knows"), object=iri("ex:bob")),
        ]))
        q = client.graph.QuerySparql(proto.QuerySparqlRequest(
            query="SELECT ?o WHERE { <https://example.com/alice> <https://example.com/knows> ?o . }",
        ))
        assert q.result.count == 1

        tools = client.tools.ListTools(proto.ListToolsRequest())
        assert any(t.name == "knowledge_search" for t in tools.tools)

        client.close()
        print("PYTHON E2E OK: admin/auth + knowledge + memory + sparql + tools")
    finally:
        proc.terminate()
        proc.wait(timeout=5)
        db.unlink(missing_ok=True)


if __name__ == "__main__":
    run()
