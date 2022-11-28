# Avalanche Network Runner

## Note

This tool is under heavy development and the documentation/code snippets below may vary slightly from the actual code in the repository. Updates to the documentation may happen some time after an update to the codebase. Nonetheless, this README should provide valuable information about using this tool.

## Overview

This is a tool to run and interact with a local Avalanche network.
This tool may be especially useful for development and testing.

## Installation

The binary will be installed into `$GOPATH/bin`. Be sure that `$GOPATH` is defined in your environment,
and that `$GOPATH/bin` is in your `$PATH`.

To install the latest binary locally, run:

```sh
curl -sSfL https://raw.githubusercontent.com/ava-labs/avalanche-network-runner/main/scripts/install.sh | sh -s -- -b $GOPATH/bin
```

The user should permanently add `$GOPATH/bin` to the `$PATH` variable by adding this to the shell initialization
script (eg `$HOME/.bashrc` for `bash` shell):

```sh
export PATH=$PATH:$GOPATH/bin
```

## Build from source code

This is only needed by advanced users who want to modify or test Avalanche Network Runner in specific ways.

### Download

```sh
git clone https://github.com/ava-labs/avalanche-network-runner.git
```

### Build

From inside the cloned directory:

```sh
./scripts/build.sh
```

After that, `avalanche-network-runner` binary should be present under the `./bin/` directory, inside the cloned one.

The user may consider to permanently add this directory to the `$PATH` environment variable by including a line in the shell
initialization script (eg `%HOME/.bashrc` for `bash` shell), replacing `CLONED_DIRECTORY` with the real one:

```sh
export PATH=$PATH:CLONED_DIRECTORY/bin
```

### Run Unit Tests

Inside the directory cloned above:

```sh
go test ./...
```

### Run E2E tests

The E2E test checks `avalanche-network-runner` RPC communication and control. It starts a network against a fresh RPC
server and executes a set of query and control operations on it.

To start it, execute inside the cloned directory:

```sh
./scripts/tests.e2e.sh
```

## Using `avalanche-network-runner`

You can import this repository as a library in your Go program, but we recommend running `avalanche-network-runner` as a binary. This creates an RPC server that you can send requests to in order to start a network, add nodes to the network, remove nodes from the network, restart nodes, etc.. You can make requests through the `avalanche-network-runner` command or by making API calls. Requests are "translated" into gRPC and sent to the server.

**Why does `avalanche-network-runner` need an RPC server?** `avalanche-network-runner` needs to provide complex workflows such as replacing nodes, restarting nodes, injecting fail points, etc.. The RPC server exposes basic operations to enable a separation of concerns such that one team develops a test framework, and the other writes test cases and controlling logic.

**Why gRPC?** The RPC server leads to more modular test components, and gRPC enables greater flexibility. The protocol buffer increases flexibility as we develop more complicated test cases. And gRPC opens up a variety of different approaches for how to write test controller (e.g., Rust). See [`rpcpb/rpc.proto`](./rpcpb/rpc.proto) for service definition.

