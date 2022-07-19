#!/usr/bin/env bash
set -e

export RUN_E2E="true"
# e.g.,
# ./scripts/tests.e2e.sh 1.7.9 1.7.10
if ! [[ "$0" =~ scripts/tests.e2e.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

VERSION_1=$1
if [[ -z "${VERSION_1}" ]]; then
  echo "Missing version argument!"
  echo "Usage: ${0} [VERSION_1] [VERSION_2]" >> /dev/stderr
  exit 255
fi

VERSION_2=$2
if [[ -z "${VERSION_2}" ]]; then
  echo "Missing version argument!"
  echo "Usage: ${0} [VERSION_1] [VERSION_2]" >> /dev/stderr
  exit 255
fi

echo "Running e2e tests with:"
echo VERSION_1: ${VERSION_1}
echo VERSION_2: ${VERSION_2}

if [ ! -f /tmp/avalanchego-v${VERSION_1}/avalanchego ]
then
    ############################
    # download avalanchego
    # https://github.com/ava-labs/avalanchego/releases
    GOARCH=$(go env GOARCH)
    GOOS=$(go env GOOS)
    DOWNLOAD_URL=https://github.com/ava-labs/avalanchego/releases/download/v${VERSION_1}/avalanchego-linux-${GOARCH}-v${VERSION_1}.tar.gz
    DOWNLOAD_PATH=/tmp/avalanchego.tar.gz
    if [[ ${GOOS} == "darwin" ]]; then
      DOWNLOAD_URL=https://github.com/ava-labs/avalanchego/releases/download/v${VERSION_1}/avalanchego-macos-v${VERSION_1}.zip
      DOWNLOAD_PATH=/tmp/avalanchego.zip
    fi

    rm -rf /tmp/avalanchego-v${VERSION_1}
    rm -rf /tmp/avalanchego-build
    rm -f ${DOWNLOAD_PATH}

    echo "downloading avalanchego ${VERSION_1} at ${DOWNLOAD_URL}"
    curl -L ${DOWNLOAD_URL} -o ${DOWNLOAD_PATH}

    echo "extracting downloaded avalanchego"
    if [[ ${GOOS} == "linux" ]]; then
      tar xzvf ${DOWNLOAD_PATH} -C /tmp
    elif [[ ${GOOS} == "darwin" ]]; then
      unzip ${DOWNLOAD_PATH} -d /tmp/avalanchego-build
      mv /tmp/avalanchego-build/build /tmp/avalanchego-v${VERSION_1}
    fi
    find /tmp/avalanchego-v${VERSION_1}
fi

if [ ! -f /tmp/avalanchego-v${VERSION_2}/avalanchego ]
then
    ############################
    # download avalanchego
    # https://github.com/ava-labs/avalanchego/releases
    DOWNLOAD_URL=https://github.com/ava-labs/avalanchego/releases/download/v${VERSION_2}/avalanchego-linux-${GOARCH}-v${VERSION_2}.tar.gz
    if [[ ${GOOS} == "darwin" ]]; then
      DOWNLOAD_URL=https://github.com/ava-labs/avalanchego/releases/download/v${VERSION_2}/avalanchego-macos-v${VERSION_2}.zip
      DOWNLOAD_PATH=/tmp/avalanchego.zip
    fi

    rm -rf /tmp/avalanchego-v${VERSION_2}
    rm -rf /tmp/avalanchego-build
    rm -f ${DOWNLOAD_PATH}

    echo "downloading avalanchego ${VERSION_2} at ${DOWNLOAD_URL}"
    curl -L ${DOWNLOAD_URL} -o ${DOWNLOAD_PATH}

    echo "extracting downloaded avalanchego"
    if [[ ${GOOS} == "linux" ]]; then
      tar xzvf ${DOWNLOAD_PATH} -C /tmp
    elif [[ ${GOOS} == "darwin" ]]; then
      unzip ${DOWNLOAD_PATH} -d /tmp/avalanchego-build
      mv /tmp/avalanchego-build/build /tmp/avalanchego-v${VERSION_2}
    fi
    find /tmp/avalanchego-v${VERSION_2}
fi

############################
echo "building runner"
./scripts/build.sh

echo "building e2e.test"
# to install the ginkgo binary (required for test build and run)
go install -v github.com/onsi/ginkgo/v2/ginkgo@v2.1.3
ACK_GINKGO_RC=true ginkgo build ./tests/e2e
./tests/e2e/e2e.test --help

snapshots_dir=/tmp/avalanche-network-runner-snapshots-e2e/
rm -rf $snapshots_dir

killall network.runner || echo

echo "launch local test cluster in the background"
bin/avalanche-network-runner \
server \
--log-level debug \
--port=":8080" \
--snapshots-dir=$snapshots_dir \
--grpc-gateway-port=":8081" &
#--disable-nodes-output \
PID=${!}

echo "running e2e tests"
./tests/e2e/e2e.test \
--ginkgo.v \
--log-level debug \
--grpc-endpoint="0.0.0.0:8080" \
--grpc-gateway-endpoint="0.0.0.0:8081" \
--avalanchego-path-1=/tmp/avalanchego-v${VERSION_1}/avalanchego \
--avalanchego-path-2=/tmp/avalanchego-v${VERSION_2}/avalanchego

kill -9 ${PID}
echo "ALL SUCCESS!"
