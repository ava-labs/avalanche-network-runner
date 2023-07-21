// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanche-network-runner/ux"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	avago_constants "github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"golang.org/x/exp/maps"
)

const (
	prometheusConfFname  = "prometheus.yaml"
	prometheusConfCommon = `global:
  scrape_interval: 15s
  evaluation_interval: 15s
scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: 
        - localhost:9090
  - job_name: avalanchego-machine
    static_configs:
     - targets: 
       - localhost:9100
       labels:
         alias: machine
  - job_name: avalanchego
    metrics_path: /ext/metrics
    static_configs:
      - targets:
`
)

type localNetwork struct {
	lock sync.Mutex
	log  logging.Logger

	networkID uint32

	execPath  string
	pluginDir string

	cfg network.Config

	nw network.Network

	nodeInfos map[string]*rpcpb.NodeInfo

	options localNetworkOptions

	// map from blockchain ID to blockchain info
	customChainIDToInfo map[ids.ID]chainInfo

	// Closed when [stop] is called.
	stopCh   chan struct{}
	stopOnce sync.Once

	subnets map[string]*rpcpb.SubnetInfo

	prometheusConfPath string
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

	return &localNetwork{
		log:                 logger,
		execPath:            opts.execPath,
		pluginDir:           opts.pluginDir,
		options:             opts,
		customChainIDToInfo: make(map[ids.ID]chainInfo),
		stopCh:              make(chan struct{}),
		nodeInfos:           make(map[string]*rpcpb.NodeInfo),
		subnets:             make(map[string]*rpcpb.SubnetInfo),
	}, nil
}

// TODO document.
// Assumes [lc.lock] is held.
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

// Creates a network and sets [lc.nw] to it.
// Assumes [lc.lock] isn't held.
func (lc *localNetwork) Start(ctx context.Context) error {
	lc.lock.Lock()
	defer lc.lock.Unlock()

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

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return err
	}

	return nil
}

// Creates the blockchains specified in [chainSpecs].
// Assumes [lc.lock] isn't held.
func (lc *localNetwork) CreateChains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec, // VM name + genesis bytes
) ([]ids.ID, error) {
	lc.lock.Lock()
	defer lc.lock.Unlock()

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

	if len(chainSpecs) == 0 {
		return nil, nil
	}

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return nil, err
	}

	chainIDs, err := lc.nw.CreateBlockchains(ctx, chainSpecs)
	if err != nil {
		return nil, err
	}

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return nil, err
	}

	return chainIDs, nil
}

func (lc *localNetwork) AddPermissionlessDelegators(ctx context.Context, delegatorSpecs []network.PermissionlessStakerSpec) error {
	lc.lock.Lock()
	defer lc.lock.Unlock()

	if len(delegatorSpecs) == 0 {
		ux.Print(lc.log, logging.Orange.Wrap(logging.Bold.Wrap("no delegator specs provided...")))
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

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return err
	}

	err := lc.nw.AddPermissionlessDelegators(ctx, delegatorSpecs)
	if err != nil {
		return err
	}

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return err
	}

	ux.Print(lc.log, logging.Green.Wrap(logging.Bold.Wrap("finished adding permissionless delegators")))
	return nil
}

func (lc *localNetwork) AddPermissionlessValidators(ctx context.Context, validatorSpecs []network.PermissionlessStakerSpec) error {
	lc.lock.Lock()
	defer lc.lock.Unlock()

	if len(validatorSpecs) == 0 {
		ux.Print(lc.log, logging.Orange.Wrap(logging.Bold.Wrap("no validator specs provided...")))
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

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return err
	}

	err := lc.nw.AddPermissionlessValidators(ctx, validatorSpecs)
	if err != nil {
		return err
	}

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return err
	}

	ux.Print(lc.log, logging.Green.Wrap(logging.Bold.Wrap("finished adding permissionless validators")))
	return nil
}

func (lc *localNetwork) RemoveSubnetValidator(ctx context.Context, validatorSpecs []network.RemoveSubnetValidatorSpec) error {
	lc.lock.Lock()
	defer lc.lock.Unlock()

	if len(validatorSpecs) == 0 {
		ux.Print(lc.log, logging.Orange.Wrap(logging.Bold.Wrap("no validator specs provided...")))
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

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return err
	}

	err := lc.nw.RemoveSubnetValidators(ctx, validatorSpecs)
	if err != nil {
		return err
	}

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return err
	}

	ux.Print(lc.log, logging.Green.Wrap(logging.Bold.Wrap("finished removing subnet validators")))
	return nil
}

