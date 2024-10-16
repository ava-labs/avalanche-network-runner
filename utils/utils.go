package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"strings"
	"time"

	rpcb "github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/ux"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/beacon"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	genesisNetworkIDKey = "networkID"
	dirTimestampFormat  = "20060102_150405"
	dockerEnvPath       = "/.dockerenv"
)

var (
	ErrEmptyExecPath    = errors.New("the path to the avalanchego executable is not defined. please make sure to either set the AVALANCHEGO_EXEC_PATH environment variable or pass the path with the --avalanchego-path flag")
	ErrNotExists        = errors.New("the avalanchego executable does not exists at the provided path")
	ErrNotExistsPlugin  = errors.New("the vm plugin does not exists at the provided path. please check if the plugin path is set correctly in the AVALANCHEGO_PLUGIN_PATH environment variable or the --plugin-dir flag and if the plugin binary is located there")
	ErrorNoNetworkIDKey = fmt.Errorf("couldn't find key %q in genesis", genesisNetworkIDKey)
)

func ToNodeID(stakingKey, stakingCert []byte) (ids.NodeID, error) {
	tlsCert, err := staking.LoadTLSCertFromBytes(stakingKey, stakingCert)
	if err != nil {
		return ids.EmptyNodeID, err
	}
	cert, err := staking.ParseCertificate(tlsCert.Leaf.Raw)
	if err != nil {
		return ids.EmptyNodeID, err
	}
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

func SetGenesisNetworkID(genesis []byte, networkID uint32) ([]byte, error) {
	genesisMap := map[string]interface{}{}
	if err := json.Unmarshal(genesis, &genesisMap); err != nil {
		return nil, fmt.Errorf("couldn't unmarshal genesis: %w", err)
	}
	genesisMap[genesisNetworkIDKey] = networkID
	var err error
	genesis, err = json.MarshalIndent(genesisMap, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal genesis: %w", err)
	}
	return genesis, nil
}

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

func DirnameWithTimestamp(dirPrefix string) string {
	currentTime := time.Now().Format(dirTimestampFormat)
	return dirPrefix + "_" + currentTime
}

func MkDirWithTimestamp(dirPrefix string) (string, error) {
	dirName := DirnameWithTimestamp(dirPrefix)
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

func IsInsideDockerContainer() (bool, error) {
	return PathExists(dockerEnvPath)
}

func PathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// FileExists checks if a file exists.
func FileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

func WaitForFile(
	filename string,
	timeout time.Duration,
	checkInterval time.Duration,
	description string,
) error {
	t0 := time.Now()
	for {
		if FileExists(filename) {
			break
		}
		elapsed := time.Since(t0)
		if elapsed > timeout {
			return fmt.Errorf(
				"%s within %.0f seconds",
				description,
				timeout.Seconds(),
			)
		}
		time.Sleep(checkInterval)
	}
	return nil
}

func IsPublicNetwork(networkID uint32) bool {
	return networkID == constants.FujiID || networkID == constants.MainnetID
}

func IsCustomNetwork(networkID uint32) bool {
	return !IsPublicNetwork(networkID) && networkID != constants.LocalID
}

func BeaconMapToSet(beaconMap map[ids.NodeID]netip.AddrPort) (beacon.Set, error) {
	set := beacon.NewSet()
	for id, addr := range beaconMap {
		if err := set.Add(beacon.New(id, addr)); err != nil {
			return nil, fmt.Errorf("failed to add beacon to set: %w", err)
		}
	}
	return set, nil
}

func BeaconMapFromSet(beaconSet beacon.Set) (map[ids.NodeID]netip.AddrPort, error) {
	beaconMap := make(map[ids.NodeID]netip.AddrPort)
	if beaconSet.Len() == 0 {
		return beaconMap, nil
	}
	beaconIDs := strings.Split(beaconSet.IDsArg(), ",")
	beaconIPs := strings.Split(beaconSet.IPsArg(), ",")

	if len(beaconIDs) != len(beaconIPs) {
		return nil, fmt.Errorf("beacon IDs and IPs do not match")
	}

	for i := 0; i < len(beaconIDs); i++ {
		beaconID, err := ids.NodeIDFromString(beaconIDs[i])
		if err != nil {
			return nil, fmt.Errorf("failed to parse beacon ID: %w", err)
		}
		beaconIP, err := netip.ParseAddrPort(beaconIPs[i])
		if err != nil {
			return nil, fmt.Errorf("failed to parse beacon IP: %w", err)
		}
		beaconMap[beaconID] = beaconIP
	}
	return beaconMap, nil
}
