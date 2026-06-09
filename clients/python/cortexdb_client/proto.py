"""Re-exports of the generated ``cortexdb.v1`` protobuf message types.

Use these as ``proto.SaveKnowledgeRequest(...)`` etc. The grpc service stubs
live alongside but are wired up by :mod:`cortexdb_client.client`; application
code should only need the message types from here.
"""

from ._pb.cortexdb.v1 import common_pb2 as _common
from ._pb.cortexdb.v1 import knowledge_pb2 as _knowledge
from ._pb.cortexdb.v1 import memory_pb2 as _memory
from ._pb.cortexdb.v1 import graph_pb2 as _graph
from ._pb.cortexdb.v1 import graphrag_pb2 as _graphrag
from ._pb.cortexdb.v1 import tools_pb2 as _tools
from ._pb.cortexdb.v1 import admin_pb2 as _admin

_modules = (_common, _knowledge, _memory, _graph, _graphrag, _tools, _admin)

# Flatten every generated message class into this module's namespace so callers
# can write proto.SaveKnowledgeRequest instead of knowledge_pb2.SaveKnowledgeRequest.
__all__ = []
for _m in _modules:
    for _name in getattr(_m, "DESCRIPTOR").message_types_by_name:
        globals()[_name] = getattr(_m, _name)
        __all__.append(_name)
