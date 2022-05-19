// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/pkg/color"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"go.uber.org/zap"
)

const (
	defaultNodeConfig = `{
		"network-peer-list-gossip-frequency":"250ms",
		"network-max-reconnect-delay":"1s",
		"public-ip":"127.0.0.1",
		"health-check-frequency":"2s",
		"api-admin-enabled":true,
		"api-ipcs-enabled":true,
		"index-enabled":true
  }`
)

var ignoreFields = map[string]struct{}{
	"public-ip":    {},
	"http-port":    {},
	"staking-port": {},
}

type localNetwork struct {
	logger logging.Logger

	binPath string
	cfg     network.Config

	nw network.Network

	// NOTE: Naming convention for node names is currently `node` + number, i.e. `node1,node2,node3,...node101`
	nodeNames []string
	nodeInfos map[string]*rpcpb.NodeInfo

	options localNetworkOptions

	// maps from node name to peer ID to peer object
	attachedPeers map[string]map[string]peer.Peer

	localClusterReadyc          chan struct{} // closed when local network is ready/healthy
	localClusterReadycCloseOnce sync.Once

	// map from VM name to genesis bytes
	customVMNameToGenesis map[string][]byte
	// map from VM ID to VM info
	customVMIDToInfo map[ids.ID]vmInfo

	customVMsReadyc          chan struct{} // closed when subnet installations are complete
	customVMsReadycCloseOnce sync.Once
	customVMRestartMu        *sync.RWMutex

	stopc      chan struct{}
	startDonec chan struct{}
	startErrc  chan error

	stopOnce sync.Once
}

type vmInfo struct {
	info         *rpcpb.CustomVmInfo
	subnetID     ids.ID
	blockchainID ids.ID
}

type localNetworkOptions struct {
	execPath            string
	rootDataDir         string
	numNodes            uint32
	whitelistedSubnets  string
	redirectNodesOutput bool
	globalNodeConfig    string

	pluginDir         string
	customVMs         map[string][]byte
	customNodeConfigs map[string]string

	// to block racey restart while installing custom VMs
	restartMu *sync.RWMutex

	snapshotsDir string
}

func newLocalNetwork(opts localNetworkOptions) (*localNetwork, error) {
	lcfg := logging.DefaultConfig
	lcfg.Directory = opts.rootDataDir
	logFactory := logging.NewFactory(lcfg)
	logger, err := logFactory.Make("main")
	if err != nil {
		return nil, err
	}

	return &localNetwork{
		logger: logger,

		binPath: opts.execPath,

		options: opts,

		attachedPeers: make(map[string]map[string]peer.Peer),

		localClusterReadyc: make(chan struct{}),

		customVMNameToGenesis: opts.customVMs,
		customVMIDToInfo:      make(map[ids.ID]vmInfo),
		customVMsReadyc:       make(chan struct{}),
		customVMRestartMu:     opts.restartMu,

		stopc:      make(chan struct{}),
		startDonec: make(chan struct{}),
		startErrc:  make(chan error, 1),

		nodeInfos: make(map[string]*rpcpb.NodeInfo),
		nodeNames: []string{},
	}, nil
}

func (lc *localNetwork) createConfig() error {
	cfg, err := local.NewDefaultConfigNNodes(lc.options.execPath, lc.options.numNodes)
	if err != nil {
		return err
	}

	var defaultConfig, globalConfig map[string]interface{}
	if err := json.Unmarshal([]byte(defaultNodeConfig), &defaultConfig); err != nil {
		return err
	}

	if lc.options.globalNodeConfig != "" {
		if err := json.Unmarshal([]byte(lc.options.globalNodeConfig), &globalConfig); err != nil {
			return err
		}
	}

	for i := range cfg.NodeConfigs {
		// NOTE: Naming convention for node names is currently `node` + number, i.e. `node1,node2,node3,...node101`
		nodeName := fmt.Sprintf("node%d", i+1)
		logDir := filepath.Join(lc.options.rootDataDir, nodeName, "log")
		dbDir := filepath.Join(lc.options.rootDataDir, nodeName, "db-dir")

		lc.nodeNames = append(lc.nodeNames, nodeName)
		cfg.NodeConfigs[i].Name = nodeName

		mergedConfig, err := mergeNodeConfig(defaultConfig, globalConfig, lc.options.customNodeConfigs[nodeName])
		if err != nil {
			return fmt.Errorf("failed merging provided configs: %w", err)
		}

		buildDir := ""
		if lc.options.pluginDir != "" {
			pluginDir := filepath.Clean(lc.options.pluginDir)
			if filepath.Base(pluginDir) != "plugins" {
				return fmt.Errorf("plugin dir %q is not named plugins", pluginDir)
			}
			buildDir = filepath.Dir(pluginDir)
		}

		cfg.NodeConfigs[i].ConfigFile, err = createConfigFileString(mergedConfig, logDir, dbDir, buildDir, lc.options.whitelistedSubnets)
		if err != nil {
			return err
		}

		cfg.NodeConfigs[i].BinaryPath = lc.options.execPath
		cfg.NodeConfigs[i].RedirectStdout = lc.options.redirectNodesOutput
		cfg.NodeConfigs[i].RedirectStderr = lc.options.redirectNodesOutput
	}

	lc.cfg = cfg
	return nil
}

