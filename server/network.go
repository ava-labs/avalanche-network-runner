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
	"strings"
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

type localNetwork struct {
	logger logging.Logger

	binPath string
	cfg     network.Config

	nw network.Network

	nodeNames []string
	nodeInfos map[string]*rpcpb.NodeInfo

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
	execPath           string
	rootDataDir        string
	numNodes           uint32
	whitelistedSubnets string
	logLevel           string
	nodeConfigParam    string

	pluginDir string
	customVMs map[string][]byte

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

	logLevel := opts.logLevel
	if logLevel == "" {
		logLevel = "INFO"
	}

	nodeInfos := make(map[string]*rpcpb.NodeInfo)
	cfg, err := local.NewDefaultConfigNNodes(opts.execPath, opts.numNodes)
	if err != nil {
		return nil, err
	}
	nodeNames := make([]string, len(cfg.NodeConfigs))
	for i := range cfg.NodeConfigs {
		nodeName := fmt.Sprintf("node%d", i+1)
		logDir := filepath.Join(opts.rootDataDir, nodeName, "log")
		dbDir := filepath.Join(opts.rootDataDir, nodeName, "db-dir")

		nodeNames[i] = nodeName
		cfg.NodeConfigs[i].Name = nodeName

		// get the node configs right
		nodeConfig := defaultNodeConfig
		if opts.nodeConfigParam != "" {
			nodeConfig = opts.nodeConfigParam
		}
		cfg.NodeConfigs[i].ConfigFile, err = createConfigFileString(nodeConfig, logLevel, logDir, dbDir, opts.pluginDir, opts.whitelistedSubnets)
		if err != nil {
			return nil, err
		}

		cfg.NodeConfigs[i].BinaryPath = opts.execPath
		cfg.NodeConfigs[i].RedirectStdout = true
		cfg.NodeConfigs[i].RedirectStderr = true

		nodeInfos[nodeName] = &rpcpb.NodeInfo{
			Name:               nodeName,
			ExecPath:           opts.execPath,
			Uri:                "",
			Id:                 "",
			LogDir:             logDir,
			DbDir:              dbDir,
			PluginDir:          opts.pluginDir,
			WhitelistedSubnets: opts.whitelistedSubnets,
			Config:             []byte(cfg.NodeConfigs[i].ConfigFile),
		}
	}

	return &localNetwork{
		logger: logger,

		binPath: opts.execPath,
		cfg:     cfg,

		nodeNames:     nodeNames,
		nodeInfos:     nodeInfos,
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
	}, nil
}

func createConfigFileString(nodeConfig string, logLevel string, logDir string, dbDir string, pluginDir string, whitelistedSubnets string) (string, error) {
	var jsonContent map[string]interface{}
	if err := json.Unmarshal([]byte(nodeConfig), &jsonContent); err != nil {
		return "", err
	}
	// add (or overwrite, if given) the following entries
	jsonContent["log-level"] = strings.ToUpper(logLevel)
	jsonContent["log-display-level"] = strings.ToUpper(logLevel)
	jsonContent["log-dir"] = logDir
	jsonContent["db-dir"] = dbDir
	jsonContent["plugin-dir"] = pluginDir
	// need to whitelist subnet ID to create custom VM chain
	// ref. vms/platformvm/createChain
	jsonContent["whitelisted-subnets"] = whitelistedSubnets

	finalJSON, err := json.Marshal(jsonContent)
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
	nw, err := local.NewNetwork(lc.logger, lc.cfg, os.TempDir())
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
	for name, node := range nodes {
		uri := fmt.Sprintf("http://%s:%d", node.GetURL(), node.GetAPIPort())
		nodeID := node.GetNodeID().PrefixedString(constants.NodeIDPrefix)

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
