# `avalanche-network-runner` Commands

## Global Flags

- `--dial-timeout duration`      server dial timeout (default 10s)
- `--endpoint string`            server endpoint (default "0.0.0.0:8080")
- `--log-dir string`             log directory
- `--log-level string`           log level (default "INFO")
- `--request-timeout duration`   client request timeout (default 3m0s)

## Ping

Ping the server.

### Usage

```sh
avalanche-network-runner ping [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner ping
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/ping'
```

## Server

Start a network runner server.

### Usage

`avalanche-network-runner server [options] [flags]`

### Flags

- `--dial-timeout duration` server dial timeout (default 10s)
- `--disable-grpc-gateway`true to disable grpc-gateway server (overrides `--grpc-gateway-port`)
- `--disable-nodes-output` true to disable nodes stdout/stderr
- `--grpc-gateway-port string` grpc-gateway server port (default ":8081")
- `--log-dir string` log directory
- `--log-level string` log level for server logs (default "INFO")
- `--port string` server port (default ":8080")
- `--snapshots-dir string` directory for snapshots

## Example

cli

```sh
avalanche-network-runner server
```

## Control

Start a network runner controller.

### Usage

`avalanche-network-runner control [command]`

## `add-node`

Add a new node to the network.

### Flags

- `--avalanchego-path string` avalanchego binary path
- `--chain-configs string` [optional] JSON string of map from chain id to its config file contents
- `--node-config string` node config as string
- `--plugin-dir string` [optional] plugin directory
- `--subnet-configs string` [optional] JSON string of map from subnet id to its config file contents
- `--upgrade-configs string` [optional] JSON string of map from chain id to its upgrade file contents

### Usage

`avalanche-network-runner control add-node node-name [options] [flags]`

### Example

cli

```sh
avalanche-network-runner control add-node node6
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/addnode' \
--header 'Content-Type: application/json' \
--data '{
  "name": "node6"
}'
```

## `add-permissionless-delegator`

Delegate to a permissionless validator in an elastic subnet

### Usage

`avalanche-network-runner control add-permissionless-delegator permissionlessValidatorSpecs [options] [flags]`

### Example

cli

```sh
avalanche-network-runner control add-permissionless-delegator '{"validatorSpec":[{"subnet_id":"p433wpuXyJiDhyazPYyZMJeaoPSW76CBZ2x7wrVPLgvokotXz","node_name":"node5","asset_id":"U8iRqJoiJm8xZHAacmvYyZVwqQx6uDNtQeP3CQ6fcgQk3JqnK","staked_token_amount":2000,"start_time":"2023-09-25 21:00:00","stake_duration":336}]}'
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/addpermissionlessdelegator' \
--header 'Content-Type: application/json' \
--data '{
    "validatorSpec": [
        {
          "subnet_id": "p433wpuXyJiDhyazPYyZMJeaoPSW76CBZ2x7wrVPLgvokotXz",
          "node_name": "node5",
          "asset_id": "U8iRqJoiJm8xZHAacmvYyZVwqQx6uDNtQeP3CQ6fcgQk3JqnK",
          "staked_token_amount": 2000,
          "start_time": "2023-09-25 21:00:00",
          "stake_duration": 336
        }
    ]
    
}'
```

## `add-permissionless-validator`

Add permissionless validator to elastic subnets.

### Usage

`avalanche-network-runner control add-permissionless-validator permissionlessValidatorSpecs [options] [flags]`

### Example

cli

```sh
avalanche-network-runner control add-permissionless-validator '[{"subnet_id":"p433wpuXyJiDhyazPYyZMJeaoPSW76CBZ2x7wrVPLgvokotXz","node_name":"node5","staked_token_amount":2000,"asset_id":"U8iRqJoiJm8xZHAacmvYyZVwqQx6uDNtQeP3CQ6fcgQk3JqnK","start_time":"2023-09-25 21:00:00","stake_duration":336}]'
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/addpermissionlessvalidator' \
--header 'Content-Type: application/json' \
--data '{
  "validatorSpec": [
    {
      "subnetId":"p433wpuXyJiDhyazPYyZMJeaoPSW76CBZ2x7wrVPLgvokotXz",
      "nodeName":"node1", 
      "stakedTokenAmount": 2000, 
      "assetId": "U8iRqJoiJm8xZHAacmvYyZVwqQx6uDNtQeP3CQ6fcgQk3JqnK", 
      "startTime": "2023-05-25 21:00:00", 
      "stakeDuration": 336
    }
]}'
```

## `attach-peer`

Attaches a peer to the node.