// mergeAndCheckForIgnores takes two maps, merging the two and overriding the first with the second
// if common entries are found.
// It also skips some entries which are internal to the runner
func mergeAndCheckForIgnores(base, override map[string]interface{}) {
	for k, v := range override {
		if _, ok := ignoreFields[k]; ok {
			continue
		}
		base[k] = v
	}
}

// mergeNodeConfig evaluates the final node config.
// defaultConfig: map of base config to be applied
// globalConfig: map of global config provided to be applied to all nodes. Overrides defaultConfig
// customConfig: a custom config provided to be applied to this node. Overrides globalConfig and defaultConfig
// returns final map of node config entries
func mergeNodeConfig(baseConfig map[string]interface{}, globalConfig map[string]interface{}, customConfig string) (map[string]interface{}, error) {
	mergeAndCheckForIgnores(baseConfig, globalConfig)

	var jsonCustom map[string]interface{}
	// merge, overwriting entries in default with the global ones
	if customConfig != "" {
		if err := json.Unmarshal([]byte(customConfig), &jsonCustom); err != nil {
			return nil, err
		}
		// merge, overwriting entries in default with the custom ones
		mergeAndCheckForIgnores(baseConfig, jsonCustom)
	}

	return baseConfig, nil
}

// createConfigFileString finalizes the config setup and returns the node config JSON string
func createConfigFileString(configFileMap map[string]interface{}, logDir string, dbDir string, buildDir string, whitelistedSubnets string) (string, error) {
	// add (or overwrite, if given) the following entries
	if configFileMap[config.LogsDirKey] != "" {
		zap.L().Warn("ignoring config file entry provided; the network runner needs to set its own", zap.String("entry", config.LogsDirKey))
	}
	configFileMap[config.LogsDirKey] = logDir
	if configFileMap[config.DBPathKey] != "" {
		zap.L().Warn("ignoring config file entry provided; the network runner needs to set its own", zap.String("entry", config.DBPathKey))
	}
	configFileMap[config.DBPathKey] = dbDir
	if buildDir != "" {
		configFileMap[config.BuildDirKey] = buildDir
	}
	// need to whitelist subnet ID to create custom VM chain
	// ref. vms/platformvm/createChain
	if whitelistedSubnets != "" {
		configFileMap[config.WhitelistedSubnetsKey] = whitelistedSubnets
	}

	finalJSON, err := json.Marshal(configFileMap)
	if err != nil {
		return "", err
	}
	return string(finalJSON), nil
}

func (lc *localNetwork) start(ctx context.Context) {
	defer func() {
		close(lc.startDonec)
	}()

	color.Outf("{{blue}}{{bold}}create and run local network{{/}}\n")
	nw, err := local.NewNetwork(lc.logger, os.TempDir(), lc.options.snapshotsDir)
	if err != nil {
		lc.startErrc <- err
		return
	}
	err = nw.LoadConfig(ctx, lc.cfg)
	if err != nil {
		lc.startErrc <- err
		return
	}
	lc.nw = nw

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrc <- err
		return
	}

	if len(lc.customVMNameToGenesis) == 0 {
		color.Outf("{{orange}}{{bold}}custom VM not specified, skipping installation and its health checks...{{/}}\n")
		return
	}
	if err := lc.installCustomVMs(ctx); err != nil {
		lc.startErrc <- err
		return
	}
	if err := lc.waitForCustomVMsReady(ctx); err != nil {
		lc.startErrc <- err
		return
	}
}

func (lc *localNetwork) loadSnapshot(ctx context.Context, snapshotName string) error {
	defer func() {
		close(lc.startDonec)
	}()
	color.Outf("{{blue}}{{bold}}create and run local network from snapshot{{/}}\n")
	nw, err := local.NewNetwork(lc.logger, lc.options.rootDataDir, lc.options.snapshotsDir)
	if err != nil {
		return err
	}
	err = nw.LoadSnapshot(ctx, snapshotName)
	if err != nil {
		return err
	}
	lc.nw = nw
	return nil
}

