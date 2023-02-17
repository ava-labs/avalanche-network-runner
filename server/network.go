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
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanche-network-runner/ux"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	avago_constants "github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"golang.org/x/exp/maps"
)

type localNetwork struct {
	log logging.Logger

	execPath  string
	pluginDir string

	cfg network.Config

	nw network.Network

	nodeNames []string

	nodeInfos map[string]*rpcpb.NodeInfo

	options localNetworkOptions

	// map from blockchain ID to blockchain info
	customChainIDToInfo map[ids.ID]chainInfo

	rootCtx       context.Context    // TODO use and document
	rootCtxCancel context.CancelFunc // TODO use and document

	stopCh      chan struct{}
	startDoneCh chan struct{}

	stopOnce sync.Once

	subnets []string
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
	trackSubnets        string
	redirectNodesOutput bool
	globalNodeConfig    string

	pluginDir         string
	customNodeConfigs map[string]string

	// chain configs to be added to the network, besides the ones in default config, or saved snapshot
	chainConfigs map[string]string
	// upgrade configs to be added to the network, besides the ones in default config, or saved snapshot
	upgradeConfigs map[string]string
	// subnet configs to be added to the network, besides the ones in default config, or saved snapshot
	subnetConfigs map[string]string

	snapshotsDir string

	logLevel logging.Level

	reassignPortsIfUsed bool

	dynamicPorts bool
}

func newLocalNetwork(opts localNetworkOptions) (*localNetwork, error) {
	logFactory := logging.NewFactory(logging.Config{
		RotatingWriterConfig: logging.RotatingWriterConfig{
			Directory: opts.rootDataDir,
		},
		LogLevel:     opts.logLevel,
		DisplayLevel: opts.logLevel,
	})
	logger, err := logFactory.Make(constants.LogNameMain)
	if err != nil {
		return nil, err
	}

	rootCtx, rootCtxCancel := context.WithCancel(context.Background())
	return &localNetwork{
		log:                 logger,
		execPath:            opts.execPath,
		pluginDir:           opts.pluginDir,
		options:             opts,
		customChainIDToInfo: make(map[ids.ID]chainInfo),
		stopCh:              make(chan struct{}),
		startDoneCh:         make(chan struct{}),
		nodeInfos:           make(map[string]*rpcpb.NodeInfo),
		nodeNames:           []string{},
		rootCtx:             rootCtx,
		rootCtxCancel:       rootCtxCancel,
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

	if lc.pluginDir != "" {
		cfg.Flags[config.PluginDirKey] = lc.pluginDir
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
		for k, v := range lc.options.upgradeConfigs {
			cfg.NodeConfigs[i].UpgradeConfigFiles[k] = v
		}
		for k, v := range lc.options.subnetConfigs {
			cfg.NodeConfigs[i].SubnetConfigFiles[k] = v
		}

		if cfg.NodeConfigs[i].Flags == nil {
			cfg.NodeConfigs[i].Flags = map[string]interface{}{}
		}

		if lc.options.dynamicPorts {
			// remove http port defined in local network config, to get dynamic port generation
			delete(cfg.NodeConfigs[i].Flags, config.HTTPPortKey)
			delete(cfg.NodeConfigs[i].Flags, config.StakingPortKey)
		}

		cfg.NodeConfigs[i].Flags[config.LogsDirKey] = logDir
		cfg.NodeConfigs[i].Flags[config.DBPathKey] = dbDir

		if lc.options.trackSubnets != "" {
			cfg.NodeConfigs[i].Flags[config.TrackSubnetsKey] = lc.options.trackSubnets
		}

		cfg.NodeConfigs[i].BinaryPath = lc.execPath
		cfg.NodeConfigs[i].RedirectStdout = lc.options.redirectNodesOutput
		cfg.NodeConfigs[i].RedirectStderr = lc.options.redirectNodesOutput

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
	}

	lc.cfg = cfg
	return nil
}

func (lc *localNetwork) start() error {
	if err := lc.createConfig(); err != nil {
		return err
	}

	ux.Print(lc.log, logging.Blue.Wrap(logging.Bold.Wrap("create and run local network")))
	nw, err := local.NewNetwork(lc.log, lc.cfg, lc.options.rootDataDir, lc.options.snapshotsDir, lc.options.reassignPortsIfUsed)
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

// Waits until the network is healthy.
// If it doesn't become healthy before [ctx] is canceled, returns an error.
// If it does, sends a message on [readyCh] and creates the blockchains
// specified in [chainSpecs].
// Always closes [lc.startDoneCh] on return.
func (lc *localNetwork) startWait(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec, // VM name + genesis bytes
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func(ctx context.Context) {
		select {
		case <-lc.stopCh:
			// The network is stopped; return from method calls below.
			cancel()
		case <-ctx.Done():
			// This method is done. Don't leak [ctx].
		}
	}(ctx)

	defer close(lc.startDoneCh) // TODO remove

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		return err
	}

	return lc.createBlockchains(ctx, chainSpecs)
}

// Creates the blockchains specified in [chainSpecs].
// If successful, closes [createBlockchainsReadyCh].
func (lc *localNetwork) createBlockchains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec, // VM name + genesis bytes
) error {
	if len(chainSpecs) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func(ctx context.Context) {
		select {
		case <-lc.stopCh:
			// The network is stopped; return from method calls below.
			cancel()
		case <-ctx.Done():
			// This method is done. Don't leak [ctx].
		}
	}(ctx)

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		return err
	}

	if err := lc.nw.CreateBlockchains(ctx, chainSpecs); err != nil {
		return err
	}

	if err := lc.updateNodeInfo(); err != nil {
		return err
	}

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		return err
	}

	if err := lc.updateSubnetInfo(ctx); err != nil {
		return err
	}

	return nil
}

