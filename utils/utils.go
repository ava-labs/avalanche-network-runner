package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"golang.org/x/sync/errgroup"
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

// NewLocalNodeConfigJsonRaw returns a JSON formatted string as json.RawMessage for a
// local node config object (ImplSpecificConfig)
func NewLocalNodeConfigJsonRaw(binaryPath string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"binaryPath":"%s"}`, binaryPath))
}

// NewK8sNodeConfigJsonRaw returns a JSON formatted string as json.RawMessage for a
// kubernetes node config object (ImplSpecificConfig)
func NewK8sNodeConfigJsonRaw(
	api,
	id,
	image,
	kind,
	namespace,
	tag string,
) json.RawMessage {
	return json.RawMessage(
		fmt.Sprintf(`{"apiVersion":"%s","identifier":"%s","image":"%s","kind":"%s","namespace":"%s","tag":"%s"}`,
			api, id, image, kind, namespace, tag,
		),
	)
}

// AwaitAllNodesPChainTxAccepted returns nil when all nodes in [nodes]
// have accepted transaction [txID].
// Returns an error if [ctx] is cancelled before all nodes accept [txID],
// or if [txID] is aborted/dropped.
// Note that the caller should eventually cancel [ctx], or else
// this function may never terminate.
func AwaitAllNodesPChainTxAccepted(
	ctx context.Context,
	log logging.Logger,
	apiRetryFreq time.Duration,
	nodes map[string]node.Node,
	txID ids.ID,
) error {
	g, ctx := errgroup.WithContext(ctx)
	for nodeName, node := range nodes {
		client := node.GetAPIClient().PChainAPI()
		nodeName := nodeName
		g.Go(func() error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(apiRetryFreq):
				}
				status, err := client.GetTxStatus(txID, true)
				if err != nil {
					return err
				}
				switch status.Status {
				case platformvm.Committed:
					log.Debug("tx %s accepted on node %s", txID, nodeName)
					return nil
				case platformvm.Dropped, platformvm.Aborted:
					return fmt.Errorf("expected transaction %s to be accepted but was dropped on node %s", txID, nodeName)
				default: // Processing or unknown
					log.Debug("waiting for tx %s to be accepted on node %s", txID, nodeName)
				}
			}
		})
	}
	return g.Wait()
}
