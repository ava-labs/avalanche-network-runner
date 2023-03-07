package local

import (
	"context"

	"github.com/ava-labs/avalanche-network-runner/local/experimental"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
)

var _ experimental.LocalNetwork = (*localNetwork)(nil)

func (ln *localNetwork) GetClientURIUnsafe() (string, error) {
	return ln.getClientURI()
}

func (ln *localNetwork) GetNodeUnsafe(name string) node.Node {
	return ln.nodes[name]
}

func (ln *localNetwork) Logger() logging.Logger {
	return ln.log
}

func (ln *localNetwork) SetBlockchainConfigFilesUnsafe(chainSpecs []network.BlockchainSpec, blockchainTxs []*txs.Tx) bool {
	return ln.setBlockchainConfigFiles(chainSpecs, blockchainTxs, ln.log)
}

func (ln *localNetwork) RestartNodeUnsafe(ctx context.Context, nodeName string) error {
	return ln.restartNode(ctx, nodeName, "", "", "", nil, nil, nil)
}

func (ln *localNetwork) HealthyUnsafe(ctx context.Context) error {
	return ln.healthy(ctx)
}

func (ln *localNetwork) ReloadVMPluginsUnsafe(ctx context.Context) error {
	return ln.reloadVMPlugins(ctx)
}