// Creates the given number of subnets.
// If successful, closes [createSubnetsReadyCh].
func (lc *localNetwork) createSubnets(
	ctx context.Context,
	numSubnets uint32,
	createSubnetsReadyCh chan struct{}, // closed when subnet installations are complete
) error {
	if numSubnets == 0 {
		// TODO should we close [createSubnetsReadyCh] here?
		ux.Print(lc.log, logging.Orange.Wrap(logging.Bold.Wrap("no subnets specified...")))
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func(ctx context.Context) {
		select {
		case <-lc.stopCh:
			// The network is stopped; return from method calls below.
			cancel()
		case <-ctx.Done():
			// This method is done. Don't leak [ctx].
		}
	}(ctx)

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		return err
	}

	if err := lc.nw.CreateSubnets(ctx, numSubnets); err != nil {
		return err
	}

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		return err
	}

	if err := lc.updateNodeInfo(); err != nil {
		return err
	}

	if err := lc.updateSubnetInfo(ctx); err != nil {
		return err
	}

	ux.Print(lc.log, logging.Green.Wrap(logging.Bold.Wrap("finished adding subnets")))

	close(createSubnetsReadyCh) // TODO remove
	return nil
}

// Loads a snapshot and sets [l.nw] to the network created from the snapshot.
func (lc *localNetwork) loadSnapshot(snapshotName string) error {
	ux.Print(lc.log, logging.Blue.Wrap(logging.Bold.Wrap("create and run local network from snapshot")))

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
		lc.pluginDir,
		lc.options.chainConfigs,
		lc.options.upgradeConfigs,
		lc.options.subnetConfigs,
		globalNodeConfig,
		lc.options.reassignPortsIfUsed,
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

// Waits for the network to be healthy and updates the subnet info.
// If successful, closes [loadSnapshotReadyCh].
// Always closes [lc.startDoneCh].
func (lc *localNetwork) loadSnapshotWait(ctx context.Context) error {
	defer close(lc.startDoneCh) // TODO remove

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func(ctx context.Context) {
		select {
		case <-lc.stopCh:
			// The network is stopped; return from method calls below.
			cancel()
		case <-ctx.Done():
			// This method is done. Don't leak [ctx].
		}
	}(ctx)

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		return err
	}
	if err := lc.updateSubnetInfo(ctx); err != nil {
		return err
	}
	return nil
}

