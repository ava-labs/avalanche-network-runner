#!/usr/bin/env bash

set -o errexit
set -o pipefail
set -e

if ! [[ "$0" =~ scripts/lint.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

if [ "$#" -eq 0 ]; then
  # by default, check all source code
  # to test only "node" package do:
  # ./scripts/lint.sh ./node/...
  TARGET="./..."
else
  TARGET="${1}"
fi

TESTS=${TESTS:-"golangci_lint"}

function test_golangci_lint {
  go install -v github.com/golangci/golangci-lint/cmd/golangci-lint@v1.49.0
  golangci-lint run --config .golangci.yml
}

# find_go_files [package]
# all go files except generated ones
function find_go_files {
  local target="${1}"
  go fmt -n "${target}"  | grep -Eo "([^ ]*)$" | grep -vE "(\\.pb\\.go|\\.pb\\.gw.go)"
}

function run {
  local test="${1}"
  shift 1
  echo "START: '${test}' at $(date)"
  if "test_${test}" "$@" ; then
    echo "SUCCESS: '${test}' completed at $(date)"
  else
    echo "FAIL: '${test}' failed at $(date)"
    exit 255
  fi
}

echo "Running '$TESTS' at: $(date)"
for test in $TESTS; do
  run "${test}" "${TARGET}"
done

echo "ALL SUCCESS!"