func (lc *localNetwork) TransformSubnets(ctx context.Context, elasticSubnetSpecs []network.ElasticSubnetSpec) ([]ids.ID, []ids.ID, error) {
	lc.lock.Lock()
	defer lc.lock.Unlock()

	if len(elasticSubnetSpecs) == 0 {
		ux.Print(lc.log, logging.Orange.Wrap(logging.Bold.Wrap("no subnets specified...")))
		return nil, nil, nil
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

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return nil, nil, err
	}

	chainIDs, assetIDs, err := lc.nw.TransformSubnet(ctx, elasticSubnetSpecs)
	if err != nil {
		return nil, nil, err
	}

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return nil, nil, err
	}

	ux.Print(lc.log, logging.Green.Wrap(logging.Bold.Wrap("finished transforming subnets")))
	return chainIDs, assetIDs, nil
}

// Creates the given number of subnets.
// Assumes [lc.lock] isn't held.
func (lc *localNetwork) CreateSubnets(ctx context.Context, subnetSpecs []network.SubnetSpec) ([]ids.ID, error) {
	lc.lock.Lock()
	defer lc.lock.Unlock()

	if len(subnetSpecs) == 0 {
		ux.Print(lc.log, logging.Orange.Wrap(logging.Bold.Wrap("no subnets specified...")))
		return nil, nil
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

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return nil, err
	}

	subnetIDs, err := lc.nw.CreateSubnets(ctx, subnetSpecs)
	if err != nil {
		return nil, err
	}

	if err := lc.awaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return nil, err
	}

	ux.Print(lc.log, logging.Green.Wrap(logging.Bold.Wrap("finished adding subnets")))
	return subnetIDs, nil
}

// Loads a snapshot and sets [l.nw] to the network created from the snapshot.
// Assumes [lc.lock] isn't held.
func (lc *localNetwork) LoadSnapshot(snapshotName string) error {
	lc.lock.Lock()
	defer lc.lock.Unlock()

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

	if err := lc.updateNodeInfo(); err != nil {
		return err
	}

	return nil
}

