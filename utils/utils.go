package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/staking"
)

const genesisNetworkIDKey = "networkID"

func ToNodeID(stakingKey, stakingCert []byte) (ids.ShortID, error) {
	cert, err := staking.LoadTLSCertFromBytes(stakingKey, stakingCert)
	if err != nil {
		return ids.ShortID{}, err
	}
	nodeID := peer.CertToID(cert.Leaf)
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

func CheckExecPluginPaths(exec string, pluginExec string, pluginGenesisPath string) error {
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

	// no custom VM is specified
	// no need to check further
	// (subnet installation is optional)
	if pluginExec == "" {
		return nil
	}

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

func GetFreePorts(numPorts int) ([]int, error) {
	ports := make([]int, 0, numPorts)
	for i := 0; i < numPorts; i++ {
		listener, err := net.Listen("tcp", ":0")
		if err != nil {
			return nil, err
		}
		// Defer closing the listener until the end of the function
		// so that we don't accidentally allocate the same port twice.
		defer listener.Close()
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}

	return ports, nil
}
