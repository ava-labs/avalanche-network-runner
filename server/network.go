// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
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
	log logging.Logger

	execPath string
	buildDir string

	cfg network.Config

	nw network.Network

	nodeNames []string

	nodeInfos map[string]*rpcpb.NodeInfo

	options localNetworkOptions

	// map from blockchain ID to blockchain info
	customChainIDToInfo map[ids.ID]chainInfo

	customChainRestartMu *sync.RWMutex

	stopCh         chan struct{}
	startDoneCh    chan struct{}
	startErrCh     chan error
	startCtxCancel context.CancelFunc // allow the Start context to be cancelled

	stopOnce sync.Once

	subnets []string

	// default chain configs to be used when adding new nodes to the network
	// includes the ones received in options, plus default config or snapshot
	chainConfigs map[string]string
}

type chainInfo struct {
	info         *rpcpb.CustomChainInfo
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

	buildDir          string
	customNodeConfigs map[string]string

	// chain configs to be added to the network, besides the ones in default config, or saved snapshot
	chainConfigs map[string]string

	// to block racey restart while installing custom chains
	restartMu *sync.RWMutex

	snapshotsDir string

	logLevel logging.Level
}

func newLocalNetwork(opts localNetworkOptions) (*localNetwork, error) {
	lcfg := logging.Config{
		DisplayLevel: logging.Info,
		LogLevel:     logging.Debug,
	}
	lcfg.Directory = opts.rootDataDir
	logFactory := logging.NewFactory(lcfg)
	logger, err := logFactory.Make("main")
	if err != nil {
		return nil, err
	}

	return &localNetwork{
		log: logger,

		execPath: opts.execPath,

		buildDir: getBuildDir(opts.execPath, opts.buildDir),

		options: opts,

		customChainIDToInfo:  make(map[ids.ID]chainInfo),
		customChainRestartMu: opts.restartMu,

		stopCh:      make(chan struct{}),
		startDoneCh: make(chan struct{}),
		startErrCh:  make(chan error, 1),

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

		for k, v := range lc.options.chainConfigs {
			cfg.NodeConfigs[i].ChainConfigFiles[k] = v
		}

		mergedConfig, err := mergeNodeConfig(defaultConfig, globalConfig, lc.options.customNodeConfigs[nodeName])
		if err != nil {
			return fmt.Errorf("failed merging provided configs: %w", err)
		}

		cfg.NodeConfigs[i].ConfigFile, err = createConfigFileString(mergedConfig, logDir, dbDir, lc.buildDir, lc.options.whitelistedSubnets, lc.log)
		if err != nil {
			return err
		}

		cfg.NodeConfigs[i].BinaryPath = lc.execPath
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

// if givenBuildDir is empty, generates it from execPath
// returns error if pluginDir is non empty and invalid
func getBuildDir(execPath string, givenBuildDir string) string {
	buildDir := ""
	if execPath != "" {
		buildDir = filepath.Dir(execPath)
	}
	if givenBuildDir != "" {
		buildDir = givenBuildDir
	}
	return buildDir
}

// createConfigFileString finalizes the config setup and returns the node config JSON string
func createConfigFileString(
	configFileMap map[string]interface{},
	logDir string,
	dbDir string,
	buildDir string,
	whitelistedSubnets string,
	log logging.Logger,
) (string, error) {
	// add (or overwrite, if given) the following entries
	if configFileMap[config.LogsDirKey] != "" {
		log.Warn("ignoring config file entry %q provided; the network runner needs to set its own", config.LogsDirKey)
	}
	configFileMap[config.LogsDirKey] = logDir
	if configFileMap[config.DBPathKey] != "" {
		log.Warn("ignoring config file entry %q provided; the network runner needs to set its own", config.DBPathKey)
	}
	configFileMap[config.DBPathKey] = dbDir
	if buildDir != "" {
		configFileMap[config.BuildDirKey] = buildDir
	}
	// need to whitelist subnet ID to create custom chain
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

func (lc *localNetwork) start() error {
	if err := lc.createConfig(); err != nil {
		return err
	}

	lc.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("create and run local network")))
	nw, err := local.NewNetwork(lc.log, lc.cfg, lc.options.rootDataDir, lc.options.snapshotsDir)
	if err != nil {
		return err
	}
	lc.nw = nw

	// node info is already available
	if err := lc.updateNodeInfo(); err != nil {
		return err
	}

	return nil
}

func (lc *localNetwork) startWait(
	argCtx context.Context,
	chainSpecs []network.BlockchainSpec, // VM name + genesis bytes
	readyCh chan struct{}, // messaged when initial network is healthy, closed when subnet installations are complete
) {
	defer close(lc.startDoneCh)

	// start triggers a series of different time consuming actions
	// (in case of subnets: create a wallet, create subnets, issue txs, etc.)
	// We may need to cancel the context, for example if the client hits Ctrl-C
	var ctx context.Context
	ctx, lc.startCtxCancel = context.WithCancel(argCtx)

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrCh <- err
		return
	}

	readyCh <- struct{}{}

	lc.createBlockchains(ctx, chainSpecs, readyCh)
}

func (lc *localNetwork) createBlockchains(
	argCtx context.Context,
	chainSpecs []network.BlockchainSpec, // VM name + genesis bytes
	createBlockchainsReadyCh chan struct{}, // closed when subnet installations are complete
) {
	// createBlockchains triggers a series of different time consuming actions
	// (in case of subnets: create a wallet, create subnets, issue txs, etc.)
	// We may need to cancel the context, for example if the client hits Ctrl-C
	var ctx context.Context
	ctx, lc.startCtxCancel = context.WithCancel(argCtx)

	if len(chainSpecs) == 0 {
		lc.log.Info(logging.Orange.Wrap(logging.Bold.Wrap("custom chain not specified, skipping installation and its health checks")))
		return
	}

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.nw.CreateBlockchains(ctx, chainSpecs); err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.updateNodeInfo(); err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.updateSubnetInfo(ctx); err != nil {
		lc.startErrCh <- err
		return
	}

	close(createBlockchainsReadyCh)
}

