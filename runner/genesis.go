package runner

import (
	"fmt"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/units"
)

// GenerateDefaultGenesis
// Give some random addresses an initial balance,
// and make all nodes validators.
// Note that you also read a genesis file from disk
// and set [networkConfig.Genesis] to that.
func GenerateDefaultGenesis(log logging.Logger, networkID uint32, nodeCount int) ([]byte, []node.Config, error) {
	nodeConfigs := make([]node.Config, nodeCount)
	allNodeIDs := make([]ids.ShortID, nodeCount)
	for i := 0; i < nodeCount; i++ {
		cert, key, err := staking.NewCertAndKeyBytes()
		if err != nil {
			return nil, nil, err
		}
		nodeID, err := utils.ToNodeID(key, cert)
		if err != nil {
			return nil, nil, err
		}
		n := node.Config{
			Name:        fmt.Sprintf("node-%d", i),
			StakingKey:  key,
			StakingCert: cert,
			ConfigFile:  nil,
			NodeID:      nodeID,
		}
		if i == 0 {
			n.IsBeacon = true
		}
		allNodeIDs[i] = nodeID
		nodeConfigs[i] = n
	}
	genesis, err := network.NewAvalancheGoGenesis(
		log,
		networkID, // Network ID
		[]network.AddrAndBalance{ // X-Chain Balances
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 1,
			},
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 2,
			},
		},
		[]network.AddrAndBalance{ // C-Chain Balances
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 3,
			},
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 4,
			},
		},
		allNodeIDs, // Make all nodes validators
	)
	if err != nil {
		return nil, nil, err
	}
	return genesis, nodeConfigs, nil
}
