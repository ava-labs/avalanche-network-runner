package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	dircopy "github.com/otiai10/copy"
	"golang.org/x/exp/maps"
)

const (
	deprecatedBuildDirKey           = "build-dir"
	deprecatedWhitelistedSubnetsKey = "whitelisted-subnets"
)

// NetworkState defines dynamic network information not available on blockchain db
type NetworkState struct {
	// Map from subnet id to elastic subnet tx id
	SubnetID2ElasticSubnetID map[string]string `json:"subnetID2ElasticSubnetID"`
	// Map from blockchain id to blockchain aliases
	BlockchainAliases map[string][]string `json:"blockchainAliases"`
}

// snapshots generated using older ANR versions may contain deprecated avago flags
func fixDeprecatedAvagoFlags(flags map[string]interface{}) error {
	if vIntf, ok := flags[deprecatedWhitelistedSubnetsKey]; ok {
		v, ok := vIntf.(string)
		if !ok {
			return fmt.Errorf("expected %q to be of type string but got %T", deprecatedWhitelistedSubnetsKey, vIntf)
		}
		if v != "" {
			flags[config.TrackSubnetsKey] = v
		}
		delete(flags, deprecatedWhitelistedSubnetsKey)
	}
	if vIntf, ok := flags[deprecatedBuildDirKey]; ok {
		v, ok := vIntf.(string)
		if !ok {
			return fmt.Errorf("expected %q to be of type string but got %T", deprecatedBuildDirKey, vIntf)
		}
		if v != "" {
			flags[config.PluginDirKey] = filepath.Join(v, "plugins")
		}
		delete(flags, deprecatedBuildDirKey)
	}
	return nil
}

// NewNetwork returns a new network from the given snapshot
func NewNetworkFromSnapshot(
	log logging.Logger,
	snapshotName string,
	rootDir string,
	logRootDir string,
	snapshotsDir string,
	binaryPath string,
	pluginDir string,
	chainConfigs map[string]string,
	upgradeConfigs map[string]string,
	subnetConfigs map[string]string,
	flags map[string]interface{},
	reassignPortsIfUsed bool,
	redirectStdout bool,
	redirectStderr bool,
	inPlace bool,
	walletPrivateKey string,
) (network.Network, error) {
	if inPlace {
		if rootDir != "" {
			return nil, fmt.Errorf("root dir must be empty when using in place snapshot load")
		}
		rootDir = getSnapshotDir(snapshotsDir, snapshotName)
	}
	net, err := newNetwork(
		log,
		api.NewAPIClient,
		&nodeProcessCreator{
			colorPicker: utils.NewColorPicker(),
			log:         log,
			stdout:      os.Stdout,
			stderr:      os.Stderr,
		},
		rootDir,
		logRootDir,
		snapshotsDir,
		reassignPortsIfUsed,
		redirectStdout,
		redirectStderr,
		walletPrivateKey,
	)
	if err != nil {
		return net, err
	}
	err = net.loadSnapshot(
		context.Background(),
		snapshotName,
		binaryPath,
		pluginDir,
		chainConfigs,
		upgradeConfigs,
		subnetConfigs,
		flags,
		inPlace,
	)
	return net, err
}