func (lc *localNetwork) createSubnets(
	argCtx context.Context,
	numSubnets uint32,
	createSubnetsReadyCh chan struct{}, // closed when subnet installations are complete
) {
	// start triggers a series of different time consuming actions
	// (in case of subnets: create a wallet, create subnets, issue txs, etc.)
	// We may need to cancel the context, for example if the client hits Ctrl-C
	var ctx context.Context
	ctx, lc.startCtxCancel = context.WithCancel(argCtx)

	if numSubnets == 0 {
		lc.log.Info(logging.Orange.Wrap(logging.Bold.Wrap("no subnets specified...")))
		return
	}

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.nw.CreateSubnets(ctx, numSubnets); err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.updateNodeInfo(); err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.updateSubnetInfo(ctx); err != nil {
		lc.startErrCh <- err
	}

	lc.log.Info(logging.Green.Wrap(logging.Bold.Wrap("finished adding subnets")))

	close(createSubnetsReadyCh)
}

func (lc *localNetwork) loadSnapshot(
	ctx context.Context,
	snapshotName string,
) error {
	lc.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("create and run local network from snapshot")))

	var globalNodeConfig map[string]interface{}
	if lc.options.globalNodeConfig != "" {
		if err := json.Unmarshal([]byte(lc.options.globalNodeConfig), &globalNodeConfig); err != nil {
			return err
		}
	}

	nw, err := local.NewNetworkFromSnapshot(
		lc.log,
		snapshotName,
		lc.options.rootDataDir,
		lc.options.snapshotsDir,
		lc.execPath,
		lc.buildDir,
		lc.options.chainConfigs,
		globalNodeConfig,
	)
	if err != nil {
		return err
	}
	lc.nw = nw

	// node info is already available
	if err := lc.updateNodeInfo(); err != nil {
		return err
	}

	return nil
}

func (lc *localNetwork) loadSnapshotWait(ctx context.Context, loadSnapshotReadyCh chan struct{}) {
	defer close(lc.startDoneCh)
	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrCh <- err
		return
	}
	if err := lc.updateSubnetInfo(ctx); err != nil {
		lc.startErrCh <- err
		return
	}
	close(loadSnapshotReadyCh)
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
			lc.customChainIDToInfo[blockchain.ID] = chainInfo{
				info: &rpcpb.CustomChainInfo{
					ChainName: blockchain.Name,
					VmId:      blockchain.VMID.String(),
					SubnetId:  blockchain.SubnetID.String(),
					ChainId:   blockchain.ID.String(),
				},
				subnetID:     blockchain.SubnetID,
				blockchainID: blockchain.ID,
			}
		}
	}
	subnets, err := node.GetAPIClient().PChainAPI().GetSubnets(ctx, nil)
	if err != nil {
		return err
	}
	lc.subnets = []string{}
	for _, subnet := range subnets {
		if subnet.ID != constants.PlatformChainID {
			lc.subnets = append(lc.subnets, subnet.ID.String())
		}
	}
	for _, nodeName := range lc.nodeNames {
		nodeInfo := lc.nodeInfos[nodeName]
		for chainID, chainInfo := range lc.customChainIDToInfo {
			lc.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("[blockchain RPC for %q] \"%s/ext/bc/%s\"")), chainInfo.info.VmId, nodeInfo.GetUri(), chainID)
		}
	}
	return nil
}

func (lc *localNetwork) waitForLocalClusterReady(ctx context.Context) error {
	lc.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("waiting for all nodes to report healthy...")))

	if err := lc.nw.Healthy(ctx); err != nil {
		return err
	}

	for _, name := range lc.nodeNames {
		nodeInfo := lc.nodeInfos[name]
		lc.log.Info(logging.Cyan.Wrap("%s: node ID %q, URI %q"), name, nodeInfo.Id, nodeInfo.Uri)
	}
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
		var whitelistedSubnets string
		var configFileMap map[string]interface{}
		if err := json.Unmarshal(configFile, &configFileMap); err != nil {
			return err
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
			Id:                 node.GetNodeID().String(),
			ExecPath:           node.GetBinaryPath(),
			LogDir:             node.GetLogsDir(),
			DbDir:              node.GetDbDir(),
			Config:             []byte(node.GetConfigFile()),
			BuildDir:           node.GetBuildDir(),
			WhitelistedSubnets: whitelistedSubnets,
		}

		// update default exec and buildDir if empty (snapshots started without this params)
		if lc.execPath == "" {
			lc.execPath = node.GetBinaryPath()
		}
		if lc.buildDir == "" {
			lc.buildDir = node.GetBuildDir()
		}
		// update default chain configs if empty
		if lc.chainConfigs == nil {
			lc.chainConfigs = node.GetConfig().ChainConfigFiles
		}

	}
	return nil
}

func (lc *localNetwork) stop(ctx context.Context) {
	lc.stopOnce.Do(func() {
		close(lc.stopCh)
		// cancel possible concurrent still running start
		if lc.startCtxCancel != nil {
			lc.startCtxCancel()
		}
		serr := lc.nw.Stop(ctx)
		<-lc.startDoneCh
		lc.log.Info(logging.Red.Wrap(logging.Bold.Wrap("terminated network (error %v)")), serr)
	})
}