**Why gRPC gateway?** [gRPC gateway](https://grpc-ecosystem.github.io/grpc-gateway/) exposes gRPC API via HTTP, without us writing any code. Which can be useful if a test controller writer does not want to deal with gRPC.

## `network-runner` RPC server: examples

To start the server:

```bash
avalanche-network-runner server \
--log-level debug \
--port=":8080" \
--grpc-gateway-port=":8081"

# set "--disable-grpc-gateway" to disable gRPC gateway
```

Note that the above command will run until you stop it with `CTRL + C`. You should run further commands in a separate terminal.

To ping the server:

```bash
curl -X POST -k http://localhost:8081/v1/ping -d ''

# or
avalanche-network-runner ping \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

To start a new Avalanche network with five nodes (a cluster):

```bash
# replace execPath with the path to AvalancheGo on your machine
# e.g., ${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego
AVALANCHEGO_EXEC_PATH="avalanchego"

curl -X POST -k http://localhost:8081/v1/control/start -d '{"execPath":"'${AVALANCHEGO_EXEC_PATH}'","numNodes":5,"logLevel":"INFO"}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--number-of-nodes=5 \
--avalanchego-path ${AVALANCHEGO_EXEC_PATH}
```

Additional optional parameters which can be passed to the start command:

```bash
--plugin-dir ${AVALANCHEGO_PLUGIN_PATH} \
--blockchain-specs '[{"vm_name": "subnetevm", "genesis": "/tmp/subnet-evm.genesis.json"}]'
--global-node-config '{"index-enabled":false, "api-admin-enabled":true,"network-peer-list-gossip-frequency":"300ms"}'
--custom-node-configs" '{"node1":{"log-level":"debug","api-admin-enabled":false},"node2":{...},...}'
```

For example, to set `avalanchego --http-host` flag for all nodes:

```bash
# to expose local RPC server to all traffic
# (e.g., run network runner within cloud instance)
curl -X POST -k http://localhost:8081/v1/control/start -d '{"execPath":"'${AVALANCHEGO_EXEC_PATH}'","globalNodeConfig":"{\"http-host\":\"0.0.0.0\"}"}'

# or
avalanche-network-runner control start \
--avalanchego-path ${AVALANCHEGO_EXEC_PATH} \
--global-node-config '{"http-host":"0.0.0.0"}'
```

`--plugin-dir` and `--blockchain-specs` are parameters relevant to subnet operation.
See the [subnet](#network-runner-rpc-server-subnet-evm-example) section for details about how to run subnets.

The network-runner supports avalanchego node configuration at different levels.

1. If neither `--global-node-config` nor `--custom-node-configs` is supplied, all nodes get a standard set of config options. Currently this set contains:

    ```json
        {
        "network-peer-list-gossip-frequency":"250ms",
        "network-max-reconnect-delay":"1s",
        "public-ip":"127.0.0.1",
        "health-check-frequency":"2s",
        "api-admin-enabled":true,
        "api-ipcs-enabled":true,
        "index-enabled":true
        }
    ```

2. `--global-node-config` is a JSON string representing a _single_ avalanchego config, which will be applied to **all nodes**. This makes it easy to define common properties to all nodes. Whatever is set here will be _combined_ with the standard set above.
3. `--custom-node-configs` is a map of JSON strings representing the _complete_ network with individual configs. This allows to configure each node independently. If set, `--number-of-nodes` will be **ignored** to avoid conflicts.
4. The configs can be combined and will be merged, i.e. one could set global `--global-node-config` entries applied to each node, and also set `--custom-node-configs` for additional entries.
5. Common `--custom-node-configs` entries override `--global-node-config` entries which override the standard set.

Example usage of `--custom-node-configs` to get deterministic API port numbers:

```bash
curl -X POST -k http://localhost:8081/v1/control/start -d\
'{"execPath":"'${AVALANCHEGO_EXEC_PATH}'","customNodeConfigs":
{
"node1":"{\"http-port\":9650}",
"node2":"{\"http-port\":9652}",
"node3":"{\"http-port\":9654}",
"node4":"{\"http-port\":9656}",
"node5":"{\"http-port\":9658}"
}
}'

# or
avalanche-network-runner control start \
--avalanchego-path ${AVALANCHEGO_EXEC_PATH} \
--custom-node-configs \
'{
"node1":"{\"http-port\":9650}",
"node2":"{\"http-port\":9652}",
"node3":"{\"http-port\":9654}",
"node4":"{\"http-port\":9656}",
"node5":"{\"http-port\":9658}"
}'
```

**NAMING CONVENTION**: Currently, node names should be called `node` + a number, i.e. `node1,node2,node3,...node 101`

To wait for all the nodes in the cluster to become healthy:

```bash
curl -X POST -k http://localhost:8081/v1/control/health -d ''

# or
avalanche-network-runner control health \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

To get the API endpoints of all nodes in the cluster:

```bash
curl -X POST -k http://localhost:8081/v1/control/uris -d ''

# or
avalanche-network-runner control uris \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

To query the cluster status from the server:

```bash
curl -X POST -k http://localhost:8081/v1/control/status -d ''

# or
avalanche-network-runner control status \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

To stream cluster status:

```bash
avalanche-network-runner control \
--request-timeout=3m \
stream-status \
--push-interval=5s \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

To save the network to a snapshot:

```bash
curl -X POST -k http://localhost:8081/v1/control/savesnapshot -d '{"snapshot_name":"node5"}'

# or
avalanche-network-runner control save-snapshot snapshotName
```

To load a network from a snapshot:

```bash
curl -X POST -k http://localhost:8081/v1/control/loadsnapshot -d '{"snapshot_name":"node5"}'

# or
avalanche-network-runner control load-snapshot snapshotName
```

An avalanchego binary path and/or plugin dir can be specified when loading the snapshot. This is
optional. If not specified, will use the paths saved with the snapshot:

```bash
curl -X POST -k http://localhost:8081/v1/control/loadsnapshot -d '{"snapshot_name":"node5","execPath":"'${AVALANCHEGO_EXEC_PATH}'","pluginDir":"'${AVALANCHEGO_PLUGIN_PATH}'"}'

# or
avalanche-network-runner control load-snapshot snapshotName --avalanchego-path ${AVALANCHEGO_EXEC_PATH} --plugin-dir ${AVALANCHEGO_PLUGIN_PATH}
```

To get the list of snapshots:

```bash
curl -X POST -k http://localhost:8081/v1/control/getsnapshotnames

