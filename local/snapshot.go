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
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	dircopy "github.com/otiai10/copy"
)

// NewNetwork returns a new network from the given snapshot
func NewNetworkFromSnapshot(
	log logging.Logger,
	snapshotName string,
	rootDir string,
	snapshotsDir string,
	binaryPath string,
	buildDir string,
	chainConfigs map[string]string,
	upgradeConfigs map[string]string,
	subnetConfigs map[string]string,
	flags map[string]interface{},
	reassignPortsIfUsed bool,
) (network.Network, error) {
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
		snapshotsDir,
		reassignPortsIfUsed,
	)
	if err != nil {
		return net, err
	}
	err = net.loadSnapshot(
		context.Background(),
		snapshotName,
		binaryPath,
		buildDir,
		chainConfigs,
		upgradeConfigs,
		subnetConfigs,
		flags,
	)
	return net, err
}

// Save network snapshot
// Network is stopped in order to do a safe preservation
func (ln *localNetwork) SaveSnapshot(ctx context.Context, snapshotName string) (string, error) {
	ln.lock.Lock()
	defer ln.lock.Unlock()
	if ln.stopCalled() {
		return "", network.ErrStopped
	}
	if len(snapshotName) == 0 {
		return "", fmt.Errorf("invalid snapshotName %q", snapshotName)
	}
	// check if snapshot already exists
	snapshotDir := filepath.Join(ln.snapshotsDir, snapshotPrefix+snapshotName)
	_, err := os.Stat(snapshotDir)
	if err == nil {
		return "", fmt.Errorf("snapshot %q already exists", snapshotName)
	}
	// keep copy of node info that will be removed by stop
	nodesConfig := map[string]node.Config{}
	nodesDBDir := map[string]string{}
	for nodeName, node := range ln.nodes {
		nodeConfig := node.config
		// depending on how the user generated the config, different nodes config flags
		// may point to the same map, so we made a copy to avoid always modifying the same value
		nodeConfigFlags := make(map[string]interface{})
		for fk, fv := range nodeConfig.Flags {
			nodeConfigFlags[fk] = fv
		}
		nodeConfig.Flags = nodeConfigFlags
		nodesConfig[nodeName] = nodeConfig
		nodesDBDir[nodeName] = node.GetDbDir()
	}
	// we change nodeConfig.Flags so as to preserve in snapshot the current node ports
	for nodeName, nodeConfig := range nodesConfig {
		nodeConfig.Flags[config.HTTPPortKey] = ln.nodes[nodeName].GetAPIPort()
		nodeConfig.Flags[config.StakingPortKey] = ln.nodes[nodeName].GetP2PPort()
	}
	// make copy of network flags
	networkConfigFlags := make(map[string]interface{})
	for fk, fv := range ln.flags {
		networkConfigFlags[fk] = fv
	}
	// remove all log dir references
	delete(networkConfigFlags, config.LogsDirKey)
	for nodeName, nodeConfig := range nodesConfig {
		if nodeConfig.ConfigFile != "" {
			nodeConfig.ConfigFile, err = utils.SetJSONKey(nodeConfig.ConfigFile, config.LogsDirKey, "")
			if err != nil {
				return "", err
			}
		}
		delete(nodeConfig.Flags, config.LogsDirKey)
		nodesConfig[nodeName] = nodeConfig
	}

	// stop network to safely save snapshot
	if err := ln.stop(ctx); err != nil {
		return "", err
	}
	// create main snapshot dirs
	snapshotDBDir := filepath.Join(snapshotDir, defaultDBSubdir)
	err = os.MkdirAll(snapshotDBDir, os.ModePerm)
	if err != nil {
		return "", err
	}
	// save db
	for _, nodeConfig := range nodesConfig {
		sourceDBDir, ok := nodesDBDir[nodeConfig.Name]
		if !ok {
			return "", fmt.Errorf("failure obtaining db path for node %q", nodeConfig.Name)
		}
		sourceDBDir = filepath.Join(sourceDBDir, constants.NetworkName(ln.networkID))
		targetDBDir := filepath.Join(filepath.Join(snapshotDBDir, nodeConfig.Name), constants.NetworkName(ln.networkID))
		if err := dircopy.Copy(sourceDBDir, targetDBDir); err != nil {
			return "", fmt.Errorf("failure saving node %q db dir: %w", nodeConfig.Name, err)
		}
	}
	// save network conf
	networkConfig := network.Config{
		Genesis:            string(ln.genesis),
		Flags:              networkConfigFlags,
		NodeConfigs:        []node.Config{},
		BinaryPath:         ln.binaryPath,
		ChainConfigFiles:   ln.chainConfigFiles,
		UpgradeConfigFiles: ln.upgradeConfigFiles,
		SubnetConfigFiles:  ln.subnetConfigFiles,
	}

	for _, nodeConfig := range nodesConfig {
		// no need to save this, will be generated automatically on snapshot load
		networkConfig.NodeConfigs = append(networkConfig.NodeConfigs, nodeConfig)
	}
	networkConfigJSON, err := json.MarshalIndent(networkConfig, "", "    ")
	if err != nil {
		return "", err
	}
	err = createFileAndWrite(filepath.Join(snapshotDir, "network.json"), networkConfigJSON)
	if err != nil {
		return "", err
	}
	return snapshotDir, nil
}

