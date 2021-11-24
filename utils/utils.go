package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/hashing"
)

const genesisNetworkIDKey = "networkID"

func ToNodeID(stakingKey, stakingCert []byte) (ids.ShortID, error) {
	cert, err := tls.X509KeyPair(stakingCert, stakingKey)
	if err != nil {
		return ids.ShortID{}, err
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return ids.ShortID{}, err
	}
	nodeID := ids.ShortID(
		hashing.ComputeHash160Array(
			hashing.ComputeHash256(cert.Leaf.Raw),
		),
	)
	return nodeID, err
}

// Returns the network ID in the given genesis
func NetworkIDFromGenesis(genesis []byte) (uint32, error) {
	genesisMap := map[string]interface{}{}
	if err := json.Unmarshal(genesis, &genesisMap); err != nil {
		return 0, fmt.Errorf("couldn't unmarshal genesis: %w", err)
	}
	networkIDIntf, ok := genesisMap[genesisNetworkIDKey]
	if !ok {
		return 0, fmt.Errorf("couldn't find key %q in genesis", genesisNetworkIDKey)
	}
	networkID, ok := networkIDIntf.(float64)
	if !ok {
		return 0, fmt.Errorf("expected float64 but got %T", networkIDIntf)
	}
	return uint32(networkID), nil
}

// Returns nil when all the nodes in [net] are healthy,
// or an error if one doesn't become healthy within
// the timeout.
func AwaitNetworkHealthy(net network.Network, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	healthyCh := net.Healthy(ctx)
	return <-healthyCh
}
