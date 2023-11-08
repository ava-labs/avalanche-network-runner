# Examples

## Configuring network nodes on start

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

To set `avalanchego --http-host` flag for all nodes:

```sh
# to expose local RPC server to all traffic
# (e.g., run network runner within cloud instance)
curl -X POST -k http://localhost:8081/v1/control/start -d '{"globalNodeConfig":"{\"http-host\":\"0.0.0.0\"}"}'

# or
avalanche-network-runner control start \
--global-node-config '{"http-host":"0.0.0.0"}'
```

Example usage of `--custom-node-configs` to get deterministic API port numbers:

```sh
curl -X POST -k http://localhost:8081/v1/control/start -d\ '{"customNodeConfigs": { "node1":"{\"http-port\":9650}", "node2":"{\"http-port\":9652}", "node3":"{\"http-port\":9654}", "node4":"{\"http-port\":9656}", "node5":"{\"http-port\":9658}" }}'

# or
avalanche-network-runner control start \
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

## Waiting for network health and querying network status

To wait for all the nodes in the network to become healthy:

```sh
curl -X POST -k http://localhost:8081/v1/control/health

# or
avalanche-network-runner control health
```

To get the API endpoints of all nodes in the network:

```sh
curl -X POST -k http://localhost:8081/v1/control/uris

# or
avalanche-network-runner control uris
```

To query the network status from the server:

```sh
curl -X POST -k http://localhost:8081/v1/control/status

# or
avalanche-network-runner control status
```

To stream network status:

```sh
avalanche-network-runner control stream-status
```

## Operating with network snapshots

To save the network to a snapshot:

```sh
curl -X POST -k http://localhost:8081/v1/control/savesnapshot -d '{"snapshotName":"node5"}'

# or
avalanche-network-runner control save-snapshot snapshotName
```

To load a network from a snapshot:

```sh
curl -X POST -k http://localhost:8081/v1/control/loadsnapshot -d '{"snapshotName":"node5"}'

# or
avalanche-network-runner control load-snapshot snapshotName
```

An avalanchego binary path and/or plugin dir can be specified when loading the snapshot. This is
optional. If not specified, will use the paths saved with the snapshot:

```sh
curl -X POST -k http://localhost:8081/v1/control/loadsnapshot -d '{"snapshotName":"node5"}'

# or
avalanche-network-runner control load-snapshot snapshotName
```

To get the list of snapshots:

```sh
curl -X POST -k http://localhost:8081/v1/control/getsnapshotnames

# or
avalanche-network-runner control get-snapshot-names
```

To remove a snapshot:

```sh
curl -X POST -k http://localhost:8081/v1/control/removesnapshot -d '{"snapshotName":"node5"}'

# or
avalanche-network-runner control remove-snapshot snapshotName
```

## Operating with subnets

To create 1 validated subnet, with all existing nodes as participants (requires network restart):

```sh
curl -X POST -k http://localhost:8081/v1/control/createsubnets -d '{"subnetSpecs": [{}]}'

# or
avalanche-network-runner control create-subnets '[{}]'
```

To create 1 validated subnet, with some of existing nodes as participants (requires network restart):

```sh
curl -X POST -k http://localhost:8081/v1/control/createsubnets -d '{"subnetSpecs:" [{"participants": ["node1", "node2"]}]}'

# or
avalanche-network-runner control create-subnets '[{"participants": ["node1", "node2"]}]'
```

To create 1 validated subnet, with some of existing nodes and another new node as participants (requires network restart):

```sh
curl -X POST -k http://localhost:8081/v1/control/createsubnets -d '{"subnetSpecs": [{"participants": ["node1", "node2", "testNode"]}]}'

# or
avalanche-network-runner control create-subnets '[{"participants": ["node1", "node2", "testNode"]}]'

```

To create N validated subnets (requires network restart):

```sh
curl -X POST -k http://localhost:8081/v1/control/createsubnets -d '{"subnetSpecs": [{}, {"participants": ["node1", "node2", "node3"]}, {"participants": ["node1", "node2", "testNode"]}]}'

# or
avalanche-network-runner control create-subnets '[{}, {"participants": ["node1", "node2", "node3"]}, {"participants": ["node1", "node2", "testNode"]}]'

```

To get a list of all the subnet ids:

```sh
curl -X POST -k http://localhost:8081/v1/control/listsubnets

