#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${PYTHON_DIR}/.." && pwd)"

PROTO_ROOT="${REPO_ROOT}/memory-service-contracts/src/main/resources"
OUT_ROOT="${PYTHON_DIR}/langchain/memory_service_langchain/grpc"
PROTO_FILE="memory/v1/memory_service.proto"
GRPC_TOOLS_VERSION="${GRPC_TOOLS_VERSION:-1.74.0}"

mkdir -p "${OUT_ROOT}"

uvx --from "grpcio-tools==${GRPC_TOOLS_VERSION}" python -m grpc_tools.protoc \
  -I "${PROTO_ROOT}" \
  --python_out="${OUT_ROOT}" \
  --grpc_python_out="${OUT_ROOT}" \
  "${PROTO_FILE}"

touch "${OUT_ROOT}/__init__.py"
touch "${OUT_ROOT}/memory/__init__.py"
touch "${OUT_ROOT}/memory/v1/__init__.py"

GRPC_STUB_FILE="${OUT_ROOT}/memory/v1/memory_service_pb2_grpc.py"
if grep -q '^from memory.v1 import memory_service_pb2 as ' "${GRPC_STUB_FILE}"; then
  sed -i.bak \
    's/^from memory\.v1 import memory_service_pb2 as /from . import memory_service_pb2 as /' \
    "${GRPC_STUB_FILE}"
  rm -f "${GRPC_STUB_FILE}.bak"
fi
