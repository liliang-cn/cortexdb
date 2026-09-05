#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

protoc \
  --proto_path=proto \
  --go_out=. --go_opt=module=github.com/liliang-cn/cortexdb/v2 \
  --go-grpc_out=. --go-grpc_opt=module=github.com/liliang-cn/cortexdb/v2 \
  proto/cortexdb/v1/*.proto

# Vendor protos into the Rust crate (used by the dev-only generator: cargo run -p gen)
mkdir -p clients/rust/cortexdb-client/proto/cortexdb/v1
cp proto/cortexdb/v1/*.proto clients/rust/cortexdb-client/proto/cortexdb/v1/

# And into the Node client, which loads them at runtime with @grpc/proto-loader.
# It was copied by hand before, which is how it came to be missing an RPC that
# proto/ has had for a while: a vendored copy nothing syncs is a stale copy.
mkdir -p clients/node/proto/cortexdb/v1
cp proto/cortexdb/v1/*.proto clients/node/proto/cortexdb/v1/

echo "OK: pkg/rpc/v1 regenerated; protos vendored to clients/rust and clients/node"
