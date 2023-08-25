package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	rpcb "github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/ux"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	genesisNetworkIDKey = "networkID"
	dirTimestampFormat  = "20060102_150405"
)

func ToNodeID(stakingKey, stakingCert []byte) (ids.NodeID, error) {
	tlsCert, err := staking.LoadTLSCertFromBytes(stakingKey, stakingCert)
	if err != nil {
		return ids.EmptyNodeID, err
	}
	cert := staking.CertificateFromX509(tlsCert.Leaf)
	nodeID := ids.NodeIDFromCert(cert)
	return nodeID, nil
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

var (
	ErrEmptyExecPath   = errors.New("avalanche exec is not defined")
	ErrNotExists       = errors.New("avalanche exec not exists")
	ErrNotExistsPlugin = errors.New("plugin exec not exists")
)

func CheckExecPath(exec string) error {
	if exec == "" {
		return ErrEmptyExecPath
	}
	_, err := os.Stat(exec)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotExists
		}
		return fmt.Errorf("failed to stat exec %q (%w)", exec, err)
	}
	return nil
}

func CheckPluginPath(pluginExec string) error {
	var err error
	if _, err = os.Stat(pluginExec); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotExistsPlugin
		}
		return fmt.Errorf("failed to stat plugin exec %q (%w)", pluginExec, err)
	}

	return nil
}

func VMID(vmName string) (ids.ID, error) {
	if len(vmName) > 32 {
		return ids.Empty, fmt.Errorf("VM name must be <= 32 bytes, found %d", len(vmName))
	}
	b := make([]byte, 32)
	copy(b, []byte(vmName))
	return ids.ToID(b)
}

func MkDirWithTimestamp(dirPrefix string) (string, error) {
	currentTime := time.Now().Format(dirTimestampFormat)
	dirName := dirPrefix + "_" + currentTime
	return dirName, os.MkdirAll(dirName, os.ModePerm)
}

func VerifySubnetHasCorrectParticipants(
	log logging.Logger,
	subnetParticipants []string,
	cluster *rpcb.ClusterInfo,
	subnetID string,
) bool {
	if cluster != nil {
		participatingNodeNames := cluster.Subnets[subnetID].GetSubnetParticipants().GetNodeNames()

		var nodeIsInList bool
		// Check that all subnet validators are equal to the node IDs added as participant in subnet creation
		for _, node := range subnetParticipants {
			nodeIsInList = false
			for _, subnetValidator := range participatingNodeNames {
				if subnetValidator == node {
					nodeIsInList = true
					break
				}
			}
			if !nodeIsInList {
				ux.Print(log, logging.Red.Wrap(fmt.Sprintf("VerifySubnetHasCorrectParticipants: %#v", cluster)))
				ux.Print(log, logging.Red.Wrap(fmt.Sprintf("VerifySubnetHasCorrectParticipants: node not in list subnet %q node %q %v %v", subnetID, node, subnetParticipants, participatingNodeNames)))
				return false
			}
		}
		return true
	} else {
		ux.Print(log, logging.Red.Wrap("VerifySubnetHasCorrectParticipants: cluster is nil"))
	}
	return false
}
