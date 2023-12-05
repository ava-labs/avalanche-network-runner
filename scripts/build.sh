#!/bin/bash

# Run with ./scripts/build.sh <optional_build_location>

if ! [[ "$0" =~ scripts/build.sh ]]; then
  echo "must be run from repository root"
  exit 1
fi

VERSION=`cat VERSION`

if [ $# -eq 0 ] ; then
    OUTPUT="bin"
else
    OUTPUT=$1
fi

# Check for CGO_ENABLED
if [[ $(go env CGO_ENABLED) = 0 ]]; then
        echo "must have installed gcc (linux), clang (macos), or have set CC to an appropriate C compiler"
        exit 1
fi

# Set the CGO flags to use the portable version of BLST
#
# We use "export" here instead of just setting a bash variable because we need
# to pass this flag to all child processes spawned by the shell.
export CGO_CFLAGS="-O -D__BLST_PORTABLE__"

go build -v -ldflags="-X 'github.com/ava-labs/avalanche-network-runner/cmd.Version=$VERSION'" -o $OUTPUT/avalanche-network-runner
