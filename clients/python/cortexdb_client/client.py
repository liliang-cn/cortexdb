"""The CortexClient facade — mirrors the Rust crate's sub-client layout."""

from __future__ import annotations

import uuid
from typing import Optional

import grpc

from ._pb.cortexdb.v1 import admin_pb2_grpc, graph_pb2_grpc, graphrag_pb2_grpc
from ._pb.cortexdb.v1 import knowledge_pb2_grpc, memory_pb2_grpc, tools_pb2_grpc


def _bearer_interceptor(token: str) -> grpc.UnaryUnaryClientInterceptor:
    """Attach ``authorization: Bearer <token>`` to every unary call."""

    class _Interceptor(grpc.UnaryUnaryClientInterceptor):
        def intercept_unary_unary(self, continuation, client_call_details, request):
            metadata = list(client_call_details.metadata or [])
            metadata.append(("authorization", f"Bearer {token}"))
            new_details = client_call_details._replace(metadata=metadata)
            return continuation(new_details, request)

    return _Interceptor()


REQUEST_ID_HEADER = "x-request-id"


class _RequestIDInterceptor(grpc.UnaryUnaryClientInterceptor):
    """Send a request id with every unary call, unless the caller set one.

    The server logs the id and returns it; here it is also put on what the
    call returns as ``request_id`` — on the raised ``grpc.RpcError`` too — so
    a reply that went wrong can be matched to the server's record of it.
    """

    def intercept_unary_unary(self, continuation, client_call_details, request):
        metadata = list(client_call_details.metadata or [])
        request_id = next((v for k, v in metadata if k == REQUEST_ID_HEADER), None)
        if request_id is None:
            request_id = uuid.uuid4().hex
            metadata.append((REQUEST_ID_HEADER, request_id))
        outcome = continuation(client_call_details._replace(metadata=metadata), request)
        try:
            outcome.request_id = request_id
        except AttributeError:
            pass
        return outcome


class CortexClient:
    """One connection to a CortexDB sidecar, exposing all service clients.

    Construct with :meth:`connect`. Use as a context manager to close the
    channel automatically, or call :meth:`close` yourself.
    """

    def __init__(self, channel: grpc.Channel):
        self._channel = channel
        self.knowledge = knowledge_pb2_grpc.KnowledgeServiceStub(channel)
        self.memory = memory_pb2_grpc.MemoryServiceStub(channel)
        self.graph = graph_pb2_grpc.KnowledgeGraphServiceStub(channel)
        self.graphrag = graphrag_pb2_grpc.GraphRagServiceStub(channel)
        self.tools = tools_pb2_grpc.ToolsServiceStub(channel)
        self.admin = admin_pb2_grpc.AdminServiceStub(channel)

    @classmethod
    def connect(
        cls,
        endpoint: str,
        token: Optional[str] = None,
        *,
        secure: bool = False,
        credentials: Optional[grpc.ChannelCredentials] = None,
    ) -> "CortexClient":
        """Connect to ``endpoint`` (``host:port``, optional ``http(s)://`` prefix).

        Pass ``token`` to authenticate every request with a bearer token. The
        sidecar listens on plaintext by default; set ``secure=True`` (and
        optionally ``credentials``) when fronting it with TLS.
        """
        target = endpoint
        for prefix in ("http://", "https://"):
            if target.startswith(prefix):
                target = target[len(prefix):]
                break

        if secure:
            channel = grpc.secure_channel(target, credentials or grpc.ssl_channel_credentials())
        else:
            channel = grpc.insecure_channel(target)

        channel = grpc.intercept_channel(channel, _RequestIDInterceptor())
        if token:
            channel = grpc.intercept_channel(channel, _bearer_interceptor(token))

        return cls(channel)

    def close(self) -> None:
        self._channel.close()

    def __enter__(self) -> "CortexClient":
        return self

    def __exit__(self, *exc) -> None:
        self.close()