# or
avalanche-network-runner control get-snapshot-names
```

To remove a snapshot:

```bash
curl -X POST -k http://localhost:8081/v1/control/removesnapshot -d '{"snapshot_name":"node5"}'

# or
avalanche-network-runner control remove-snapshot snapshotName
```

To create N validated subnets (requires network restart):

```bash
curl -X POST -k http://localhost:8081/v1/control/createsubnets -d '{"num_subnets":5}'

# or
avalanche-network-runner control create-subnets 5
```

To create a blockchain without a subnet id (requires network restart):

```bash
curl -X POST -k http://localhost:8081/v1/control/createblockchains -d '{"pluginDir":"'$PLUGIN_DIR'","blockchainSpecs":[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'"}]}'

# or
avalanche-network-runner control create-blockchains '[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'"}]' --plugin-dir $PLUGIN_DIR
```

To create a blockchain with a subnet id (does not require restart):

```bash
curl -X POST -k http://localhost:8081/v1/control/createblockchains -d '{"pluginDir":"'$PLUGIN_DIR'","blockchainSpecs":[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnet_id": "'$SUBNET_ID'"}]}'

# or
avalanche-network-runner control create-blockchains '[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnet_id": "'$SUBNET_ID'"}]' --plugin-dir $PLUGIN_DIR
```

To create a blockchain with a subnet id, and chain config, network upgrade and subnet config file paths (requires network restart):

```bash
curl -X POST -k http://localhost:8081/v1/control/createblockchains -d '{"pluginDir":"'$PLUGIN_DIR'","blockchainSpecs":[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnet_id": "'$SUBNET_ID'", "chain_config": "'$CHAIN_CONFIG_PATH'", "network_upgrade": "'$NETWORK_UPGRADE_PATH'", "subnet_config": "'$SUBNET_CONFIG_PATH'"}]}'

# or
avalanche-network-runner control create-blockchains '[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnet_id": "'$SUBNET_ID'", "chain_config": "'$CHAIN_CONFIG_PATH'", "network_upgrade": "'$NETWORK_UPGRADE_PATH'", "subnet_config": "'$SUBNET_CONFIG_PATH'"}]' --plugin-dir $PLUGIN_DIR
```

To remove (stop) a node:

```bash
curl -X POST -k http://localhost:8081/v1/control/removenode -d '{"name":"node5"}'

# or
avalanche-network-runner control remove-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node5
```

To restart a node (in this case, the one named `node1`):

```bash
# e.g., ${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego
AVALANCHEGO_EXEC_PATH="avalanchego"

# Note that you can restart the node with a different binary by providing
# a different execPath
curl -X POST -k http://localhost:8081/v1/control/restartnode -d '{"name":"node1","execPath":"'${AVALANCHEGO_EXEC_PATH}'","logLevel":"INFO"}'

# or
avalanche-network-runner control restart-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node1 \
--avalanchego-path ${AVALANCHEGO_EXEC_PATH}
```

To add a node (in this case, a new node named `node99`):

```bash
# e.g., ${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego
AVALANCHEGO_EXEC_PATH="avalanchego"

# Note that you can add the new node with a different binary by providing
# a different execPath
curl -X POST -k http://localhost:8081/v1/control/addnode -d '{"name":"node99","execPath":"'${AVALANCHEGO_EXEC_PATH}'","logLevel":"INFO"}'

# or
avalanche-network-runner control add-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node99 \
--avalanchego-path ${AVALANCHEGO_EXEC_PATH}
```

You can also provide additional flags that specify the node's config:

```sh
  --node-config '{"index-enabled":false, "api-admin-enabled":true,"network-peer-list-gossip-frequency":"300ms"}'
```

`--node-config` allows to specify specific avalanchego config parameters to the new node. See [here](https://docs.avax.network/build/references/avalanchego-config-flags) for the reference of supported flags.

**Note**: The following parameters will be _ignored_ if set in `--node-config`, because the network runner needs to set its own in order to function properly:
`--log-dir`
`--db-dir`

AvalancheGo exposes a "test peer", which you can attach to a node.
(See [here](https://github.com/ava-labs/avalanchego/blob/master/network/peer/test_peer.go) for more information.)
You can send messages through the test peer to the node it is attached to.

To attach a test peer to a node (in this case, `node1`):

```bash
curl -X POST -k http://localhost:8081/v1/control/attachpeer -d '{"nodeName":"node1"}'