func (lc *localNetwork) loadSnapshotWait(ctx context.Context) {
	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrc <- err
		return
	}
	if err := lc.updateSubnetInfo(ctx); err != nil {
		lc.startErrc <- err
		return
	}
	close(lc.customVMsReadyc)
}

func (lc *localNetwork) updateSubnetInfo(ctx context.Context) error {
	node, err := lc.nw.GetNode(lc.nodeNames[0])
	if err != nil {
		return err
	}
	blockchains, err := node.GetAPIClient().PChainAPI().GetBlockchains(ctx)
	if err != nil {
		return err
	}
	for _, blockchain := range blockchains {
		if blockchain.Name != "C-Chain" && blockchain.Name != "X-Chain" {
			lc.customVMIDToInfo[blockchain.VMID] = vmInfo{
				info: &rpcpb.CustomVmInfo{
					VmName:       blockchain.Name,
					VmId:         blockchain.VMID.String(),
					SubnetId:     blockchain.SubnetID.String(),
					BlockchainId: blockchain.ID.String(),
				},
				subnetID:     blockchain.SubnetID,
				blockchainID: blockchain.ID,
			}
		}
	}
	return nil
}

var errAborted = errors.New("aborted")

func (lc *localNetwork) waitForLocalClusterReady(ctx context.Context) error {
	color.Outf("{{blue}}{{bold}}waiting for all nodes to report healthy...{{/}}\n")

	hc := lc.nw.Healthy(ctx)
	select {
	case <-lc.stopc:
		return errAborted
	case <-ctx.Done():
		return ctx.Err()
	case err := <-hc:
		if err != nil {
			return err
		}
	}

	if err := lc.updateNodeInfo(); err != nil {
		return err
	}

	for _, name := range lc.nodeNames {
		nodeInfo := lc.nodeInfos[name]
		color.Outf("{{cyan}}%s: node ID %q, URI %q{{/}}\n", name, nodeInfo.Id, nodeInfo.Uri)
	}
	lc.localClusterReadycCloseOnce.Do(func() {
		close(lc.localClusterReadyc)
	})
	return nil
}

func (lc *localNetwork) updateNodeInfo() error {
	nodes, err := lc.nw.GetAllNodes()
	if err != nil {
		return err
	}
	nodeNames := []string{}
	for name := range nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)
	lc.nodeNames = nodeNames
	lc.nodeInfos = make(map[string]*rpcpb.NodeInfo)
	for _, name := range lc.nodeNames {
		node := nodes[name]
		configFile := []byte(node.GetConfigFile())
		var pluginDir string
		var whitelistedSubnets string
		var configFileMap map[string]interface{}
		if err := json.Unmarshal(configFile, &configFileMap); err != nil {
			return err
		}
		buildDirIntf, ok := configFileMap[config.BuildDirKey]
		if ok {
			buildDir, ok := buildDirIntf.(string)
			if ok {
				if buildDir != "" {
					pluginDir = filepath.Join(buildDir, "plugins")
				}
			} else {
				return fmt.Errorf("unexpected type for %q expected string got %T", config.BuildDirKey, buildDirIntf)
			}
		}
		whitelistedSubnetsIntf, ok := configFileMap[config.WhitelistedSubnetsKey]
		if ok {
			whitelistedSubnets, ok = whitelistedSubnetsIntf.(string)
			if !ok {
				return fmt.Errorf("unexpected type for %q expected string got %T", config.WhitelistedSubnetsKey, whitelistedSubnetsIntf)
			}
		}

		lc.nodeInfos[name] = &rpcpb.NodeInfo{
			Name:               node.GetName(),
			Uri:                fmt.Sprintf("http://%s:%d", node.GetURL(), node.GetAPIPort()),
			Id:                 node.GetNodeID().PrefixedString(constants.NodeIDPrefix),
			ExecPath:           node.GetBinaryPath(),
			LogDir:             node.GetLogsDir(),
			DbDir:              node.GetDbDir(),
			Config:             []byte(node.GetConfigFile()),
			PluginDir:          pluginDir,
			WhitelistedSubnets: whitelistedSubnets,
		}
	}
	return nil
}

func (lc *localNetwork) stop(ctx context.Context) {
	lc.stopOnce.Do(func() {
		close(lc.stopc)
		serr := lc.nw.Stop(ctx)
		<-lc.startDonec
		color.Outf("{{red}}{{bold}}terminated network{{/}} (error %v)\n", serr)
	})
}
