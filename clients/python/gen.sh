#!/usr/bin/env bash
# Regenerate cortexdb_client/_pb from the shared protos. Run with: uv run --extra dev ./gen.sh
set -euo pipefail
cd "$(dirname "$0")"

PROTO_ROOT="../../proto"
OUT="cortexdb_client/_pb"
mkdir -p "$OUT"

python -m grpc_tools.protoc \
    --proto_path="$PROTO_ROOT" \
    --python_out="$OUT" \
    --grpc_python_out="$OUT" \
    "$PROTO_ROOT"/cortexdb/v1/*.proto

# grpc_tools emits absolute `from cortexdb.v1 import ...`; rewrite to a relative
# package import so the generated code works as cortexdb_client._pb.cortexdb.v1.
find "$OUT/cortexdb" -name '*.py' -exec \
    perl -i -pe 's/^from cortexdb\.v1 import/from cortexdb_client._pb.cortexdb.v1 import/' {} +

# Make every generated dir a package.
find "$OUT" -type d -exec touch {}/__init__.py \;

echo "OK: regenerated $OUT"