# or
avalanche-network-runner control attach-peer \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node1
```

To send a chit message to the node through the test peer:

```bash
curl -X POST -k http://localhost:8081/v1/control/sendoutboundmessage -d '{"nodeName":"node1","peerId":"7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg","op":16,"bytes":"EAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAKgAAAAPpAqmoZkC/2xzQ42wMyYK4Pldl+tX2u+ar3M57WufXx0oXcgXfXCmSnQbbnZQfg9XqmF3jAgFemSUtFkaaZhDbX6Ke1DVpA9rCNkcTxg9X2EcsfdpKXgjYioitjqca7WA="}'

# or
avalanche-network-runner control send-outbound-message \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node1 \
--peer-id "7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg" \
--message-op=16 \
--message-bytes-b64="EAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAKgAAAAPpAqmoZkC/2xzQ42wMyYK4Pldl+tX2u+ar3M57WufXx0oXcgXfXCmSnQbbnZQfg9XqmF3jAgFemSUtFkaaZhDbX6Ke1DVpA9rCNkcTxg9X2EcsfdpKXgjYioitjqca7WA="
```

To terminate the cluster:

```bash
curl -X POST -k http://localhost:8081/v1/control/stop -d ''

# or
avalanche-network-runner control stop \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

## `network-runner` RPC server: `subnet-evm` example

To start the server:

```bash
avalanche-network-runner server \
--log-level debug \
--port=":8080" \
--grpc-gateway-port=":8081"

# make sure network-runner server is up
curl -X POST -k http://localhost:8081/v1/ping -d ''
```

To start the cluster with custom chains:

```bash
# or download from https://github.com/ava-labs/subnet-cli/releases
cd ${HOME}/go/src/github.com/ava-labs/subnet-cli
go install -v .
subnet-cli create VMID subnetevm
# srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy

# download from https://github.com/ava-labs/avalanchego/releases
# or build
rm -rf ${HOME}/go/src/github.com/ava-labs/avalanchego/build
cd ${HOME}/go/src/github.com/ava-labs/avalanchego
./scripts/build.sh

# ref. https://github.com/ava-labs/subnet-evm/blob/b69e47e0398b5237cda0422f6a32969e64bde346/scripts/run.sh
cd ${HOME}/go/src/github.com/ava-labs/subnet-evm
go build -v \
-o ${HOME}/go/src/github.com/ava-labs/avalanchego/build/plugins/srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy \
./plugin

# make sure binaries are built
find ${HOME}/go/src/github.com/ava-labs/avalanchego/build
# for example
# .../build
# .../build/plugins
# .../build/plugins/srEXiWaHuhNyGwPUi444Tu47ZEDwxTWrbQiuD7FmgSAQ6X7Dy
# .../build/plugins/evm
# .../build/avalanchego

# generate the genesis for the custom chain
export CHAIN_ID=99999
export GENESIS_ADDRESS="0x8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC"
cat <<EOF > /tmp/subnet-evm.genesis.json
{
  "config": {
    "chainId": $CHAIN_ID,
    "homesteadBlock": 0,
    "eip150Block": 0,
    "eip150Hash": "0x2086799aeebeae135c246c65021c82b4e15a2c451340993aacfd2751886514f0",
    "eip155Block": 0,
    "eip158Block": 0,
    "byzantiumBlock": 0,
    "constantinopleBlock": 0,
    "petersburgBlock": 0,
    "istanbulBlock": 0,
    "muirGlacierBlock": 0,
    "subnetEVMTimestamp": 0,
    "feeConfig": {
      "gasLimit": 20000000,
      "minBaseFee": 1000000000,
      "targetGas": 100000000,
      "baseFeeChangeDenominator": 48,
      "minBlockGasCost": 0,
      "maxBlockGasCost": 10000000,
      "targetBlockRate": 2,
      "blockGasCostStep": 500000
    }
  },
  "alloc": {
    "${GENESIS_ADDRESS}": {
      "balance": "0x52B7D2DCC80CD2E4000000"
    }
  },
  "nonce": "0x0",
  "timestamp": "0x0",
  "extraData": "0x00",
  "gasLimit": "0x1312D00",
  "difficulty": "0x0",
  "mixHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
  "coinbase": "0x0000000000000000000000000000000000000000",
  "number": "0x0",
  "gasUsed": "0x0",
  "parentHash": "0x0000000000000000000000000000000000000000000000000000000000000000"
}
EOF
cat /tmp/subnet-evm.genesis.json
```

```bash
# replace execPath with the path to AvalancheGo on your machine
AVALANCHEGO_EXEC_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego"
AVALANCHEGO_PLUGIN_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/plugins"

curl -X POST -k http://localhost:8081/v1/control/start -d '{"execPath":"'${AVALANCHEGO_EXEC_PATH}'","numNodes":5,"logLevel":"INFO","pluginDir":"'${AVALANCHEGO_PLUGIN_PATH}'","blockchainSpecs":[{"vm_name":"subnetevm","genesis":"/tmp/subnet-evm.genesis.json"}]}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--avalanchego-path ${AVALANCHEGO_EXEC_PATH} \
--plugin-dir ${AVALANCHEGO_PLUGIN_PATH} \
--blockchain-specs '[{"vm_name": "subnetevm", "genesis": "/tmp/subnet-evm.genesis.json"}]'
```

