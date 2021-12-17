package utils

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"

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

// NewLocalNodeConfigJsonRaw returns a JSON formatted string as json.RawMessage for a
// local node config object (ImplSpecificConfig)
func NewLocalNodeConfigJsonRaw(binaryPath string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"binaryPath":"%s"}`, binaryPath))
}

// NewK8sNodeConfigJsonRaw returns a JSON formatted string as json.RawMessage for a
// kubernetes node config object (ImplSpecificConfig)
func NewK8sNodeConfigJsonRaw(
	api string,
	id string,
	image string,
	kind string,
	namespace string,
	tag string,
) json.RawMessage {
	return json.RawMessage(
		fmt.Sprintf(`{"apiVersion":"%s","identifier":"%s","image":"%s","kind":"%s","namespace":"%s","tag":"%s"}`,
			api, id, image, kind, namespace, tag,
		),
	)
}