// start network from snapshot
func (ln *localNetwork) loadSnapshot(
	ctx context.Context,
	snapshotName string,
	binaryPath string,
	buildDir string,
	chainConfigs map[string]string,
	upgradeConfigs map[string]string,
	subnetConfigs map[string]string,
	flags map[string]interface{},
) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()
	snapshotDir := filepath.Join(ln.snapshotsDir, snapshotPrefix+snapshotName)
	snapshotDBDir := filepath.Join(snapshotDir, defaultDBSubdir)
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
	err = json.Unmarshal(networkConfigJSON, &networkConfig)
	if err != nil {
		return fmt.Errorf("failure unmarshaling network config from snapshot: %w", err)
	}
	// add flags
	for i := range networkConfig.NodeConfigs {
		for k, v := range flags {
			networkConfig.NodeConfigs[i].Flags[k] = v
		}
	}
	// load db
	for _, nodeConfig := range networkConfig.NodeConfigs {
		sourceDBDir := filepath.Join(snapshotDBDir, nodeConfig.Name)
		targetDBDir := filepath.Join(filepath.Join(ln.rootDir, nodeConfig.Name), defaultDBSubdir)
		if err := dircopy.Copy(sourceDBDir, targetDBDir); err != nil {
			return fmt.Errorf("failure loading node %q db dir: %w", nodeConfig.Name, err)
		}
		nodeConfig.Flags[config.DBPathKey] = targetDBDir
	}
	// replace binary path
	if binaryPath != "" {
		for i := range networkConfig.NodeConfigs {
			networkConfig.NodeConfigs[i].BinaryPath = binaryPath
		}
	}
	// replace build dir
	if buildDir != "" {
		for i := range networkConfig.NodeConfigs {
			networkConfig.NodeConfigs[i].Flags[config.BuildDirKey] = buildDir
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
	return ln.loadConfig(ctx, networkConfig)
}

// Remove network snapshot
func (ln *localNetwork) RemoveSnapshot(snapshotName string) error {
	snapshotDir := filepath.Join(ln.snapshotsDir, snapshotPrefix+snapshotName)
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

// Get network snapshots
func (ln *localNetwork) GetSnapshotNames() ([]string, error) {
	_, err := os.Stat(ln.snapshotsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("snapshots dir %q does not exists", ln.snapshotsDir)
		} else {
			return nil, fmt.Errorf("failure accessing snapshots dir %q: %w", ln.snapshotsDir, err)
		}
	}
	matches, err := filepath.Glob(filepath.Join(ln.snapshotsDir, snapshotPrefix+"*"))
	if err != nil {
		return nil, err
	}
	snapshots := []string{}
	for _, match := range matches {
		snapshots = append(snapshots, strings.TrimPrefix(filepath.Base(match), snapshotPrefix))
	}
	return snapshots, nil
}