```bash
# to get cluster information including blockchain ID
curl -X POST -k http://localhost:8081/v1/control/status -d ''
```

Blockchain config file, network upgrade file, and subnet config file paths can be optionally specified at network start, eg:

```bash
curl -X POST -k http://localhost:8081/v1/control/start -d '{"execPath":"'${AVALANCHEGO_EXEC_PATH}'","numNodes":5,"logLevel":"INFO","pluginDir":"'${AVALANCHEGO_PLUGIN_PATH}'","blockchainSpecs":[{"vm_name":"subnetevm","genesis":"/tmp/subnet-evm.genesis.json","chain_config":"'$CHAIN_CONFIG_PATH'","network_upgrade":"'$NETWORK_UPGRADE_PATH'","subnet_config":"'$SUBNET_CONFIG_PATH'"}]}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--avalanchego-path ${AVALANCHEGO_EXEC_PATH} \
--plugin-dir ${AVALANCHEGO_PLUGIN_PATH} \
--blockchain-specs '[{"vm_name": "subnetevm", "genesis": "/tmp/subnet-evm.genesis.json", "chain_config": "'$CHAIN_CONFIG_PATH'", "network_upgrade": "'$NETWORK_UPGRADE_PATH'", "subnet_config": "'$SUBNET_CONFIG_PATH'"}]'
```

## `network-runner` RPC server: `blobvm` example

To start the server:

```bash
avalanche-network-runner server \
--log-level debug \
--port=":8080" \
--grpc-gateway-port=":8081"

# make sure network-runner server is up
curl -X POST -k http://localhost:8081/v1/ping -d ''
```

To start the cluster with custom chains:

```bash
# or download from https://github.com/ava-labs/subnet-cli/releases
cd ${HOME}/go/src/github.com/ava-labs/subnet-cli
go install -v .
subnet-cli create VMID blobvm
# kM6h4LYe3AcEU1MB2UNg6ubzAiDAALZzpVrbX8zn3hXF6Avd8

# download from https://github.com/ava-labs/avalanchego/releases
# or build
rm -rf ${HOME}/go/src/github.com/ava-labs/avalanchego/build
cd ${HOME}/go/src/github.com/ava-labs/avalanchego
./scripts/build.sh

cd ${HOME}/go/src/github.com/ava-labs/blobvm
go build -v \
-o ${HOME}/go/src/github.com/ava-labs/avalanchego/build/plugins/kM6h4LYe3AcEU1MB2UNg6ubzAiDAALZzpVrbX8zn3hXF6Avd8 \
./cmd/blobvm

# make sure binaries are built
find ${HOME}/go/src/github.com/ava-labs/avalanchego/build
# for example
# .../build
# .../build/plugins
# .../build/plugins/kM6h4LYe3AcEU1MB2UNg6ubzAiDAALZzpVrbX8zn3hXF6Avd8
# .../build/plugins/evm
# .../build/avalanchego

# generate the genesis for the custom chain
cd ${HOME}/go/src/github.com/ava-labs/blobvm
go install -v ./cmd/blob-cli
echo "[]" > /tmp/alloc.json
blob-cli genesis 1 /tmp/alloc.json --genesis-file /tmp/blobvm.genesis.json
cat /tmp/blobvm.genesis.json
```

```bash
# replace execPath with the path to AvalancheGo on your machine
AVALANCHEGO_EXEC_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego"
AVALANCHEGO_PLUGIN_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/plugins"

curl -X POST -k http://localhost:8081/v1/control/start -d '{"execPath":"'${AVALANCHEGO_EXEC_PATH}'","numNodes":5,"logLevel":"INFO","pluginDir":"'${AVALANCHEGO_PLUGIN_PATH}'","blockchainSpecs":[{"vm_name":"blobvm","genesis":"/tmp/blobvm.genesis.json"}]}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--avalanchego-path ${AVALANCHEGO_EXEC_PATH} \
--plugin-dir ${AVALANCHEGO_PLUGIN_PATH} \
--blockchain-specs '[{"vm_name": "blobvm", "genesis": "/tmp/blobvm.genesis.json"}]'
```

```bash
# to get cluster information including blockchain ID
curl -X POST -k http://localhost:8081/v1/control/status -d ''
```

Blockchain config file and network upgrade file paths can be optionally specified at network start, eg:

```bash
curl -X POST -k http://localhost:8081/v1/control/start -d '{"execPath":"'${AVALANCHEGO_EXEC_PATH}'","numNodes":5,"logLevel":"INFO","pluginDir":"'${AVALANCHEGO_PLUGIN_PATH}'","blockchainSpecs":[{"vm_name":"blobvm","genesis":"/tmp/blobvm.json","chain_config":"'$CHAIN_CONFIG_PATH'","network_upgrade":"'$NETWORK_UPGRADE_PATH'","subnet_config":"'$SUBNET_CONFIG_PATH'"}]}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--avalanchego-path ${AVALANCHEGO_EXEC_PATH} \
--plugin-dir ${AVALANCHEGO_PLUGIN_PATH} \
--blockchain-specs '[{"vm_name": "blobvm", "genesis": "/tmp/blobvm.genesis.json", "chain_config": "'$CHAIN_CONFIG_PATH'", "network_upgrade": "'$NETWORK_UPGRADE_PATH'", "subnet_config": "'$SUBNET_CONFIG_PATH'"}]'
```

## `network-runner` RPC server: `timestampvm` example

To start the server:

```bash
avalanche-network-runner server \
--log-level debug \
--port=":8080" \
--grpc-gateway-port=":8081"

# make sure network-runner server is up
curl -X POST -k http://localhost:8081/v1/ping -d ''
```

To start the cluster with custom chains:

```bash
# or download from https://github.com/ava-labs/subnet-cli/releases
cd ${HOME}/go/src/github.com/ava-labs/subnet-cli
go install -v .
subnet-cli create VMID timestampvm
# tGas3T58KzdjcJ2iKSyiYsWiqYctRXaPTqBCA11BqEkNg8kPc

# download from https://github.com/ava-labs/avalanchego/releases
# or build
rm -rf ${HOME}/go/src/github.com/ava-labs/avalanchego/build
cd ${HOME}/go/src/github.com/ava-labs/avalanchego
./scripts/build.sh

# or download from https://github.com/ava-labs/timestampvm/releases
# cd ${HOME}/go/src/github.com/ava-labs/timestampvm
# ./scripts/build.sh
cd ${HOME}/go/src/github.com/ava-labs/timestampvm
go build -v \
-o ${HOME}/go/src/github.com/ava-labs/avalanchego/build/plugins/tGas3T58KzdjcJ2iKSyiYsWiqYctRXaPTqBCA11BqEkNg8kPc \
./main

# make sure binaries are built
find ${HOME}/go/src/github.com/ava-labs/avalanchego/build
# for example
# .../build
# .../build/plugins
# .../build/plugins/tGas3T58KzdjcJ2iKSyiYsWiqYctRXaPTqBCA11BqEkNg8kPc
# .../build/plugins/evm
# .../build/avalanchego

# generate the genesis for the custom chain
# NOTE: timestampvm takes arbitrary data for its genesis
echo hello > /tmp/timestampvm.genesis.json
```

```bash
# replace execPath with the path to AvalancheGo on your machine
AVALANCHEGO_EXEC_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego"
AVALANCHEGO_PLUGIN_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/plugins"

curl -X POST -k http://localhost:8081/v1/control/start -d '{"execPath":"'${AVALANCHEGO_EXEC_PATH}'","numNodes":5,"logLevel":"INFO","pluginDir":"'${AVALANCHEGO_PLUGIN_PATH}'","blockchainSpecs":[{"vmName":"timestampvm","genesis":"/tmp/timestampvm.genesis.json"}]}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--avalanchego-path ${AVALANCHEGO_EXEC_PATH} \
--plugin-dir ${AVALANCHEGO_PLUGIN_PATH} \
--blockchain-specs '[{"vm_name":"timestampvm","genesis":"/tmp/timestampvm.genesis.json"}]'
```

```bash
# to get cluster information including blockchain ID
curl -X POST -k http://localhost:8081/v1/control/status -d ''
```

To call `timestampvm` APIs:

```bash
# in this example,
# "tGas3T58KzdjcJ2iKSyiYsWiqYctRXaPTqBCA11BqEkNg8kPc" is the Vm Id for the static service
curl -X POST --data '{
    "jsonrpc": "2.0",
    "id"     : 1,
    "method" : "timestampvm.encode",
    "params" : {
        "data": "mynewblock",
        "length": 32
    }
}' -H 'content-type:application/json;' 127.0.0.1:9650/ext/vm/tGas3T58KzdjcJ2iKSyiYsWiqYctRXaPTqBCA11BqEkNg8kPc

# in this example,
# "E8isHenre76NMxbJ3munSQatV8GoQ4XKWQg9vD34xMBqEFJGf" is the blockchain Id
curl -X POST --data '{
    "jsonrpc": "2.0",
    "method": "timestampvm.proposeBlock",
    "params":{
        "data":"0x6d796e6577626c6f636b0000000000000000000000000000000000000000000014228326"
    },
    "id": 1
}' -H 'content-type:application/json;' 127.0.0.1:9650/ext/bc/E8isHenre76NMxbJ3munSQatV8GoQ4XKWQg9vD34xMBqEFJGf
```

