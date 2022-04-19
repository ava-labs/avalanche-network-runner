#!/usr/bin/env bash
set -xue

if ! [[ "$0" =~ scripts/static-analysis.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

# git submodule add https://github.com/googleapis/googleapis
git submodule update --init --remote

# check https://www.rust-lang.org/tools/install for Rust compiler installation
# e.g.,
# curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
#
# install Rust nightly builds https://rust-lang.github.io/rustup/installation/index.html
# e.g.,
# rustup toolchain install nightly --allow-downgrade --profile minimal --component clippy
#
# https://github.com/rust-lang/rustfmt
# rustup component add rustfmt
# rustup component add rustfmt --toolchain nightly
# rustup component add clippy
# rustup component add clippy --toolchain nightly

rustup default stable
cargo fmt --all --verbose -- --check

# TODO: enable nightly fmt
rustup default nightly
cargo +nightly fmt --all -- --config-path .rustfmt.nightly.toml --verbose --check || true

# TODO: remove "|| true"
cargo +nightly clippy --all --all-features -- -D warnings || true

rustup default stable

echo "ALL SUCCESS!"
