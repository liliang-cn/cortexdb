"""Typed gRPC client for the CortexDB sidecar (``cortexdb-grpc``).

Example::

    from cortexdb_client import CortexClient
    from cortexdb_client import proto

    with CortexClient.connect("127.0.0.1:47821", token="s3cret") as client:
        client.knowledge.save_knowledge(
            proto.SaveKnowledgeRequest(knowledge_id="k1", content="hello from python")
        )
        hits = client.knowledge.search_knowledge(
            proto.SearchKnowledgeRequest(query="hello", top_k=3)
        )

The sub-client accessors mirror the Rust crate: ``client.knowledge``,
``client.memory``, ``client.graph``, ``client.graphrag``, ``client.tools``,
``client.admin``.

``cortexdb_client.tabular`` turns a tool result into flat records, a pandas
DataFrame or CSV.
"""

from .client import CortexClient
from . import proto
from . import tabular

__all__ = ["CortexClient", "proto", "tabular"]
__version__ = "0.1.0"
