#!/usr/bin/env bash
set -e

export RUN_E2E="true"

# e.g.,
# ./scripts/tests.e2e.sh $VERSON1 $VERSION2 $SUBNET_EVM_VERSION
if ! [[ "$0" =~ scripts/tests.e2e.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

ANR_PATH=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# this avago and subnet-evm versions must have compatibility at rpc protocol level
DEFAULT_VERSION_1=v1.10.15
DEFAULT_SUBNET_EVM_VERSION=v0.5.9

# used standalone, without interaction with subnet-evm, no compatibility needed
DEFAULT_VERSION_2=v1.10.14

if [ $# == 0 ]; then
    VERSION_1=$DEFAULT_VERSION_1
    VERSION_2=$DEFAULT_VERSION_2
    SUBNET_EVM_VERSION=$DEFAULT_SUBNET_EVM_VERSION
else
    VERSION_1=$1
    if [[ -z "${VERSION_1}" ]]; then
      echo "Missing version argument!"
      echo "Usage: ${0} [VERSION_1] [VERSION_2] [SUBNET_EVM_VERSION]" >> /dev/stderr
      exit 255
    fi
    VERSION_2=$2
    if [[ -z "${VERSION_2}" ]]; then
      echo "Missing version argument!"
      echo "Usage: ${0} [VERSION_1] [VERSION_2] [SUBNET_EVM_VERSION]" >> /dev/stderr
      exit 255
    fi
    SUBNET_EVM_VERSION=$3
    if [[ -z "${SUBNET_EVM_VERSION}" ]]; then
      echo "Missing version argument!"
      echo "Usage: ${0} [VERSION_1] [VERSION_2] [SUBNET_EVM_VERSION]" >> /dev/stderr
      exit 255
    fi
fi

echo "Running e2e tests with:"
echo VERSION_1: ${VERSION_1}
echo VERSION_2: ${VERSION_2}
echo SUBNET_EVM_VERSION: ${SUBNET_EVM_VERSION}

#
# Set the CGO flags to use the portable version of BLST
#
# We use "export" here instead of just setting a bash variable because we need
# to pass this flag to all child processes spawned by the shell.
export CGO_CFLAGS="-O -D__BLST_PORTABLE__"

############################
AVALANCHEGO_REPO=/tmp/avalanchego-repo/

if [ ! -d $AVALANCHEGO_REPO ]
then
    git clone https://github.com/ava-labs/avalanchego/ $AVALANCHEGO_REPO
fi

VERSION_1_DIR=/tmp/avalanchego-${VERSION_1}/
if [ ! -f ${VERSION_1_DIR}/avalanchego ]
then
    echo building avalanchego $VERSION_1
    rm -rf ${VERSION_1_DIR}
    mkdir -p ${VERSION_1_DIR}
    cd $AVALANCHEGO_REPO
    git checkout $VERSION_1
    ./scripts/build.sh 
    cp -r build/* ${VERSION_1_DIR}
fi

VERSION_2_DIR=/tmp/avalanchego-${VERSION_2}/
if [ ! -f ${VERSION_2_DIR}/avalanchego ]
then
    echo building avalanchego $VERSION_2
    rm -rf ${VERSION_2_DIR}
    mkdir -p ${VERSION_2_DIR}
    cd $AVALANCHEGO_REPO
    git checkout $VERSION_2
    ./scripts/build.sh 
    cp -r build/* ${VERSION_2_DIR}
fi

SUBNET_EVM_REPO=/tmp/subnet-evm-repo/
if [ ! -d $SUBNET_EVM_REPO ]
then
    git clone https://github.com/ava-labs/subnet-evm/ $SUBNET_EVM_REPO
fi

SUBNET_EVM_VERSION_DIR=/tmp/subnet-evm-${SUBNET_EVM_VERSION}/
if [ ! -f $VERSION_1_DIR/plugins/srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy ]
then
    echo building subnet-evm $SUBNET_EVM_VERSION
    rm -rf ${SUBNET_EVM_VERSION_DIR}
    mkdir -p ${SUBNET_EVM_VERSION_DIR}
    cd $SUBNET_EVM_REPO
    git checkout $SUBNET_EVM_VERSION
    # NOTE: We are copying the subnet-evm binary here to a plugin hardcoded as srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy which corresponds to the VM name `subnetevm` used as such in the test
    ./scripts/build.sh $VERSION_1_DIR/plugins/srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy
fi

############################

cd $ANR_PATH

echo "building runner"
./scripts/build.sh

echo "building e2e.test"
# to install the ginkgo binary (required for test build and run)
go install -v github.com/onsi/ginkgo/v2/ginkgo@v2.1.3
ACK_GINKGO_RC=true ginkgo build ./tests/e2e
./tests/e2e/e2e.test --help

snapshots_dir=/tmp/network-runner-root-data/snapshots-e2e/
rm -rf $snapshots_dir

killall avalanche-network-runner || true

echo "launch local test cluster in the background"
bin/avalanche-network-runner \
server \
--log-level debug \
--port=":8080" \
--snapshots-dir=$snapshots_dir \
--grpc-gateway-port=":8081" &
#--disable-nodes-output \
PID=${!}

function cleanup()
{
  echo "shutting down network runner"
  kill ${PID}
}
trap cleanup EXIT

echo "running e2e tests"
./tests/e2e/e2e.test \
--ginkgo.v \
--ginkgo.fail-fast \
--log-level debug \
--grpc-endpoint="0.0.0.0:8080" \
--grpc-gateway-endpoint="0.0.0.0:8081" \
--avalanchego-path-1=${VERSION_1_DIR}/avalanchego \
--avalanchego-path-2=${VERSION_2_DIR}/avalanchego
