// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

type localNetwork struct {
	logger logging.Logger

	execPath  string
	pluginDir string

	cfg network.Config

	nw network.Network

	nodeNames []string

	nodeInfos map[string]*rpcpb.NodeInfo

	options localNetworkOptions

	// maps from node name to peer ID to peer object
	attachedPeers map[string]map[string]peer.Peer

	// map from blockchain ID to blockchain info
	customVMBlockchainIDToInfo map[ids.ID]vmInfo

	customVMRestartMu *sync.RWMutex

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
	customNodeConfigs map[string]string

	// chain configs to be added to the network, besides the ones in default config, or saved snapshot
	chainConfigs map[string]string

	// to block racey restart while installing custom VMs
	restartMu *sync.RWMutex

	snapshotsDir string
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
		logger: logger,

		execPath: opts.execPath,

		pluginDir: opts.pluginDir,

		options: opts,

		attachedPeers: make(map[string]map[string]peer.Peer),

		customVMBlockchainIDToInfo: make(map[ids.ID]vmInfo),
		customVMRestartMu:          opts.restartMu,

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

	var globalConfig map[string]interface{}

	if lc.options.globalNodeConfig != "" {
		if err := json.Unmarshal([]byte(lc.options.globalNodeConfig), &globalConfig); err != nil {
			return err
		}
	}

	// set flags applied to all nodes
	for k, v := range globalConfig {
		cfg.Flags[k] = v
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

		if cfg.NodeConfigs[i].Flags == nil {
			cfg.NodeConfigs[i].Flags = map[string]interface{}{}
		}

		// set flags applied to the specific node
		var customNodeConfig map[string]interface{}
		if lc.options.customNodeConfigs != nil && lc.options.customNodeConfigs[nodeName] != "" {
			if err := json.Unmarshal([]byte(lc.options.customNodeConfigs[nodeName]), &customNodeConfig); err != nil {
				return err
			}
		}
		for k, v := range customNodeConfig {
			cfg.NodeConfigs[i].Flags[k] = v
		}

		// avalanchego expects buildDir (parent dir of pluginDir) to be provided at cmdline
		buildDir, err := getBuildDir(lc.execPath, lc.pluginDir)
		if err != nil {
			return err
		}

		cfg.NodeConfigs[i].Flags[config.LogsDirKey] = logDir
		cfg.NodeConfigs[i].Flags[config.DBPathKey] = dbDir
		if buildDir != "" {
			cfg.NodeConfigs[i].Flags[config.BuildDirKey] = buildDir
		}
		if lc.options.whitelistedSubnets != "" {
			cfg.NodeConfigs[i].Flags[config.WhitelistedSubnetsKey] = lc.options.whitelistedSubnets
		}

		cfg.NodeConfigs[i].BinaryPath = lc.options.execPath
		cfg.NodeConfigs[i].RedirectStdout = lc.options.redirectNodesOutput
		cfg.NodeConfigs[i].RedirectStderr = lc.options.redirectNodesOutput
	}

	lc.cfg = cfg
	return nil
}

// generates buildDir from pluginDir, and if not available, from execPath
// returns error if pluginDir is non empty and invalid
func getBuildDir(execPath string, pluginDir string) (string, error) {
	buildDir := ""
	if execPath != "" {
		buildDir = filepath.Dir(execPath)
	}
	if pluginDir != "" {
		pluginDir := filepath.Clean(pluginDir)
		if filepath.Base(pluginDir) != "plugins" {
			return "", fmt.Errorf("plugin dir %q is not named plugins", pluginDir)
		}
		buildDir = filepath.Dir(pluginDir)
	}
	return buildDir, nil
}