```bash
curl -X POST --data '{
    "jsonrpc": "2.0",
    "method": "timestampvm.getBlock",
    "params":{},
    "id": 1
}' -H 'content-type:application/json;' 127.0.0.1:9650/ext/bc/E8isHenre76NMxbJ3munSQatV8GoQ4XKWQg9vD34xMBqEFJGf
```

## Configuration

When the user creates a network, they specify the configurations of the nodes that are in the network upon creation.

A node config is defined by this struct:

```go
type Config struct {
  // A node's name must be unique from all other nodes
  // in a network. If Name is the empty string, a
  // unique name is assigned on node creation.
  Name string `json:"name"`
  // True if other nodes should use this node
  // as a bootstrap beacon.
  IsBeacon bool `json:"isBeacon"`
  // Must not be nil.
  StakingKey string `json:"stakingKey"`
  // Must not be nil.
  StakingCert string `json:"stakingCert"`
  // May be nil.
  ConfigFile string `json:"configFile"`
  // May be nil.
  ChainConfigFiles map[string]string `json:"chainConfigFiles"`
  // May be nil.
  UpgradeConfigFiles map[string]string `json:"upgradeConfigFiles"`
  // May be nil.
  SubnetConfigFiles map[string]string `json:"subnetConfigFiles"`
  // Flags can hold additional flags for the node.
  // It can be empty.
  // The precedence of flags handling is:
  // 1. Flags defined in node.Config (this struct) override
  // 2. Flags defined in network.Config override
  // 3. Flags defined in the json config file
  Flags map[string]interface{} `json:"flags"`
  // What type of node this is
  BinaryPath string `json:"binaryPath"`
  // If non-nil, direct this node's Stdout to os.Stdout
  RedirectStdout bool `json:"redirectStdout"`
  // If non-nil, direct this node's Stderr to os.Stderr
  RedirectStderr bool `json:"redirectStderr"`
}
```

As you can see, some fields of the config must be set, while others will be auto-generated if not provided. Bootstrap IPs/ IDs will be overwritten even if provided.

## Genesis Generation

You can create a custom AvalancheGo genesis with function `network.NewAvalancheGoGenesis`:

```go
// Return a genesis JSON where:
// The nodes in [genesisVdrs] are validators.
// The C-Chain and X-Chain balances are given by
// [cChainBalances] and [xChainBalances].
// Note that many of the genesis fields (i.e. reward addresses)
// are randomly generated or hard-coded.
func NewAvalancheGoGenesis(
  log logging.Logger,
  networkID uint32,
  xChainBalances []AddrAndBalance,
  cChainBalances []AddrAndBalance,
  genesisVdrs []ids.ShortID,
) ([]byte, error)
```

Later on the genesis contents can be used in network creation.

## Network Creation

Th function `NewNetwork` returns a new network, parameterized on `network.Config`:

```go
type Config struct {
  // Must not be empty
  Genesis string `json:"genesis"`
  // May have length 0
  // (i.e. network may have no nodes on creation.)
  NodeConfigs []node.Config `json:"nodeConfigs"`
  // Flags that will be passed to each node in this network.
  // It can be empty.
  // Config flags may also be passed in a node's config struct
  // or config file.
  // The precedence of flags handling is, from highest to lowest:
  // 1. Flags defined in a node's node.Config
  // 2. Flags defined in a network's network.Config
  // 3. Flags defined in a node's config file
  // For example, if a network.Config has flag W set to X,
  // and a node within that network has flag W set to Y,
  // and the node's config file has flag W set to Z,
  // then the node will be started with flag W set to Y.
  Flags map[string]interface{} `json:"flags"`
}
```

The function that returns a new network may have additional configuration fields.

## Default Network Creation

The helper function `NewDefaultNetwork` returns a network using a pre-defined configuration. This allows users to create a new network without needing to define any configurations.