// Save network conf + state into json at root dir
func (ln *localNetwork) persistNetwork() error {
	// clone network flags
	networkConfigFlags := maps.Clone(ln.flags)
	// remove data dir, log dir references
	delete(networkConfigFlags, config.DataDirKey)
	delete(networkConfigFlags, config.LogsDirKey)
	// clone node info
	nodeConfigs := []node.Config{}
	for nodeName, node := range ln.nodes {
		nodeConfig := node.config
		// depending on how the user generated the config, different nodes config flags
		// may point to the same map, so we made a copy to avoid always modifying the same value
		nodeConfig.Flags = maps.Clone(nodeConfig.Flags)
		// preserve the current node ports
		nodeConfig.Flags[config.HTTPPortKey] = ln.nodes[nodeName].GetAPIPort()
		nodeConfig.Flags[config.StakingPortKey] = ln.nodes[nodeName].GetP2PPort()
		// remove data dir, log dir references
		if nodeConfig.ConfigFile != "" {
			var err error
			nodeConfig.ConfigFile, err = utils.SetJSONKey(nodeConfig.ConfigFile, config.LogsDirKey, "")
			if err != nil {
				return err
			}
			nodeConfig.ConfigFile, err = utils.SetJSONKey(nodeConfig.ConfigFile, config.DataDirKey, "")
			if err != nil {
				return err
			}
		}
		delete(nodeConfig.Flags, config.LogsDirKey)
		delete(nodeConfig.Flags, config.DataDirKey)
		nodeConfigs = append(nodeConfigs, nodeConfig)
	}
	// save network conf
	networkConfig := network.Config{
		Genesis:            string(ln.genesis),
		Flags:              networkConfigFlags,
		NodeConfigs:        nodeConfigs,
		BinaryPath:         ln.binaryPath,
		ChainConfigFiles:   ln.chainConfigFiles,
		UpgradeConfigFiles: ln.upgradeConfigFiles,
		SubnetConfigFiles:  ln.subnetConfigFiles,
	}
	networkConfigJSON, err := json.MarshalIndent(networkConfig, "", "    ")
	if err != nil {
		return err
	}
	if err := createFileAndWrite(filepath.Join(ln.rootDir, "network.json"), networkConfigJSON); err != nil {
		return err
	}
	// save dynamic part of network not available on blockchain
	subnetID2ElasticSubnetID := map[string]string{}
	for subnetID, elasticSubnetID := range ln.subnetID2ElasticSubnetID {
		subnetID2ElasticSubnetID[subnetID.String()] = elasticSubnetID.String()
	}
	networkState := NetworkState{
		SubnetID2ElasticSubnetID: subnetID2ElasticSubnetID,
		BlockchainAliases:        ln.blockchainAliases,
	}
	networkStateJSON, err := json.MarshalIndent(networkState, "", "    ")
	if err != nil {
		return err
	}
	return createFileAndWrite(filepath.Join(ln.rootDir, "state.json"), networkStateJSON)
}

// Save network snapshot
// Network is stopped in order to do a safe preservation
func (ln *localNetwork) SaveSnapshot(ctx context.Context, snapshotName string, force bool) (string, error) {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	if ln.stopCalled() {
		return "", network.ErrStopped
	}
	if len(snapshotName) == 0 {
		return "", fmt.Errorf("invalid snapshotName %q", snapshotName)
	}
	// check if snapshot already exists
	snapshotDir := getSnapshotDir(ln.snapshotsDir, snapshotName)
	exists := false
	if _, err := os.Stat(snapshotDir); err == nil {
		exists = true
	}
	// check is network was loaded in place from the given snapshot
	if ln.rootDir == snapshotDir {
		return "", fmt.Errorf("already auto saving into the specified snapshot %q", snapshotName)
	}
	if !force && exists {
		return "", fmt.Errorf("snapshot %q already exists", snapshotName)
	}
	if err := ln.persistNetwork(); err != nil {
		return "", err
	}
	// stop network to safely save snapshot
	if err := ln.stop(ctx); err != nil {
		return "", err
	}
	// remove if force save
	if force && exists {
		if err := ln.RemoveSnapshot(snapshotName); err != nil {
			return "", err
		}
	}
	// copy all info
	if err := dircopy.Copy(ln.rootDir, snapshotDir); err != nil {
		return "", fmt.Errorf("failure saving data dir %s: %w", ln.rootDir, err)
	}
	return snapshotDir, nil
}

