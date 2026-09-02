# cortexdb-grpc as a container image.
#
#   docker build -f deploy/docker/Dockerfile -t cortexdb-grpc:local .
#
# The build context is the repository root. The result is a static binary on
# Alpine: no CGO, because modernc.org/sqlite is pure Go, and ca-certificates
# only because the optional embedder talks to an HTTPS endpoint.

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cortexdb-grpc ./cmd/cortexdb-grpc

FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
 && adduser -D -u 10001 cortex \
 && mkdir -p /data && chown cortex /data
COPY --from=build /out/cortexdb-grpc /usr/local/bin/cortexdb-grpc
VOLUME /data
USER cortex

# Defaults, all overridable at run time with -e / environment:.
# Binding 0.0.0.0 inside the container is what makes -p work; what the port is
# reachable from is decided by the publish flag, not by this line. Change the
# port by setting CORTEXDB_GRPC_ADDR — the healthcheck below reads the same
# variable, so the probe follows the port automatically.
ENV CORTEXDB_PATH=/data/cortexdb.db \
    CORTEXDB_GRPC_ADDR=0.0.0.0:47821
EXPOSE 47821

# The server binary is its own probe, so nothing else has to be in the image.
# It dials loopback and passes CORTEXDB_GRPC_TOKEN, so it works on a locked-down
# server too.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["cortexdb-grpc", "-health"]

ENTRYPOINT ["cortexdb-grpc"]