# or
avalanche-network-runner control list-subnets
```

To add a node as a validator to a Subnet:

```sh
curl -X POST -k http://localhost:8081/v1/control/addsubnetvalidators  -d '[{"subnetId": "'$SUBNET_ID'", "nodeNames":["node1"]}]'

# or
avalanche-network-runner control add-subnet-validators '[{"subnet_id": "'$SUBNET_ID'", "node_names":["node1"]}]'
```

To remove a node as a validator from a Subnet:

```sh
curl -X POST -k http://localhost:8081/v1/control/removesubnetvalidator  -d '[{"subnetId": "'$SUBNET_ID'", "nodeNames":["node1"]}]'

# or
avalanche-network-runner control remove-subnet-validator '[{"subnet_id": "'$SUBNET_ID'", "node_names":["node1"]}]'
```


## Operating with blockchains

**Note**: To create a blockchain, the vm binary for it should be present under the plugin dir, with a filename equal to the vm id. The plugin dir
can be specified either in environment variable `AVALANCHEGO_PLUGIN_PATH` before starting the server, or as flag `--plugin-dir` to network start command.

The vm id can be derived from the vm name by using:

```sh
curl -X POST -k http://localhost:8081/v1/control/vmid -d '{"vmName": "'${VM_NAME}'"}'

# or
avalanche-network-runner control vmid ${VM_NAME}
```

To create a blockchain without a subnet id (requires network restart):

```sh
curl -X POST -k http://localhost:8081/v1/control/createblockchains -d '{"blockchainSpecs":[{"vmName":"'$VM_NAME'","genesis":"'$GENESIS_PATH'"}]}'

# or
avalanche-network-runner control create-blockchains '[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'"}]'
```

Genesis can be given either as file path or file contents:

```sh
curl -X POST -k http://localhost:8081/v1/control/createblockchains -d '{"blockchainSpecs":[{"vmName":"'$VM_NAME'","genesis":"'$GENESIS_CONTENTS'"}]}'

# or
avalanche-network-runner control create-blockchains '[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_CONTENTS'"}]'
```

To create a blockchain with a subnet id (does not require restart):

```sh
curl -X POST -k http://localhost:8081/v1/control/createblockchains -d '{"blockchainSpecs":[{"vmName":"'$VM_NAME'","genesis":"'$GENESIS_PATH'","subnetId":"'$SUBNET_ID'"}]}'

# or
avalanche-network-runner control create-blockchains '[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnet_id": "'$SUBNET_ID'"}]'
```

To create a blockchain with a subnet id, and chain config, network upgrade and subnet config file paths (requires network restart):

```sh
curl -X POST -k http://localhost:8081/v1/control/createblockchains -d '{"blockchainSpecs":[{"vmName":"'$VM_NAME'","genesis":"'$GENESIS_PATH'","subnetId": "'$SUBNET_ID'","chainConfig": "'$CHAIN_CONFIG_PATH'","networkUpgrade":"'$NETWORK_UPGRADE_PATH'","subnetConfig":"'$SUBNET_CONFIG_PATH'"}]}'

# or
avalanche-network-runner control create-blockchains '[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnet_id": "'$SUBNET_ID'", "chain_config": "'$CHAIN_CONFIG_PATH'", "network_upgrade": "'$NETWORK_UPGRADE_PATH'", "subnet_config": "'$SUBNET_CONFIG_PATH'"}]'
```

To create a blockchain with a new subnet id with select nodes as participants (requires network restart):
(New nodes will first be added as primary validators similar to the process in `create-subnets`)

```sh
curl -X POST -k http://localhost:8081/v1/control/createblockchains -d '{"blockchainSpecs":[{"vmName":"'$VM_NAME'","genesis":"'$GENESIS_PATH'","subnetSpec": {"participants": ["node1", "node2", "testNode"]}"]}'

# or
avalanche-network-runner control create-blockchains '[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnet_spec": "{"participants": ["node1", "node2", "testNode"]}]'
```

To create two blockchains in two disjoint subnets (not shared validators), and where all validators have bls keys (participants new to the network):

```sh
curl -X POST -k http://localhost:8081/v1/control/createblockchains -d '{hainSpecs":[{"vmName":"'$VM_NAME'","genesis":"'$GENESIS_PATH'","subnetSpec": {"participants": ["new_node1", "new_node2"]}},{"vmName":"'$VM_NAME'","genesis":"'$GENESIS_PATH'","subnetSpec": {"participants": ["new_node3", "new_node4"]}}]'