func (lc *localNetwork) start() error {
	if err := lc.createConfig(); err != nil {
		return err
	}

	color.Outf("{{blue}}{{bold}}create and run local network{{/}}\n")
	nw, err := local.NewNetwork(lc.logger, lc.cfg, lc.options.rootDataDir, lc.options.snapshotsDir)
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
	chainSpecs []blockchainSpec, // VM name + genesis bytes
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
	chainSpecs []blockchainSpec, // VM name + genesis bytes
	createBlockchainsReadyCh chan struct{}, // closed when subnet installations are complete
) {
	// createBlockchains triggers a series of different time consuming actions
	// (in case of subnets: create a wallet, create subnets, issue txs, etc.)
	// We may need to cancel the context, for example if the client hits Ctrl-C
	var ctx context.Context
	ctx, lc.startCtxCancel = context.WithCancel(argCtx)

	if len(chainSpecs) == 0 {
		color.Outf("{{orange}}{{bold}}custom VM not specified, skipping installation and its health checks...{{/}}\n")
		return
	}

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrCh <- err
		return
	}

	chainInfos, err := lc.installCustomVMs(ctx, chainSpecs)
	if err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.waitForCustomVMsReady(ctx, chainInfos); err != nil {
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
		color.Outf("{{orange}}{{bold}}no subnets specified...{{/}}\n")
		return
	}

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrCh <- err
		return
	}

	_, err := lc.setupWalletAndInstallSubnets(ctx, numSubnets)
	if err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrCh <- err
		return
	}

	if err := lc.updateSubnetInfo(ctx); err != nil {
		lc.startErrCh <- err
	}

	color.Outf("{{green}}{{bold}}finish adding subnets{{/}}\n")

	close(createSubnetsReadyCh)
}

func (lc *localNetwork) loadSnapshot(
	ctx context.Context,
	snapshotName string,
) error {
	color.Outf("{{blue}}{{bold}}create and run local network from snapshot{{/}}\n")

	buildDir, err := getBuildDir(lc.execPath, lc.pluginDir)
	if err != nil {
		return err
	}

	var globalNodeConfig map[string]interface{}
	if lc.options.globalNodeConfig != "" {
		if err := json.Unmarshal([]byte(lc.options.globalNodeConfig), &globalNodeConfig); err != nil {
			return err
		}
	}

	nw, err := local.NewNetworkFromSnapshot(
		lc.logger,
		snapshotName,
		lc.options.rootDataDir,
		lc.options.snapshotsDir,
		lc.execPath,
		buildDir,
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
			lc.customVMBlockchainIDToInfo[blockchain.ID] = vmInfo{
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
		for blockchainID, vmInfo := range lc.customVMBlockchainIDToInfo {
			color.Outf("{{blue}}{{bold}}[blockchain RPC for %q] \"%s/ext/bc/%s\"{{/}}\n", vmInfo.info.VmId, nodeInfo.GetUri(), blockchainID)
		}
	}
	return nil
}

var errAborted = errors.New("aborted")

func (lc *localNetwork) waitForLocalClusterReady(ctx context.Context) error {
	color.Outf("{{blue}}{{bold}}waiting for all nodes to report healthy...{{/}}\n")

	if err := lc.nw.Healthy(ctx); err != nil {
		return err
	}

	for _, name := range lc.nodeNames {
		nodeInfo := lc.nodeInfos[name]
		color.Outf("{{cyan}}%s: node ID %q, URI %q{{/}}\n", name, nodeInfo.Id, nodeInfo.Uri)
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
		var pluginDir string
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
		buildDir := node.GetBuildDir()
		if buildDir != "" {
			pluginDir = filepath.Join(buildDir, "plugins")
		}

		lc.nodeInfos[name] = &rpcpb.NodeInfo{
			Name:               node.GetName(),
			Uri:                fmt.Sprintf("http://%s:%d", node.GetURL(), node.GetAPIPort()),
			Id:                 node.GetNodeID().String(),
			ExecPath:           node.GetBinaryPath(),
			LogDir:             node.GetLogsDir(),
			DbDir:              node.GetDbDir(),
			Config:             []byte(node.GetConfigFile()),
			PluginDir:          pluginDir,
			WhitelistedSubnets: whitelistedSubnets,
		}

		// update default exec and pluginDir if empty (snapshots started without this params)
		if lc.execPath == "" {
			lc.execPath = node.GetBinaryPath()
		}
		if lc.pluginDir == "" {
			lc.pluginDir = pluginDir
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
		color.Outf("{{red}}{{bold}}terminated network{{/}} (error %v)\n", serr)
	})
}