### Usage

`avalanche-network-runner control attach-peer node-name [options] [flags]`

### Example

cli

```sh
avalanche-network-runner control attach-peer node5
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/attachpeer' \
--header 'Content-Type: application/json' \
--data '{
    "nodeName":"node5"
}'
```

## `create-blockchains`

Create blockchains.

### Usage

`avalanche-network-runner control create-blockchains blockchain-specs [options] [flags]`

### Example

cli

```sh
avalanche-network-runner control create-blockchains '[{"vm_name":"subnetevm","genesis":"/path/to/genesis.json", "subnet_id": "p433wpuXyJiDhyazPYyZMJeaoPSW76CBZ2x7wrVPLgvokotXz"}]'
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/createblockchains' \
--header 'Content-Type: application/json' \
--data '{
  "blockchainSpecs": [
    {
      "vm_name": "subnetevm",
      "genesis": "/path/to/genesis.json", 
      "subnet_id": "p433wpuXyJiDhyazPYyZMJeaoPSW76CBZ2x7wrVPLgvokotXz"
    }
  ]
}'
```

## `create-subnets`

Create subnets.

### Usage

`avalanche-network-runner control create-subnets [options] [flags]`

### Example

cli

```sh
avalanche-network-runner control create-subnets '[{"participants": ["node1", "node2", "node3", "node4", "node5"]}]'
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/createsubnets' \
--header 'Content-Type: application/json' \
--data '
{
    "participants": [
        "node1",
        "node2",
        "node3",
        "node4",
        "node5"
    ]
}'
```

## `elastic-subnets`

Transform subnets to elastic subnets.

### Usage

```sh
avalanche-network-runner control elastic-subnets elastic_subnets_specs [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control elastic-subnets '[{"subnet_id":"p433wpuXyJiDhyazPYyZMJeaoPSW76CBZ2x7wrVPLgvokotXz", "asset_name":"Avalanche", 
"asset_symbol":"AVAX", "initial_supply": 240000000, "max_supply": 720000000, "min_consumption_rate": 100000, 
"max_consumption_rate": 120000, "min_validator_stake": 2000, "max_validator_stake": 3000000, "min_stake_duration": 336, 
"max_stake_duration": 8760, "min_delegation_fee": 20000, "min_delegator_stake": 25, "max_validator_weight_factor": 5, 
"uptime_requirement": 800000}]'
```

curl

```sh
curl -X POST -k http://localhost:8081/v1/control/transformelasticsubnets -d '{"elasticSubnetSpec": [{"subnetId":"p433wpuXyJiDhyazPYyZMJeaoPSW76CBZ2x7wrVPLgvokotXz","assetName":"Avalanche", "assetSymbol":"AVAX", "initialSupply": 240000000, "maxSupply": 720000000, "minConsumption_rate": 100000, "maxConsumption_rate": 120000, "minValidatorStake": 2000, "maxValidatorStake": 3000000, "minStakeDuration": 336, "maxStakeDuration": 8760, "minDelegationFee": 20000, "minDelegatorStake": 25, "maxValidatorWeightFactor": 5, "uptimeRequirement": 800000}]}'
```

## `get-snapshot-names`

Requests server to get list of snapshot.

### Usage

```sh
avalanche-network-runner control get-snapshot-names [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control get-snapshot-names
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/getsnapshotnames' 
```

## `health`

Requests server health.

### Usage

```sh
avalanche-network-runner control health [options] [flags]
```

### Example

cli

```sh
./build/avalanche-network-runner control health
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/health'
```

## `list-blockchains`

Lists all blockchain ids of the `network.

### Usage

```sh
avalanche-network-runner control list-blockchains [flags]
```

### Example

cli

```sh
avalanche-network-runner control list-blockchains
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/listblockchains'
```

## `list-rpcs`

Lists rpcs for all blockchain of the network.

### Flags

### Usage

```sh
avalanche-network-runner control list-rpcs [flags]
```

### Example

cli

```sh
avalanche-network-runner control list-rpcs
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/listrpcs'
```

## `list-subnets`

Lists all subnet ids of the network.

### Usage

```sh
avalanche-network-runner control list-subnets [flags]
```

### Example

cli

```sh
avalanche-network-runner control list-subnets
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/listsubnets'
```

## `load-snapshot`

Requests server to load network snapshot.

### Flags

- `--avalanchego-path string`     avalanchego binary path
- `--chain-configs string`        [optional] JSON string of map from chain id to its config file contents
- `--global-node-config string`   [optional] global node config as JSON string, applied to all nodes
- `--plugin-dir string`           plugin directory
- `--reassign-ports-if-used`      true to reassign snapshot ports if already taken
- `--root-data-dir string`        root data directory to store logs and configurations
- `--subnet-configs string`       [optional] JSON string of map from subnet id to its config file contents
- `--upgrade-configs string`      [optional] JSON string of map from chain id to its upgrade file contents

### Usage

```sh
avalanche-network-runner control load-snapshot snapshot-name [flags]

 if the `AVALANCHEGO_EXEC_PATH` and `AVALANCHEGO_PLUGIN_PATH` env vars aren't set then you should pass them in as a flag