```go
// NewDefaultNetwork returns a new network using a pre-defined
// network configuration.
// The following addresses are pre-funded:
// X-Chain Address 1:     X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p
// X-Chain Address 1 Key: PrivateKey-ewoqjP7PxY4yr3iLTpLisriqt94hdyDFNgchSxGGztUrTXtNN
// X-Chain Address 2:     X-custom16045mxr3s2cjycqe2xfluk304xv3ezhkhsvkpr
// X-Chain Address 2 Key: PrivateKey-2fzYBh3bbWemKxQmMfX6DSuL2BFmDSLQWTvma57xwjQjtf8gFq
// P-Chain Address 1:     P-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p
// P-Chain Address 1 Key: PrivateKey-ewoqjP7PxY4yr3iLTpLisriqt94hdyDFNgchSxGGztUrTXtNN
// P-Chain Address 2:     P-custom16045mxr3s2cjycqe2xfluk304xv3ezhkhsvkpr
// P-Chain Address 2 Key: PrivateKey-2fzYBh3bbWemKxQmMfX6DSuL2BFmDSLQWTvma57xwjQjtf8gFq
// C-Chain Address:       0x8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC
// C-Chain Address Key:   56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027
// The following nodes are validators:
// * NodeID-7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg
// * NodeID-MFrZFVCXPv5iCn6M9K6XduxGTYp891xXZ
// * NodeID-NFBbbJ4qCmNaCzeW7sxErhvWqvEQMnYcN
// * NodeID-GWPcbFJZFfZreETSoWjPimr846mXEKCtu
// * NodeID-P7oB2McjBGgW2NXXWVYjV8JEDFoW9xDE5
func NewDefaultNetwork(
  log logging.Logger,
  binaryPath string,
  reassignPortsIfUsed,
) (network.Network, error)
```

The associated pre-defined configuration is also available to users by calling `NewDefaultConfig` function.

## Network Snapshots

A given network state, including the node ports and the full blockchain state, can be saved to a named snapshot. The network can then be restarted from such a snapshot any time later.

```go
// Save network snapshot
// Network is stopped in order to do a safe persistence
// Returns the full local path to the snapshot dir
SaveSnapshot(context.Context, string) (string, error)
// Remove network snapshot
RemoveSnapshot(string) error
// Get names of all available snapshots
GetSnapshotNames() ([]string, error)
```

To create a new network from a snapshot, the function `NewNetworkFromSnapshot` is provided.

## Network Interaction

The network runner allows users to interact with an AvalancheGo network using the `network.Network` interface:

```go
// Network is an abstraction of an Avalanche network
type Network interface {
  // Returns nil if all the nodes in the network are healthy.
  // A stopped network is considered unhealthy.
  // Timeout is given by the context parameter.
  Healthy(context.Context) error
  // Stop all the nodes.
  // Returns ErrStopped if Stop() was previously called.
  Stop(context.Context) error
  // Start a new node with the given config.
  // Returns ErrStopped if Stop() was previously called.
  AddNode(node.Config) (node.Node, error)
  // Stop the node with this name.
  // Returns ErrStopped if Stop() was previously called.
  RemoveNode(name string) error
  // Return the node with this name.
  // Returns ErrStopped if Stop() was previously called.
  GetNode(name string) (node.Node, error)
  // Return all the nodes in this network.
  // Node name --> Node.
  // Returns ErrStopped if Stop() was previously called.
  GetAllNodes() (map[string]node.Node, error)
  // Returns the names of all nodes in this network.
  // Returns ErrStopped if Stop() was previously called.
  GetNodeNames() ([]string, error)
  // Save network snapshot
  // Network is stopped in order to do a safe preservation
  // Returns the full local path to the snapshot dir
  SaveSnapshot(context.Context, string) (string, error)
  // Remove network snapshot
  RemoveSnapshot(string) error
  // Get name of available snapshots
  GetSnapshotNames() ([]string, error)
}
```

and allows users to interact with a node using the `node.Node` interface:

```go
// Node represents an AvalancheGo node
type Node interface {
  // Return this node's name, which is unique
  // across all the nodes in its network.
  GetName() string
  // Return this node's Avalanche node ID.
  GetNodeID() ids.ShortID
  // Return a client that can be used to make API calls.
  GetAPIClient() api.Client
  // Return this node's IP (e.g. 127.0.0.1).
  GetURL() string
  // Return this node's P2P (staking) port.
  GetP2PPort() uint16
  // Return this node's HTTP API port.
  GetAPIPort() uint16
  // Starts a new test peer, connects it to the given node, and returns the peer.
  // [handler] defines how the test peer handles messages it receives.
  // The test peer can be used to send messages to the node it's attached to.
  // It's left to the caller to maintain a reference to the returned peer.
  // The caller should call StartClose() on the peer when they're done with it.
  AttachPeer(ctx context.Context, handler router.InboundHandler) (peer.Peer, error)
  // Return this node's avalanchego binary path
  GetBinaryPath() string
  // Return this node's db dir
  GetDbDir() string
  // Return this node's logs dir
  GetLogsDir() string
  // Return this node's config file contents
  GetConfigFile() string
}
```