# or
go run main.go control create-blockchains '[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnet_spec": {"participants": ["new_node1", "new_node2"]}},{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnet_spec": {"participants": ["new_node3", "new_node4"]}}]'
```

Chain config can also be defined on a per node basis. For that, a per node chain config file is needed, which is a JSON that specifies the chain config per node. For example, given the following as the contents of the file with path `$PER_NODE_CHAIN_CONFIG`:

```json
{
    "node1": {"rpc-tx-fee-cap": 101},
    "node2": {"rpc-tx-fee-cap": 102},
    "node3": {"rpc-tx-fee-cap": 103},
    "node4": {"rpc-tx-fee-cap": 104},
    "node5": {"rpc-tx-fee-cap": 105}
}
```

Then a blockchain with different chain configs per node can be created with this command:

```sh
curl -X POST -k http://localhost:8081/v1/control/createblockchains -d '{"blockchainSpecs":[{"vmName":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnetId": "'$SUBNET_ID'","perNodeChainConfig": "'$PER_NODE_CHAIN_CONFIG'","networkUpgrade":"'$NETWORK_UPGRADE_PATH'","subnetConfig":"'$SUBNET_CONFIG_PATH'"}]}'

# or
avalanche-network-runner control create-blockchains '[{"vm_name":"'$VM_NAME'","genesis":"'$GENESIS_PATH'", "subnet_id": "'$SUBNET_ID'", "per_node_chain_config": "'$PER_NODE_CHAIN_CONFIG'", "network_upgrade": "'$NETWORK_UPGRADE_PATH'", "subnet_config": "'$SUBNET_CONFIG_PATH'"}]'
```

To get a list of all the blockchains (containing: blockchain id, subnet id, vm id, vm name):

```sh
curl -X POST -k http://localhost:8081/v1/control/listblockchains

# or
avalanche-network-runner control list-blockchains
```

To get a list of all the rpc urls given for all the blockchains and nodes:

```sh
curl -X POST -k http://localhost:8081/v1/control/listrpcs

# or
avalanche-network-runner control list-rpcs
```

## Interacting with the nodes

To remove (stop) a node:

```sh
curl -X POST -k http://localhost:8081/v1/control/removenode -d '{"name":"node5"}'

# or
avalanche-network-runner control remove-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
node5
```

To restart a node (in this case, the one named `node1`):

```sh
# Note that you can restart the node with a different binary by providing
# a different execPath
curl -X POST -k http://localhost:8081/v1/control/restartnode -d '{"name":"node1","logLevel":"INFO"}'

# or
avalanche-network-runner control restart-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
node1 
```

To add a node (in this case, a new node named `node99`):

```sh
# Note that you can add the new node with a different binary by providing
# a different execPath
curl -X POST -k http://localhost:8081/v1/control/addnode -d '{"name":"node99","logLevel":"INFO"}'

# or
avalanche-network-runner control add-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
node99 
```

To pause a node (in this case, node named `node99`):

```sh
# e.g., ${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego

curl -X POST -k http://localhost:8081/v1/control/pausenode -d '{"name":"node99","logLevel":"INFO"}'

# or
avalanche-network-runner control pause-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
node99 
```

To resume a paused node (in this case, node named `node99`):

```sh
curl -X POST -k http://localhost:8081/v1/control/resumenode -d '{"name":"node99","logLevel":"INFO"}'

# or
avalanche-network-runner control resume-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
node99 
```

You can also provide additional flags that specify the node's config:

```sh
  --node-config '{"index-enabled":false, "api-admin-enabled":true,"network-peer-list-gossip-frequency":"300ms"}'
```

`--node-config` allows to specify specific avalanchego config parameters to the new node. See [here](https://docs.avax.network/build/references/avalanchego-config-flags) for the reference of supported flags.

**Note**: The following parameters will be _ignored_ if set in `--node-config`, because the network runner needs to set its own in order to function properly:
`--log-dir`
`--db-dir`

## Interacting with the test peers

AvalancheGo exposes a "test peer", which you can attach to a node.
(See [here](https://github.com/ava-labs/avalanchego/blob/master/network/peer/test_peer.go) for more information.)
You can send messages through the test peer to the node it is attached to.

To attach a test peer to a node (in this case, `node1`):

```sh
curl -X POST -k http://localhost:8081/v1/control/attachpeer -d '{"nodeName":"node1"}'

# or
avalanche-network-runner control attach-peer \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node1
```

To send a chit message to the node through the test peer:

```sh
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

## Stopping a network

To terminate the network:

```sh
curl -X POST -k http://localhost:8081/v1/control/stop

# or
avalanche-network-runner control stop \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

Note: this does not stops the server.

## Elastic Subnets

**Elastic Subnets are permissionless Subnets. More information can be found [here](https://docs.avax.network/subnets/reference-elastic-subnets-parameters).**

To transform permissioned Subnets into permissionless Subnets (NOTE: this action is irreversible):

Values provided below are the default values on [Primary Network on Mainnet](https://docs.avax.network/subnets/reference-elastic-subnets-parameters#primary-network-parameters-on-mainnet).

```sh
curl -X POST -k http://localhost:8081/v1/control/transformelasticsubnets -d '{"elasticSubnetSpec": [{"subnetId":"'$SUBNET_ID'","assetName":"'$ASSET_NAME'", 
"assetSymbol":"'$ASSET_SYMBOL'", "initialSupply": 240000000, "maxSupply": 720000000, "minConsumption_rate": 100000, 
"maxConsumption_rate": 120000, "minValidatorStake": 2000, "maxValidatorStake": 3000000, "minStakeDuration": 336, 
"maxStakeDuration": 8760, "minDelegationFee": 20000, "minDelegatorStake": 25, "maxValidatorWeightFactor": 5, 
"uptimeRequirement": 800000}]}'

# or
avalanche-network-runner control elastic-subnets '[{"subnet_id":"'$SUBNET_ID'", "asset_name":"'$ASSET_NAME'", 
"asset_symbol":"'$ASSET_SYMBOL'", "initial_supply": 240000000, "max_supply": 720000000, "min_consumption_rate": 100000, 
"max_consumption_rate": 120000, "min_validator_stake": 2000, "max_validator_stake": 3000000, "min_stake_duration": 336, 
"max_stake_duration": 8760, "min_delegation_fee": 20000, "min_delegator_stake": 25, "max_validator_weight_factor": 5, 
"uptime_requirement": 800000}]'
```

To enable a node to join an Elastic Subnet as a permissionless validator:

If the node specified in the command doesn't exist yet, it will be created and added as a primary network validator first
before being added as a permissionless validator in the Elastic Subnet.

If `start_time` and `stake_duration` are omitted, the default value for validation start time will be 30 seconds ahead from
when the command was called and the node will be a validator until the node stops validating on the primary network.

**Note**: Asset ID is returned by elastic-subnets command

```sh
curl -X POST -k http://localhost:8081/v1/control/addpermissionlessvalidator  -d '{"validatorSpec": [{"subnetId":"'$SUBNET_ID'","nodeName":"node1", 
"stakedTokenAmount": 2000, "assetId": "'$ASSET_ID'", "startTime": "2023-05-25 21:00:00", "stakeDuration": 336}]}'

# or
avalanche-network-runner control add-permissionless-validator '[{"subnet_id": "'$SUBNET_ID'", "node_name":"node1", 
"staked_token_amount": 2000, "asset_id": "'$ASSET_ID'", "start_time": "2023-05-25 21:00:00", "stake_duration": 336}]'
```

To remove a node as a permissioned validator from a Subnet:

```sh
curl -X POST -k http://localhost:8081/v1/control/removesubnetvalidator  -d '[{"subnetId": "'$SUBNET_ID'", "nodeNames":["node1"]}]'

# or
avalanche-network-runner control remove-subnet-validator '[{"subnetId": "'$SUBNET_ID'", "nodeNames":["node1"]}]'
```

To delegate stake in a permissionless validator in an Elastic Subnet:

Amount that can be delegated to a validator is detailed [here](https://docs.avax.network/subnets/reference-elastic-subnets-parameters#delegators-weight-checks).

If `start_time` and `stake_duration` are omitted, the default value for validation start time will be 30 seconds ahead from
when the command was called and the stake will be delegated until the node stops validating on the primary network.

```sh
curl -X POST -k http://localhost:8081/v1/control/addpermissionlessdelegator  -d '{"validatorSpec": [{"subnetId": "'$SUBNET_ID'", "nodeName":"node1", "assetId": "'$ASSET_ID'", "stakedTokenAmount": 2000, "startTime": "2023-05-25 21:00:00", "stakeDuration": 336}]}'

# or
avalanche-network-runner control ./bin/avalanche-network-runner control add-permissionless-delegator '[{"subnet_id": "'$SUBNET_ID'", "node_name":"node1", "asset_id": "'$ASSET_ID'", "staked_token_amount": 2000, "start_time": "2023-05-25 21:00:00", "stake_duration": 336}]'
```