avalanche-network-runner control load-snapshot snapshotName --avalanchego-path /path/to/avalanchego/binary --plugin-dir /path/to/avalanchego/plugins
```

### Example

cli

```sh
avalanche-network-runner control load-snapshot snapshot

```

curl

```sh
curl --location 'http://localhost:8081/v1/control/loadsnapshot' \
--header 'Content-Type: application/json' \
--data '{
    "snapshotName":"snapshot"
}'

 if the `AVALANCHEGO_EXEC_PATH` and `AVALANCHEGO_PLUGIN_PATH` env vars aren't set then you should pass them in to the curl
curl -X POST -k http://localhost:8081/v1/control/loadsnapshot -d '{"snapshotName":"node5","execPath":"/path/to/avalanchego/binary","pluginDir":"/path/to/avalanchego/plugins"}'
```

## `pause-node`

Pauses a node.

### Usage

```sh
avalanche-network-runner control pause-node node-name [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control pause-node node5
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/pausenode' \
--header 'Content-Type: application/json' \
--data '{
  "name": "node5"
}'
```

## `remove-node`

Removes a node.

### Usage

```sh
avalanche-network-runner control remove-node node-name [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control remove-node node5
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/removenode' \
--header 'Content-Type: application/json' \
--data '{
    "name":"node5"
}'
```

## `remove-snapshot`

Requests server to remove network snapshot.

### Usage

```sh
avalanche-network-runner control remove-snapshot snapshot-name [flags]
```

### Example

cli

```sh
avalanche-network-runner control remove-snapshot node5
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/removesnapshot' \
--header 'Content-Type: application/json' \
--data '{
    "snapshot_name":"node5"
}'
```

## `remove-subnet-validator`

Remove subnet validator

### Usage

```sh
avalanche-network-runner control remove-subnet-validator removeValidatorSpec [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control remove-subnet-validator '[{"subnetId": "p433wpuXyJiDhyazPYyZMJeaoPSW76CBZ2x7wrVPLgvokotXz", "nodeNames":["node1"]}]'
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/removesubnetvalidator' \
--header 'Content-Type: application/json' \
--data '[{"subnetId": "p433wpuXyJiDhyazPYyZMJeaoPSW76CBZ2x7wrVPLgvokotXz", "nodeNames":["node1"]}]'
```

## `restart-node`

Restarts a node.

### Flags

- `--avalanchego-path string`      avalanchego binary path
- `--chain-configs string`         [optional] JSON string of map from chain id to its config file contents
- `--plugin-dir string`            [optional] plugin directory
- `--subnet-configs string`        [optional] JSON string of map from subnet id to its config file contents
- `--upgrade-configs string`       [optional] JSON string of map from chain id to its upgrade file contents
- `--whitelisted-subnets string`   [optional] whitelisted subnets (comma-separated)

### Usage

```sh
avalanche-network-runner control restart-node node-name [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control restart-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
node1 
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/restartnode' \
--header 'Content-Type: application/json' \
--data '{
  "name": "node5"
}'
```

## `resume-node`

Resumes a node.

### Usage

```sh
avalanche-network-runner control resume-node node-name [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control resume-node node5
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/resumenode' \
--header 'Content-Type: application/json' \
--data '{
  "name": "node5"
}'
```

## `rpc_version`

Requests RPC server version.

### Usage

```sh
avalanche-network-runner control rpc_version [flags]
```

### Example

cli

```sh
./build/avalanche-network-runner control rpc_version
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/rpcversion'
```

## `save-snapshot`

Requests server to save network snapshot.

### Usage

```sh
avalanche-network-runner control save-snapshot snapshot-name [flags]
```

### Example

cli

```sh
avalanche-network-runner control save-snapshot snapshotName
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/savesnapshot' \
--header 'Content-Type: application/json' \
--data '{
    "snapshot_name":"node5"
}'
```

## `send-outbound-message`

Sends an outbound message to an attached peer.

### Flags

- `--message-bytes-b64 string`   Message bytes in base64 encoding
- `--message-op uint32`          Message operation type
- `--peer-id string`             peer ID to send a message to

### Usage

```sh
avalanche-network-runner control send-outbound-message node-name [options] [flags]
```

### Example

cli

```sh

avalanche-network-runner control send-outbound-message \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node1 \
--peer-id "7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg" \
--message-op=16 \
--message-bytes-b64="EAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAKgAAAAPpAqmoZkC/2xzQ42wMyYK4Pldl+tX2u+ar3M57WufXx0oXcgXfXCmSnQbbnZQfg9XqmF3jAgFemSUtFkaaZhDbX6Ke1DVpA9rCNkcTxg9X2EcsfdpKXgjYioitjqca7WA="
```

curl

```sh
curl -X POST -k http://localhost:8081/v1/control/sendoutboundmessage -d '{"nodeName":"node1","peerId":"7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg","op":16,"bytes":"EAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAKgAAAAPpAqmoZkC/2xzQ42wMyYK4Pldl+tX2u+ar3M57WufXx0oXcgXfXCmSnQbbnZQfg9XqmF3jAgFemSUtFkaaZhDbX6Ke1DVpA9rCNkcTxg9X2EcsfdpKXgjYioitjqca7WA="}'
```

## `start`

Starts the server.

### Flags

- `--avalanchego-path string`                  avalanchego binary path
- `--blockchain-specs string`                  [optional] JSON string of array of [(VM name, genesis file path)]
- `--chain-configs string`                     [optional] JSON string of map from chain id to its config file contents
- `--custom-node-configs global-node-config`   [optional] custom node configs as JSON string of map, for each node individually. Common entries override global-node-config, but can be combined. Invalidates `number-of-nodes` (provide all node configs if used).
- `--dynamic-ports`                            true to assign dynamic ports
- `--global-node-config string`                [optional] global node config as JSON string, applied to all nodes
- `--number-of-nodes uint32`                   number of nodes of the network (default 5)
- `--plugin-dir string`                        [optional] plugin directory
- `--reassign-ports-if-used`                   true to reassign default/given ports if already taken
- `--root-data-dir string`                     [optional] root data directory to store logs and configurations
- `--subnet-configs string`                    [optional] JSON string of map from subnet id to its config file contents
- `--upgrade-configs string`                  [optional] JSON string of map from chain id to its upgrade file contents
- `--whitelisted-subnets string`               [optional] whitelisted subnets (comma-separated)

### Usage

```sh
avalanche-network-runner control start [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control start \
  --log-level debug \
  --endpoint="0.0.0.0:8080" \
  --number-of-nodes=5 \
  --blockchain-specs '[{"vm_name": "subnetevm", "genesis": "./path/to/config.json"}]'
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/start' \
--header 'Content-Type: application/json' \
--data '{
  "numNodes": 5,
  "blockchainSpecs": [
    {
      "vm_name": "subnetevm",
      "genesis": "/path/to/config.json"
    }
  ]
}'
```

## `status`

Requests server status.

### Usage

```sh
avalanche-network-runner control status [options] [flags]
```

### Example

cli

```sh
./build/avalanche-network-runner control status
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/status'
```

## `stop`

Requests server stop.

### Usage

```sh
avalanche-network-runner control stop [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control stop
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/stop'
```

## `stream-status`

Requests server bootstrap status.

### Flags

- `--push-interval duration`   interval that server pushes status updates to the client (default 5s)

### Usage

```sh
avalanche-network-runner control stream-status [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control stream-status
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/streamstatus'
```

## `uris`

Requests server uris.

### Usage

```sh
avalanche-network-runner control uris [options] [flags]
```

### Example

cli

```sh
avalanche-network-runner control uris
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/uris'
```

## `vmid`

Returns the vm id associated to the given vm name.

### Usage

```sh
avalanche-network-runner control vmid vm-name [flags]
```

### Example

cli

```sh
/build/avalanche-network-runner control vmid subnetevm
```

curl

```sh
curl --location 'http://localhost:8081/v1/control/vmid' \
--header 'Content-Type: application/json' \
--data '{
    "vmName": "subnetevm"
}'
```

## `wait-for-healthy`

Wait until local cluster and custom vms are ready.

### Usage

```sh
avalanche-network-runner control wait-for-healthy [options] [flags]
```

### Example

cli

```sh
./build/avalanche-network-runner control wait-for-healthy
```

curl

```sh
curl --location --request POST 'http://localhost:8081/v1/control/waitforhealthy'
```