// Updates [lc.customChainIDToInfo] with the chain info for all chains in the network
// other than the X-Chain and C-Chain.
// Updates [lc.subnets] to include all subnets in the network.
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
		if blockchain.Name == "C-Chain" || blockchain.Name == "X-Chain" {
			continue
		}
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

	subnets, err := node.GetAPIClient().PChainAPI().GetSubnets(ctx, nil)
	if err != nil {
		return err
	}

	lc.subnets = []string{}
	for _, subnet := range subnets {
		if subnet.ID != avago_constants.PlatformChainID {
			lc.subnets = append(lc.subnets, subnet.ID.String())
		}
	}

	for _, nodeName := range lc.nodeNames {
		nodeInfo := lc.nodeInfos[nodeName]
		for chainID, chainInfo := range lc.customChainIDToInfo {
			lc.log.Info(fmt.Sprintf(logging.LightBlue.Wrap("[blockchain RPC for %q] \"%s/ext/bc/%s\""), chainInfo.info.VmId, nodeInfo.GetUri(), chainID))
		}
	}
	return nil
}

// Returns nil when [lc.nw] reports healthy, or an error if it doesn't
// before [ctx] is canceled.
func (lc *localNetwork) waitForLocalClusterReady(ctx context.Context) error {
	ux.Print(lc.log, logging.Blue.Wrap(logging.Bold.Wrap("waiting for all nodes to report healthy...")))

	if err := lc.nw.Healthy(ctx); err != nil {
		return err
	}

	for _, name := range lc.nodeNames {
		nodeInfo := lc.nodeInfos[name]
		lc.log.Info(fmt.Sprintf(logging.Cyan.Wrap("node-info: node-name %s, node-ID: %s, URI: %s"), name, nodeInfo.Id, nodeInfo.Uri))
	}
	return nil
}

func (lc *localNetwork) updateNodeInfo() error {
	nodes, err := lc.nw.GetAllNodes()
	if err != nil {
		return err
	}

	lc.nodeNames = maps.Keys(nodes)
	sort.Strings(lc.nodeNames)

	lc.nodeInfos = make(map[string]*rpcpb.NodeInfo)
	for _, name := range lc.nodeNames {
		node := nodes[name]
		trackSubnets, err := node.GetFlag(config.TrackSubnetsKey)
		if err != nil {
			return err
		}

		lc.nodeInfos[name] = &rpcpb.NodeInfo{
			Name:               node.GetName(),
			Uri:                fmt.Sprintf("http://%s:%d", node.GetURL(), node.GetAPIPort()),
			Id:                 node.GetNodeID().String(),
			ExecPath:           node.GetBinaryPath(),
			LogDir:             node.GetLogsDir(),
			DbDir:              node.GetDbDir(),
			Config:             []byte(node.GetConfigFile()),
			PluginDir:          node.GetPluginDir(),
			WhitelistedSubnets: trackSubnets,
		}

		// update default exec and pluginDir if empty (snapshots started without this params)
		if lc.execPath == "" {
			lc.execPath = node.GetBinaryPath()
		}
		if lc.pluginDir == "" {
			lc.pluginDir = node.GetPluginDir()
		}
	}
	return nil
}

func (lc *localNetwork) stop(ctx context.Context) {
	lc.stopOnce.Do(func() {
		close(lc.stopCh)
		lc.rootCtxCancel() // TODO use or remove
		serr := lc.nw.Stop(ctx)
		<-lc.startDoneCh
		ux.Print(lc.log, logging.Red.Wrap("terminated network %s"), serr)
	})
}