// start network from snapshot
func (ln *localNetwork) loadSnapshot(
	ctx context.Context,
	snapshotName string,
	binaryPath string,
	pluginDir string,
	chainConfigs map[string]string,
	upgradeConfigs map[string]string,
	subnetConfigs map[string]string,
	flags map[string]interface{},
	inPlace bool,
) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	snapshotDir := getSnapshotDir(ln.snapshotsDir, snapshotName)
	_, err := os.Stat(snapshotDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSnapshotNotFound
		} else {
			return fmt.Errorf("failure accessing snapshot %q: %w", snapshotName, err)
		}
	}
	// load network config
	networkConfigJSON, err := os.ReadFile(filepath.Join(snapshotDir, "network.json"))
	if err != nil {
		return fmt.Errorf("failure reading network config file from snapshot: %w", err)
	}
	networkConfig := network.Config{}
	if err := json.Unmarshal(networkConfigJSON, &networkConfig); err != nil {
		return fmt.Errorf("failure unmarshaling network config from snapshot: %w", err)
	}
	// fix deprecated avago flags
	if err := fixDeprecatedAvagoFlags(networkConfig.Flags); err != nil {
		return err
	}
	for i := range networkConfig.NodeConfigs {
		if err := fixDeprecatedAvagoFlags(networkConfig.NodeConfigs[i].Flags); err != nil {
			return err
		}
	}
	// add flags
	for i := range networkConfig.NodeConfigs {
		for k, v := range flags {
			networkConfig.NodeConfigs[i].Flags[k] = v
		}
	}
	// auto migrate v0 to v1
	v0SnapshotDBDir := filepath.Join(snapshotDir, defaultDBSubdir)
	if isV0, err := utils.PathExists(v0SnapshotDBDir); err != nil {
		return err
	} else if isV0 {
		for _, nodeConfig := range networkConfig.NodeConfigs {
			sourceDBDir := filepath.Join(v0SnapshotDBDir, nodeConfig.Name)
			targetDataDir := filepath.Join(snapshotDir, nodeConfig.Name)
			if err := os.MkdirAll(targetDataDir, os.ModePerm); err != nil {
				return err
			}
			targetDBDir := filepath.Join(targetDataDir, defaultDBSubdir)
			if err := os.Rename(sourceDBDir, targetDBDir); err != nil {
				return err
			}
		}
		if err := os.RemoveAll(v0SnapshotDBDir); err != nil {
			return err
		}
	}
	// load snapshot dir
	if !inPlace {
		if snapshotDir == ln.rootDir {
			return fmt.Errorf("root dir should differ from snapshot dir for not in place load")
		}
		if err := dircopy.Copy(snapshotDir, ln.rootDir); err != nil {
			return fmt.Errorf("failure loading snapshot root dir: %w", err)
		}
	} else if snapshotDir != ln.rootDir {
		return fmt.Errorf("root dir should equal snapshot dir for not in place load")
	}
	// configure each node data dir
	for _, nodeConfig := range networkConfig.NodeConfigs {
		delete(nodeConfig.Flags, config.DBPathKey)
		nodeConfig.Flags[config.DataDirKey] = filepath.Join(ln.rootDir, nodeConfig.Name)
	}
	// replace binary path
	if binaryPath != "" {
		for i := range networkConfig.NodeConfigs {
			networkConfig.NodeConfigs[i].BinaryPath = binaryPath
		}
	}
	// replace plugin dir
	if pluginDir != "" {
		for i := range networkConfig.NodeConfigs {
			networkConfig.NodeConfigs[i].Flags[config.PluginDirKey] = pluginDir
		}
	}
	// add chain configs and upgrade configs
	for i := range networkConfig.NodeConfigs {
		if networkConfig.NodeConfigs[i].ChainConfigFiles == nil {
			networkConfig.NodeConfigs[i].ChainConfigFiles = map[string]string{}
		}
		if networkConfig.NodeConfigs[i].UpgradeConfigFiles == nil {
			networkConfig.NodeConfigs[i].UpgradeConfigFiles = map[string]string{}
		}
		if networkConfig.NodeConfigs[i].SubnetConfigFiles == nil {
			networkConfig.NodeConfigs[i].SubnetConfigFiles = map[string]string{}
		}
		for k, v := range chainConfigs {
			networkConfig.NodeConfigs[i].ChainConfigFiles[k] = v
		}
		for k, v := range upgradeConfigs {
			networkConfig.NodeConfigs[i].UpgradeConfigFiles[k] = v
		}
		for k, v := range subnetConfigs {
			networkConfig.NodeConfigs[i].SubnetConfigFiles[k] = v
		}
	}
	// load network state not available at blockchain db
	networkStateJSON, err := os.ReadFile(filepath.Join(snapshotDir, "state.json"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failure reading network state file from snapshot: %w", err)
		}
		ln.log.Warn("network state file not found on snapshot")
	} else {
		networkState := NetworkState{}
		if err := json.Unmarshal(networkStateJSON, &networkState); err != nil {
			return fmt.Errorf("failure unmarshaling network state from snapshot: %w", err)
		}
		ln.subnetID2ElasticSubnetID = map[ids.ID]ids.ID{}
		for subnetIDStr, elasticSubnetIDStr := range networkState.SubnetID2ElasticSubnetID {
			subnetID, err := ids.FromString(subnetIDStr)
			if err != nil {
				return err
			}
			elasticSubnetID, err := ids.FromString(elasticSubnetIDStr)
			if err != nil {
				return err
			}
			ln.subnetID2ElasticSubnetID[subnetID] = elasticSubnetID
		}
		for k, v := range networkState.BlockchainAliases {
			ln.blockchainAliases[k] = v
		}
	}
	if err := ln.loadConfig(ctx, networkConfig); err != nil {
		return err
	}
	if err := ln.healthy(ctx); err != nil {
		return err
	}
	// add aliases included in the snapshot state
	for blockchainID, blockchainAliases := range ln.blockchainAliases {
		for _, blockchainAlias := range blockchainAliases {
			if err := ln.setBlockchainAlias(ctx, blockchainID, blockchainAlias); err != nil {
				return err
			}
		}
	}
	// add aliases for blockchain names
	node := ln.getNode()
	blockchains, err := node.GetAPIClient().PChainAPI().GetBlockchains(ctx)
	if err != nil {
		return err
	}
	for _, blockchain := range blockchains {
		if blockchain.Name == "C-Chain" || blockchain.Name == "X-Chain" {
			continue
		}
		if err := ln.setBlockchainAlias(ctx, blockchain.ID.String(), blockchain.Name); err != nil {
			// non fatal error: not required by user
			ln.log.Warn(err.Error())
		}
	}
	return ln.persistNetwork()
}

