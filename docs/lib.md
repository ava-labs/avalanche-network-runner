# Using ANR core lib

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
  // Must not be nil.
  StakingSigningKey string `json:"stakingSigningKey"`
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
