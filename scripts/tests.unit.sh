#!/usr/bin/env bash
set -e

if ! [[ "$0" =~ scripts/tests.unit.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

go test -v -race -timeout="3m" -coverprofile="coverage.out" -covermode="atomic" $(go list ./... | grep -v tests)
