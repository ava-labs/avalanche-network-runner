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

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/pkg/color"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
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

	apiClis map[string]api.Client

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

		apiClis:       make(map[string]api.Client),
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

func (lc *localNetwork) fillDefaultConfig() error {
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

		cfg.NodeConfigs[i].ConfigFile, err = createConfigFileString(mergedConfig, logDir, dbDir, lc.options.pluginDir, lc.options.whitelistedSubnets)
		if err != nil {
			return err
		}

		cfg.NodeConfigs[i].BinaryPath = lc.options.execPath
		cfg.NodeConfigs[i].RedirectStdout = lc.options.redirectNodesOutput
		cfg.NodeConfigs[i].RedirectStderr = lc.options.redirectNodesOutput

		lc.nodeInfos[nodeName] = &rpcpb.NodeInfo{
			Name:               nodeName,
			ExecPath:           lc.options.execPath,
			Uri:                "",
			Id:                 "",
			LogDir:             logDir,
			DbDir:              dbDir,
			PluginDir:          lc.options.pluginDir,
			WhitelistedSubnets: lc.options.whitelistedSubnets,
			Config:             []byte(cfg.NodeConfigs[i].ConfigFile),
		}
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
func createConfigFileString(config map[string]interface{}, logDir string, dbDir string, pluginDir string, whitelistedSubnets string) (string, error) {
	// add (or overwrite, if given) the following entries
	if config["log-dir"] != "" {
		zap.L().Warn("ignoring 'log-dir' config entry provided; the network runner needs to set its own")
	}
	config["log-dir"] = logDir
	if config["db-dir"] != "" {
		zap.L().Warn("ignoring 'db-dir' config entry provided; the network runner needs to set its own")
	}
	config["db-dir"] = dbDir
	config["plugin-dir"] = pluginDir
	// need to whitelist subnet ID to create custom VM chain
	// ref. vms/platformvm/createChain
	config["whitelisted-subnets"] = whitelistedSubnets

	finalJSON, err := json.Marshal(config)
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
	nw, err := local.NewNetwork(lc.logger, os.TempDir(), "")
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

func (lc *localNetwork) loadSnapshot(ctx context.Context, snapshotName string) {
	defer func() {
		close(lc.startDonec)
	}()

	color.Outf("{{blue}}{{bold}}create and run local network from snapshot{{/}}\n")
	nw, err := local.NewNetwork(lc.logger, os.TempDir(), "")
	if err != nil {
		lc.startErrc <- err
		return
	}
	err = nw.LoadSnapshot(ctx, snapshotName)
	if err != nil {
		lc.startErrc <- err
		return
	}
	lc.nw = nw

	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.startErrc <- err
		return
	}
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

	nodes, err := lc.nw.GetAllNodes()
	if err != nil {
		return err
	}
	nodeNames := []string{}
	for name := range nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	for _, name := range nodeNames {
		node := nodes[name]
		uri := fmt.Sprintf("http://%s:%d", node.GetURL(), node.GetAPIPort())
		nodeID := node.GetNodeID().PrefixedString(constants.NodeIDPrefix)

		if _, ok := lc.nodeInfos[name]; !ok {
			lc.nodeInfos[name] = &rpcpb.NodeInfo{}
		}
		lc.nodeInfos[name].Uri = uri
		lc.nodeInfos[name].Id = nodeID

		lc.apiClis[name] = node.GetAPIClient()
		color.Outf("{{cyan}}%s: node ID %q, URI %q{{/}}\n", name, nodeID, uri)
	}

	lc.localClusterReadycCloseOnce.Do(func() {
		close(lc.localClusterReadyc)
	})
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
