# Blockchain examples

## `subnet-evm` example

To start the server:

```sh
export AVALANCHEGO_EXEC_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego"
export AVALANCHEGO_PLUGIN_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/plugins"

avalanche-network-runner server \
--log-level debug \
--port=":8080" \
--grpc-gateway-port=":8081"

# make sure network-runner server is up
curl -X POST -k http://localhost:8081/v1/ping
```

To start the network with custom chains:

```sh
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

```sh
curl -X POST -k http://localhost:8081/v1/control/start -d '{"numNodes":5,"logLevel":"INFO","blockchainSpecs":[{"vm_name":"subnetevm","genesis":"/tmp/subnet-evm.genesis.json"}]}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--blockchain-specs '[{"vm_name": "subnetevm", "genesis": "/tmp/subnet-evm.genesis.json"}]'
```

```sh
# to get network information including blockchain ID
curl -X POST -k http://localhost:8081/v1/control/status
```

Blockchain config file, network upgrade file, and subnet config file paths can be optionally specified at network start, eg:

```sh
curl -X POST -k http://localhost:8081/v1/control/start -d '{"numNodes":5,"logLevel":"INFO","blockchainSpecs":[{"vm_name":"subnetevm","genesis":"/tmp/subnet-evm.genesis.json","chainConfig":"'$CHAIN_CONFIG_PATH'","networkUpgrade":"'$NETWORK_UPGRADE_PATH'","subnetConfig":"'$SUBNET_CONFIG_PATH'"}]}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--blockchain-specs '[{"vm_name": "subnetevm", "genesis": "/tmp/subnet-evm.genesis.json", "chain_config": "'$CHAIN_CONFIG_PATH'", "network_upgrade": "'$NETWORK_UPGRADE_PATH'", "subnet_config": "'$SUBNET_CONFIG_PATH'"}]'
```

## `blobvm` example

To start the server:

```sh
export AVALANCHEGO_EXEC_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego"
export AVALANCHEGO_PLUGIN_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/plugins"

avalanche-network-runner server \
--log-level debug \
--port=":8080" \
--grpc-gateway-port=":8081"

# make sure network-runner server is up
curl -X POST -k http://localhost:8081/v1/ping
```

To start the network with custom chains:

```sh
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

```sh
curl -X POST -k http://localhost:8081/v1/control/start -d '{"numNodes":5,"logLevel":"INFO","blockchainSpecs":[{"vm_name":"blobvm","genesis":"/tmp/blobvm.genesis.json"}]}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--blockchain-specs '[{"vm_name": "blobvm", "genesis": "/tmp/blobvm.genesis.json"}]'
```

```sh
# to get network information including blockchain ID
curl -X POST -k http://localhost:8081/v1/control/status
```

Blockchain config file and network upgrade file paths can be optionally specified at network start, eg:

```sh
curl -X POST -k http://localhost:8081/v1/control/start -d '{"numNodes":5,"logLevel":"INFO","blockchainSpecs":[{"vm_name":"blobvm","genesis":"/tmp/blobvm.json","chainConfig":"'$CHAIN_CONFIG_PATH'","networkUpgrade":"'$NETWORK_UPGRADE_PATH'","subnetConfig":"'$SUBNET_CONFIG_PATH'"}]}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--blockchain-specs '[{"vm_name": "blobvm", "genesis": "/tmp/blobvm.genesis.json", "chain_config": "'$CHAIN_CONFIG_PATH'", "network_upgrade": "'$NETWORK_UPGRADE_PATH'", "subnet_config": "'$SUBNET_CONFIG_PATH'"}]'
```

## `timestampvm` example

To start the server:

```sh
export AVALANCHEGO_EXEC_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego"
export AVALANCHEGO_PLUGIN_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/plugins"

avalanche-network-runner server \
--log-level debug \
--port=":8080" \
--grpc-gateway-port=":8081"

# make sure network-runner server is up
curl -X POST -k http://localhost:8081/v1/ping
```

To start the network with custom chains:

```sh
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

```sh
curl -X POST -k http://localhost:8081/v1/control/start -d '{"numNodes":5,"logLevel":"INFO","blockchainSpecs":[{"vmName":"timestampvm","genesis":"/tmp/timestampvm.genesis.json","blockchainAlias":"timestamp"}]}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--blockchain-specs '[{"vm_name":"timestampvm","genesis":"/tmp/timestampvm.genesis.json","blockchain_alias":"timestamp"}]'
```

```sh
# to get network information including blockchain ID
curl -X POST -k http://localhost:8081/v1/control/status
```

To call `timestampvm` APIs:

```sh
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
# "timestamp" is the blockchain alias
# You can use 127.0.0.1:9650/ext/bc/timestamp or 127.0.0.1:9650/ext/bc/E8isHenre76NMxbJ3munSQatV8GoQ4XKWQg9vD34xMBqEFJGf
curl -X POST --data '{
    "jsonrpc": "2.0",
    "method": "timestampvm.proposeBlock",
    "params":{
        "data":"0x6d796e6577626c6f636b0000000000000000000000000000000000000000000014228326"
    },
    "id": 1
}' -H 'content-type:application/json;' 127.0.0.1:9650/ext/bc/timestamp
```

```sh
curl -X POST --data '{
    "jsonrpc": "2.0",
    "method": "timestampvm.getBlock",
    "params":{},
    "id": 1
}' -H 'content-type:application/json;' 127.0.0.1:9650/ext/bc/timestamp
```
