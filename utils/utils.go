package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	rpcb "github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"golang.org/x/net/context"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/staking"
)

const (
	genesisNetworkIDKey = "networkID"
	dirTimestampFormat  = "20060102_150405"
)

func ToNodeID(stakingKey, stakingCert []byte) (ids.NodeID, error) {
	cert, err := staking.LoadTLSCertFromBytes(stakingKey, stakingCert)
	if err != nil {
		return ids.EmptyNodeID, err
	}
	nodeID := ids.NodeIDFromCert(cert.Leaf)
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
	ErrInvalidExecPath        = errors.New("avalanche exec is invalid")
	ErrNotExists              = errors.New("avalanche exec not exists")
	ErrNotExistsPlugin        = errors.New("plugin exec not exists")
	ErrNotExistsPluginGenesis = errors.New("plugin genesis not exists")
)

func CheckExecPath(exec string) error {
	if exec == "" {
		return ErrInvalidExecPath
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

func CheckPluginPaths(pluginExec string, pluginGenesisPath string) error {
	var err error
	if _, err = os.Stat(pluginExec); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotExistsPlugin
		}
		return fmt.Errorf("failed to stat plugin exec %q (%w)", pluginExec, err)
	}
	if _, err = os.Stat(pluginGenesisPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotExistsPluginGenesis
		}
		return fmt.Errorf("failed to stat plugin genesis %q (%w)", pluginGenesisPath, err)
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
	ctx context.Context,
	clientURI string,
	createdSubnetID ids.ID,
	cluster *rpcb.ClusterInfo,
	subnetID string,
) bool {
	if cluster != nil {
		nodeIdsList := cluster.SubnetParticipants[subnetID].GetSubnetParticipants()
		platformCli := platformvm.NewClient(clientURI)
		vdrs, err := platformCli.GetCurrentValidators(ctx, createdSubnetID, nil)
		if err != nil {
			return false
		}
		var nodeIsInList bool
		// Check that all subnet validators are equal to the node IDs added as participant in subnet creation
		for _, v := range vdrs {
			nodeIsInList = false
			for _, subnetValidator := range nodeIdsList {
				if v.NodeID.String() == subnetValidator.Id {
					nodeIsInList = true
					break
				}
			}
			if !nodeIsInList {
				return false
			}
		}
		return true
	}
	return false
}