// Remove network snapshot
func (ln *localNetwork) RemoveSnapshot(snapshotName string) error {
	return RemoveSnapshot(ln.snapshotsDir, snapshotName)
}

// Get network snapshots
func (ln *localNetwork) GetSnapshotNames() ([]string, error) {
	return GetSnapshotNames(ln.snapshotsDir)
}

func getSnapshotDir(snapshotsDir string, snapshotName string) string {
	if snapshotsDir == "" {
		snapshotsDir = DefaultSnapshotsDir
	}
	return filepath.Join(snapshotsDir, snapshotPrefix+snapshotName)
}

func RemoveSnapshot(snapshotsDir string, snapshotName string) error {
	snapshotDir := getSnapshotDir(snapshotsDir, snapshotName)
	_, err := os.Stat(snapshotDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSnapshotNotFound
		} else {
			return fmt.Errorf("failure accessing snapshot %q: %w", snapshotName, err)
		}
	}
	if err := os.RemoveAll(snapshotDir); err != nil {
		return fmt.Errorf("failure removing snapshot path %q: %w", snapshotDir, err)
	}
	return nil
}

func GetSnapshotNames(snapshotsDir string) ([]string, error) {
	_, err := os.Stat(snapshotsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("snapshots dir %q does not exists", snapshotsDir)
		} else {
			return nil, fmt.Errorf("failure accessing snapshots dir %q: %w", snapshotsDir, err)
		}
	}
	matches, err := filepath.Glob(filepath.Join(snapshotsDir, snapshotPrefix+"*"))
	if err != nil {
		return nil, err
	}
	snapshots := []string{}
	for _, match := range matches {
		snapshots = append(snapshots, strings.TrimPrefix(filepath.Base(match), snapshotPrefix))
	}
	return snapshots, nil
}
