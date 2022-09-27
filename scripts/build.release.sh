#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

if ! [[ "$0" =~ scripts/build.release.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

if [ "${1-}" != linux ] && [ "${1-}" != darwin ]; then
    echo "arg must be linux or darwin"
    exit 255
fi
os=${1-}

# https://goreleaser.com/install/
go install -v github.com/goreleaser/goreleaser@latest

# e.g.,
# git tag 1.0.0
#goreleaser release --config .goreleaser-$os.yml --skip-announce --skip-publish

# to test without git tags
goreleaser release --config .goreleaser-$os.yml --rm-dist --skip-announce --skip-publish --snapshot
