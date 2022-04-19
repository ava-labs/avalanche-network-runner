#!/usr/bin/env bash
set -xue

if ! [[ "$0" =~ scripts/cargo.unused.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

# git submodule add https://github.com/googleapis/googleapis
git submodule update --init --remote

# https://github.com/est31/cargo-udeps
cargo install cargo-udeps --locked
cargo +nightly udeps

echo "ALL SUCCESS!"
