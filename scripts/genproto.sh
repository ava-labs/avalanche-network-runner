#!/usr/bin/env bash
set -e

if ! [[ "$0" =~ scripts/genproto.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

go install -v google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install -v github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
go install -v google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# https://docs.buf.build/installation
# https://grpc-ecosystem.github.io/grpc-gateway/docs/tutorials/introduction/
buf lint
buf generate

echo "ALL SUCCESS"