// Populates [lc.customChainIDToInfo] for all chains other than those on
// the Primary Network (P-Chain, X-Chain, C-Chain.)
// Populates [lc.subnets] with all subnets that exist.
// Doesn't contain the Primary network.
// Assumes [lc.lock] is held.
func (lc *localNetwork) updateSubnetInfo(ctx context.Context) error {
	nodes, err := lc.nw.GetAllNodes()
	if err != nil {
		return err
	}
	minAPIPortNumber := uint16(local.MaxPort)
	var node node.Node
	for _, n := range nodes {
		if n.GetPaused() {
			continue
		}
		if n.GetAPIPort() < minAPIPortNumber {
			minAPIPortNumber = n.GetAPIPort()
			node = n
		}
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

	subnetIDList := []string{}
	for _, subnet := range subnets {
		if subnet.ID != avago_constants.PlatformChainID {
			subnetIDList = append(subnetIDList, subnet.ID.String())
		}
	}

	for _, subnetIDStr := range subnetIDList {
		subnetID, err := ids.FromString(subnetIDStr)
		if err != nil {
			return err
		}
		vdrs, err := node.GetAPIClient().PChainAPI().GetCurrentValidators(ctx, subnetID, nil)
		if err != nil {
			return err
		}
		var nodeNameList []string

		for _, node := range vdrs {
			for nodeName, nodeInfo := range lc.nodeInfos {
				if nodeInfo.Id == node.NodeID.String() {
					nodeNameList = append(nodeNameList, nodeName)
				}
			}
		}

		isElastic := false
		elasticSubnetID := ids.Empty
		if _, err := node.GetAPIClient().PChainAPI().GetCurrentSupply(ctx, subnetID); err == nil {
			isElastic = true
			elasticSubnetID, err = lc.nw.GetElasticSubnetID(ctx, subnetID)
			if err != nil {
				return err
			}
		}

		lc.subnets[subnetIDStr] = &rpcpb.SubnetInfo{
			IsElastic:          isElastic,
			ElasticSubnetId:    elasticSubnetID.String(),
			SubnetParticipants: &rpcpb.SubnetParticipants{NodeNames: nodeNameList},
		}
	}

	for chainID, chainInfo := range lc.customChainIDToInfo {
		vs, err := node.GetAPIClient().PChainAPI().GetCurrentValidators(ctx, chainInfo.subnetID, nil)
		if err != nil {
			return err
		}
		nodeNames := []string{}
		for _, v := range vs {
			for nodeName, nodeInfo := range lc.nodeInfos {
				if v.NodeID.String() == nodeInfo.Id {
					nodeNames = append(nodeNames, nodeName)
				}
			}
		}
		if len(nodeNames) != len(vs) {
			return fmt.Errorf("not all subnet validators are in network for subnet %s", chainInfo.subnetID.String())
		}

		sort.Strings(nodeNames)
		for _, nodeName := range nodeNames {
			nodeInfo := lc.nodeInfos[nodeName]
			if nodeInfo.Paused {
				continue
			}
			lc.log.Info(fmt.Sprintf(logging.LightBlue.Wrap("[blockchain RPC for %q] \"%s/ext/bc/%s\""), chainInfo.info.VmId, nodeInfo.GetUri(), chainID))
		}
	}

	return nil
}

// Assumes [lc.lock] isn't held.
func (lc *localNetwork) AwaitHealthyAndUpdateNetworkInfo(ctx context.Context) error {
	lc.lock.Lock()
	defer lc.lock.Unlock()

	return lc.awaitHealthyAndUpdateNetworkInfo(ctx)
}

// Returns nil when [lc.nw] reports healthy.
// Updates node and subnet info.
// Assumes [lc.lock] is held.
func (lc *localNetwork) awaitHealthyAndUpdateNetworkInfo(ctx context.Context) error {
	ux.Print(lc.log, logging.Blue.Wrap(logging.Bold.Wrap("waiting for all nodes to report healthy...")))

	if err := lc.nw.Healthy(ctx); err != nil {
		return err
	}

	if err := lc.updateNodeInfo(); err != nil {
		return err
	}

	if err := lc.updateSubnetInfo(ctx); err != nil {
		return err
	}

	var err error
	lc.networkID, err = lc.nw.GetNetworkID()
	if err != nil {
		return err
	}

	nodeNames := maps.Keys(lc.nodeInfos)
	sort.Strings(nodeNames)
	for _, nodeName := range nodeNames {
		nodeInfo := lc.nodeInfos[nodeName]
		if nodeInfo.Paused {
			continue
		}
		lc.log.Debug(fmt.Sprintf(logging.Cyan.Wrap("node-info: node-name %s, node-ID: %s, URI: %s"), nodeName, nodeInfo.Id, nodeInfo.Uri))
	}

	return nil
}

// Assumes [lc.lock] isn't held.
func (lc *localNetwork) UpdateNodeInfo() error {
	lc.lock.Lock()
	defer lc.lock.Unlock()

	return lc.updateNodeInfo()
}

// Populates [lc.nodeNames] and [lc.nodeInfos] for
// all nodes in this network.
// Assumes [lc.lock] is held.
func (lc *localNetwork) updateNodeInfo() error {
	nodes, err := lc.nw.GetAllNodes()
	if err != nil {
		return err
	}

	lc.nodeInfos = make(map[string]*rpcpb.NodeInfo)
	for name, node := range nodes {
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
			Paused:             node.GetPaused(),
		}

		// update default exec and pluginDir if empty (snapshots started without these params)
		if lc.execPath == "" {
			lc.execPath = node.GetBinaryPath()
		}
		if lc.pluginDir == "" {
			lc.pluginDir = node.GetPluginDir()
		}
	}
	return lc.generatePrometheusConf()
}

func (lc *localNetwork) generatePrometheusConf() error {
	if lc.prometheusConfPath == "" {
		lc.prometheusConfPath = filepath.Join(lc.options.rootDataDir, prometheusConfFname)
		lc.log.Info(fmt.Sprintf(logging.Cyan.Wrap("prometheus conf file %s"), lc.prometheusConfPath))
	}
	prometheusConf := prometheusConfCommon
	for _, nodeInfo := range lc.nodeInfos {
		if !nodeInfo.Paused {
			prometheusConf += "        - " + strings.TrimPrefix(nodeInfo.Uri, "http://") + "\n"
		}
	}
	file, err := os.Create(lc.prometheusConfPath)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write([]byte(prometheusConf))
	return err
}

// Assumes [lc.lock] isn't held.
func (lc *localNetwork) Stop(ctx context.Context) {
	lc.stopOnce.Do(func() {
		close(lc.stopCh) // Stop in-flight method executions

		lc.lock.Lock()
		defer lc.lock.Unlock()

		if lc.nw != nil {
			err := lc.nw.Stop(ctx)
			msg := "terminated network"
			if err != nil {
				msg += fmt.Sprintf(" (error %v)", err)
			}
			ux.Print(lc.log, logging.Red.Wrap(msg))
		}
	})
}
