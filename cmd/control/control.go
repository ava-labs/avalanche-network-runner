// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner/client"
	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanche-network-runner/ux"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func init() {
	cobra.EnablePrefixMatching = true
}

var (
	logLevel           string
	whitelistedSubnets string
	endpoint           string
	dialTimeout        time.Duration
	requestTimeout     time.Duration
	log                logging.Logger
)

// NOTE: Naming convention for node names is currently `node` + number, i.e. `node1,node2,node3,...node101`

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "control [options]",
		Short: "Start a network runner controller.",
	}

	cmd.PersistentFlags().StringVar(&logLevel, "log-level", logging.Info.String(), "log level")
	cmd.PersistentFlags().StringVar(&endpoint, "endpoint", "0.0.0.0:8080", "server endpoint")
	cmd.PersistentFlags().DurationVar(&dialTimeout, "dial-timeout", 10*time.Second, "server dial timeout")
	cmd.PersistentFlags().DurationVar(&requestTimeout, "request-timeout", 3*time.Minute, "client request timeout")

	cmd.AddCommand(
		newStartCommand(),
		newCreateBlockchainsCommand(),
		newCreateSubnetsCommand(),
		newHealthCommand(),
		newURIsCommand(),
		newStatusCommand(),
		newStreamStatusCommand(),
		newAddNodeCommand(),
		newRemoveNodeCommand(),
		newRestartNodeCommand(),
		newAttachPeerCommand(),
		newSendOutboundMessageCommand(),
		newStopCommand(),
		newSaveSnapshotCommand(),
		newLoadSnapshotCommand(),
		newRemoveSnapshotCommand(),
		newGetSnapshotNamesCommand(),
	)

	lvl, err := logging.ToLevel(logLevel)
	if err != nil {
		panic(err)
	}
	lcfg := logging.Config{
		DisplayLevel: lvl,
		// this will result in no written logs, just stdout
		// to enable log files, a logDir param should be added and
		// accordingly possibly a flag
		LogLevel: logging.Off,
	}
	logFactory := logging.NewFactory(lcfg)
	log, err = logFactory.Make(constants.LogNameControl)
	if err != nil {
		panic(err)
	}

	return cmd
}

var (
	avalancheGoBinPath  string
	numNodes            uint32
	pluginDir           string
	globalNodeConfig    string
	addNodeConfig       string
	blockchainSpecsStr  string
	customNodeConfigs   string
	rootDataDir         string
	numSubnets          uint32
	chainConfigs        string
	upgradeConfigs      string
	subnetConfigs       string
	reassignPortsIfUsed bool
	dynamicPorts        bool
)

func newStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start [options]",
		Short: "Starts the server.",
		RunE:  startFunc,
		Args:  cobra.ExactArgs(0),
	}
	cmd.PersistentFlags().StringVar(
		&avalancheGoBinPath,
		"avalanchego-path",
		"",
		"avalanchego binary path",
	)
	cmd.PersistentFlags().Uint32Var(
		&numNodes,
		"number-of-nodes",
		local.DefaultNumNodes,
		"number of nodes of the network",
	)
	cmd.PersistentFlags().StringVar(
		&pluginDir,
		"plugin-dir",
		"",
		"[optional] plugin directory",
	)
	cmd.PersistentFlags().StringVar(
		&rootDataDir,
		"root-data-dir",
		"",
		"[optional] root data directory to store logs and configurations",
	)
	cmd.PersistentFlags().StringVar(
		&blockchainSpecsStr,
		"blockchain-specs",
		"",
		"[optional] JSON string of array of [(VM name, genesis file path)]",
	)
	cmd.PersistentFlags().StringVar(
		&globalNodeConfig,
		"global-node-config",
		"",
		"[optional] global node config as JSON string, applied to all nodes",
	)
	cmd.PersistentFlags().StringVar(
		&customNodeConfigs,
		"custom-node-configs",
		"",
		"[optional] custom node configs as JSON string of map, for each node individually. Common entries override `global-node-config`, but can be combined. Invalidates `number-of-nodes` (provide all node configs if used).",
	)
	cmd.PersistentFlags().StringVar(
		&whitelistedSubnets,
		"whitelisted-subnets",
		"",
		"[optional] whitelisted subnets (comma-separated)",
	)
	cmd.PersistentFlags().StringVar(
		&chainConfigs,
		"chain-configs",
		"",
		"[optional] JSON string of map from chain id to its config file contents",
	)
	cmd.PersistentFlags().StringVar(
		&upgradeConfigs,
		"upgrade-configs",
		"",
		"[optional] JSON string of map from chain id to its upgrade file contents",
	)
	cmd.PersistentFlags().StringVar(
		&subnetConfigs,
		"subnet-configs",
		"",
		"[optional] JSON string of map from subnet id to its config file contents",
	)
	cmd.PersistentFlags().BoolVar(
		&reassignPortsIfUsed,
		"reassign-ports-if-used",
		false,
		"true to reassign default/given ports if already taken",
	)
	cmd.PersistentFlags().BoolVar(
		&dynamicPorts,
		"dynamic-ports",
		false,
		"true to assign dynamic ports",
	)
	if err := cmd.MarkPersistentFlagRequired("avalanchego-path"); err != nil {
		panic(err)
	}
	return cmd
}

func startFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	opts := []client.OpOption{
		client.WithNumNodes(numNodes),
		client.WithPluginDir(pluginDir),
		client.WithWhitelistedSubnets(whitelistedSubnets),
		client.WithRootDataDir(rootDataDir),
		client.WithReassignPortsIfUsed(reassignPortsIfUsed),
		client.WithDynamicPorts(dynamicPorts),
	}

	if globalNodeConfig != "" {
		ux.Print(log, logging.Yellow.Wrap("global node config provided, will be applied to all nodes: %s"), globalNodeConfig)
		// validate it's valid JSON
		var js json.RawMessage
		if err := json.Unmarshal([]byte(globalNodeConfig), &js); err != nil {
			return fmt.Errorf("failed to validate JSON for provided config file: %s", err)
		}
		opts = append(opts, client.WithGlobalNodeConfig(globalNodeConfig))
	}

	if customNodeConfigs != "" {
		nodeConfigs := make(map[string]string)
		if err := json.Unmarshal([]byte(customNodeConfigs), &nodeConfigs); err != nil {
			return err
		}
		opts = append(opts, client.WithCustomNodeConfigs(nodeConfigs))
	}

	if blockchainSpecsStr != "" {
		blockchainSpecs := []*rpcpb.BlockchainSpec{}
		if err := json.Unmarshal([]byte(blockchainSpecsStr), &blockchainSpecs); err != nil {
			return err
		}
		opts = append(opts, client.WithBlockchainSpecs(blockchainSpecs))
	}

	if chainConfigs != "" {
		chainConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(chainConfigs), &chainConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithChainConfigs(chainConfigsMap))
	}
	if upgradeConfigs != "" {
		upgradeConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(upgradeConfigs), &upgradeConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithUpgradeConfigs(upgradeConfigsMap))
	}
	if subnetConfigs != "" {
		subnetConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(subnetConfigs), &subnetConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithSubnetConfigs(subnetConfigsMap))
	}

	ctx := getAsyncContext()

	info, err := cli.Start(
		ctx,
		avalancheGoBinPath,
		opts...,
	)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("start response: %+v"), info)
	return nil
}

func newCreateBlockchainsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create-blockchains blockchain-specs [options]",
		Short: "Create blockchains.",
		RunE:  createBlockchainsFunc,
		Args:  cobra.ExactArgs(1),
	}
	cmd.PersistentFlags().StringVar(
		&customNodeConfigs,
		"custom-node-configs",
		"",
		"[optional] custom node configs as JSON string of map, for each node individually",
	)
	return cmd
}

func createBlockchainsFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	blockchainSpecsStr := args[0]

	blockchainSpecs := []*rpcpb.BlockchainSpec{}
	if err := json.Unmarshal([]byte(blockchainSpecsStr), &blockchainSpecs); err != nil {
		return err
	}

	opts := []client.OpOption{}

	if customNodeConfigs != "" {
		nodeConfigs := make(map[string]string)
		if err := json.Unmarshal([]byte(customNodeConfigs), &nodeConfigs); err != nil {
			return err
		}
		opts = append(opts, client.WithCustomNodeConfigs(nodeConfigs))
	}

	ctx := getAsyncContext()

	info, err := cli.CreateBlockchains(
		ctx,
		blockchainSpecs,
		opts...,
	)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("deploy-blockchains response: %+v"), info)
	return nil
}

func newCreateSubnetsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create-subnets [options]",
		Short: "Create subnets.",
		RunE:  createSubnetsFunc,
		Args:  cobra.ExactArgs(0),
	}
	cmd.PersistentFlags().Uint32Var(
		&numSubnets,
		"num-subnets",
		0,
		"number of subnets",
	)
	return cmd
}

func createSubnetsFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	opts := []client.OpOption{
		client.WithNumSubnets(numSubnets),
	}

	ctx := getAsyncContext()

	info, err := cli.CreateSubnets(
		ctx,
		opts...,
	)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("add-subnets response: %+v"), info)
	return nil
}

func newHealthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health [options]",
		Short: "Requests server health.",
		RunE:  healthFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func healthFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.Health(ctx)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("health response: %+v"), resp)
	return nil
}

func newURIsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uris [options]",
		Short: "Requests server uris.",
		RunE:  urisFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func urisFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	uris, err := cli.URIs(ctx)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("URIs: %s"), uris)
	return nil
}

func newStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [options]",
		Short: "Requests server status.",
		RunE:  statusFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func statusFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.Status(ctx)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("status response: %+v"), resp)
	return nil
}

var pushInterval time.Duration

func newStreamStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stream-status [options]",
		Short: "Requests server bootstrap status.",
		RunE:  streamStatusFunc,
		Args:  cobra.ExactArgs(0),
	}
	cmd.PersistentFlags().DurationVar(
		&pushInterval,
		"push-interval",
		5*time.Second,
		"interval that server pushes status updates to the client",
	)
	return cmd
}

func streamStatusFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	// poll until the cluster is healthy or os signal
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

	donec := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	go func() {
		select {
		case sig := <-sigc:
			log.Warn("received signal", zap.String("signal", sig.String()))
		case <-ctx.Done():
		}
		cancel()
		close(donec)
	}()

	ch, err := cli.StreamStatus(ctx, pushInterval)
	if err != nil {
		return err
	}
	for info := range ch {
		ux.Print(log, logging.Cyan.Wrap("cluster info: %+v"), info)
	}
	cancel() // receiver channel is closed, so cancel goroutine
	<-donec
	return nil
}

func newRemoveNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove-node node-name [options]",
		Short: "Removes a node.",
		RunE:  removeNodeFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func removeNodeFunc(cmd *cobra.Command, args []string) error {
	// no validation for empty string required, as covered by `cobra.ExactArgs`
	nodeName := args[0]
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.RemoveNode(ctx, nodeName)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("remove node response: %+v"), info)
	return nil
}

func newAddNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-node node-name [options]",
		Short: "Add a new node to the network",
		RunE:  addNodeFunc,
		Args:  cobra.ExactArgs(1),
	}
	cmd.PersistentFlags().StringVar(
		&avalancheGoBinPath,
		"avalanchego-path",
		"",
		"avalanchego binary path",
	)
	cmd.PersistentFlags().StringVar(
		&addNodeConfig,
		"node-config",
		"",
		"node config as string",
	)
	cmd.PersistentFlags().StringVar(
		&chainConfigs,
		"chain-configs",
		"",
		"[optional] JSON string of map from chain id to its config file contents",
	)
	cmd.PersistentFlags().StringVar(
		&upgradeConfigs,
		"upgrade-configs",
		"",
		"[optional] JSON string of map from chain id to its upgrade file contents",
	)
	cmd.PersistentFlags().StringVar(
		&subnetConfigs,
		"subnet-configs",
		"",
		"[optional] JSON string of map from subnet id to its config file contents",
	)
	return cmd
}

func addNodeFunc(cmd *cobra.Command, args []string) error {
	// no validation for empty string required, as covered by `cobra.ExactArgs`
	nodeName := args[0]
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	opts := []client.OpOption{}

	if addNodeConfig != "" {
		ux.Print(log, logging.Yellow.Wrap("WARNING: overriding node configs with custom provided config %s"), addNodeConfig)
		// validate it's valid JSON
		var js json.RawMessage
		if err := json.Unmarshal([]byte(addNodeConfig), &js); err != nil {
			return fmt.Errorf("failed to validate JSON for provided config file: %s", err)
		}
		opts = append(opts, client.WithGlobalNodeConfig(addNodeConfig))
	}

	if chainConfigs != "" {
		chainConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(chainConfigs), &chainConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithChainConfigs(chainConfigsMap))
	}
	if upgradeConfigs != "" {
		upgradeConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(upgradeConfigs), &upgradeConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithUpgradeConfigs(upgradeConfigsMap))
	}
	if subnetConfigs != "" {
		subnetConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(subnetConfigs), &subnetConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithSubnetConfigs(subnetConfigsMap))
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.AddNode(
		ctx,
		nodeName,
		avalancheGoBinPath,
		opts...,
	)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("add node response: %+v"), info)
	return nil
}

func newRestartNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart-node node-name [options]",
		Short: "Restarts a node.",
		RunE:  restartNodeFunc,
		Args:  cobra.ExactArgs(1),
	}
	cmd.PersistentFlags().StringVar(
		&avalancheGoBinPath,
		"avalanchego-path",
		"",
		"avalanchego binary path",
	)
	cmd.PersistentFlags().StringVar(
		&whitelistedSubnets,
		"whitelisted-subnets",
		"",
		"whitelisted subnets (comma-separated)",
	)
	cmd.PersistentFlags().StringVar(
		&chainConfigs,
		"chain-configs",
		"",
		"[optional] JSON string of map from chain id to its config file contents",
	)
	cmd.PersistentFlags().StringVar(
		&upgradeConfigs,
		"upgrade-configs",
		"",
		"[optional] JSON string of map from chain id to its upgrade file contents",
	)
	cmd.PersistentFlags().StringVar(
		&subnetConfigs,
		"subnet-configs",
		"",
		"[optional] JSON string of map from subnet id to its config file contents",
	)
	return cmd
}

func restartNodeFunc(cmd *cobra.Command, args []string) error {
	// no validation for empty string required, as covered by `cobra.ExactArgs`
	nodeName := args[0]
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	opts := []client.OpOption{
		client.WithExecPath(avalancheGoBinPath),
		client.WithWhitelistedSubnets(whitelistedSubnets),
	}

	if chainConfigs != "" {
		chainConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(chainConfigs), &chainConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithChainConfigs(chainConfigsMap))
	}
	if upgradeConfigs != "" {
		upgradeConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(upgradeConfigs), &upgradeConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithUpgradeConfigs(upgradeConfigsMap))
	}
	if subnetConfigs != "" {
		subnetConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(subnetConfigs), &subnetConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithSubnetConfigs(subnetConfigsMap))
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.RestartNode(
		ctx,
		nodeName,
		opts...,
	)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("restart node response: %+v"), info)
	return nil
}

func newAttachPeerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach-peer node-name [options]",
		Short: "Attaches a peer to the node.",
		RunE:  attachPeerFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func attachPeerFunc(cmd *cobra.Command, args []string) error {
	// no validation for empty string required, as covered by `cobra.ExactArgs`
	nodeName := args[0]
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.AttachPeer(ctx, nodeName)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("attach peer response: %+v"), resp)
	return nil
}

var (
	peerID      string
	msgOp       uint32
	msgBytesB64 string
)

func newSendOutboundMessageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "send-outbound-message node-name [options]",
		Short:     "Sends an outbound message to an attached peer.",
		RunE:      sendOutboundMessageFunc,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"node-name"},
	}
	cmd.PersistentFlags().StringVar(
		&peerID,
		"peer-id",
		"",
		"peer ID to send a message to",
	)
	cmd.PersistentFlags().Uint32Var(
		&msgOp,
		"message-op",
		0,
		"Message operation type",
	)
	cmd.PersistentFlags().StringVar(
		&msgBytesB64,
		"message-bytes-b64",
		"",
		"Message bytes in base64 encoding",
	)
	if err := cmd.MarkPersistentFlagRequired("peer-id"); err != nil {
		panic(err)
	}
	if err := cmd.MarkPersistentFlagRequired("message-op"); err != nil {
		panic(err)
	}
	if err := cmd.MarkPersistentFlagRequired("message-bytes-b64"); err != nil {
		panic(err)
	}
	return cmd
}

func sendOutboundMessageFunc(cmd *cobra.Command, args []string) error {
	// no validation for empty string required, as covered by `cobra.ExactArgs`
	nodeName := args[0]
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	b, err := base64.StdEncoding.DecodeString(msgBytesB64)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.SendOutboundMessage(ctx, nodeName, peerID, msgOp, b)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("send outbound message response: %+v"), resp)
	return nil
}

func newStopCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop [options]",
		Short: "Requests server stop.",
		RunE:  stopFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func stopFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.Stop(ctx)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("stop response: %+v"), info)
	return nil
}

func newSaveSnapshotCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "save-snapshot snapshot-name",
		Short: "Requests server to save network snapshot.",
		RunE:  saveSnapshotFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func saveSnapshotFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.SaveSnapshot(ctx, args[0])
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("save-snapshot response: %+v"), resp)
	return nil
}

func newLoadSnapshotCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "load-snapshot snapshot-name",
		Short: "Requests server to load network snapshot.",
		RunE:  loadSnapshotFunc,
		Args:  cobra.ExactArgs(1),
	}
	cmd.PersistentFlags().StringVar(
		&avalancheGoBinPath,
		"avalanchego-path",
		"",
		"avalanchego binary path",
	)
	cmd.PersistentFlags().StringVar(
		&pluginDir,
		"plugin-dir",
		"",
		"plugin directory",
	)
	cmd.PersistentFlags().StringVar(
		&rootDataDir,
		"root-data-dir",
		"",
		"root data directory to store logs and configurations",
	)
	cmd.PersistentFlags().StringVar(
		&chainConfigs,
		"chain-configs",
		"",
		"[optional] JSON string of map from chain id to its config file contents",
	)
	cmd.PersistentFlags().StringVar(
		&upgradeConfigs,
		"upgrade-configs",
		"",
		"[optional] JSON string of map from chain id to its upgrade file contents",
	)
	cmd.PersistentFlags().StringVar(
		&subnetConfigs,
		"subnet-configs",
		"",
		"[optional] JSON string of map from subnet id to its config file contents",
	)
	cmd.PersistentFlags().StringVar(
		&globalNodeConfig,
		"global-node-config",
		"",
		"[optional] global node config as JSON string, applied to all nodes",
	)
	cmd.PersistentFlags().BoolVar(
		&reassignPortsIfUsed,
		"reassign-ports-if-used",
		false,
		"true to reassign snapshot ports if already taken",
	)
	return cmd
}

func loadSnapshotFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	opts := []client.OpOption{
		client.WithExecPath(avalancheGoBinPath),
		client.WithPluginDir(pluginDir),
		client.WithRootDataDir(rootDataDir),
		client.WithReassignPortsIfUsed(reassignPortsIfUsed),
	}

	if chainConfigs != "" {
		chainConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(chainConfigs), &chainConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithChainConfigs(chainConfigsMap))
	}
	if upgradeConfigs != "" {
		upgradeConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(upgradeConfigs), &upgradeConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithUpgradeConfigs(upgradeConfigsMap))
	}
	if subnetConfigs != "" {
		subnetConfigsMap := make(map[string]string)
		if err := json.Unmarshal([]byte(subnetConfigs), &subnetConfigsMap); err != nil {
			return err
		}
		opts = append(opts, client.WithSubnetConfigs(subnetConfigsMap))
	}

	if globalNodeConfig != "" {
		ux.Print(log, logging.Yellow.Wrap("global node config provided, will be applied to all nodes: %s"), globalNodeConfig)

		// validate it's valid JSON
		var js json.RawMessage
		if err := json.Unmarshal([]byte(globalNodeConfig), &js); err != nil {
			return fmt.Errorf("failed to validate JSON for provided config file: %s", err)
		}
		opts = append(opts, client.WithGlobalNodeConfig(globalNodeConfig))
	}

	ctx := getAsyncContext()

	resp, err := cli.LoadSnapshot(ctx, args[0], opts...)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("load-snapshot response: %+v"), resp)
	return nil
}

func newRemoveSnapshotCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove-snapshot snapshot-name",
		Short: "Requests server to remove network snapshot.",
		RunE:  removeSnapshotFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func removeSnapshotFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.RemoveSnapshot(ctx, args[0])
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("remove-snapshot response: %+v"), resp)
	return nil
}

func newGetSnapshotNamesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get-snapshot-names [options]",
		Short: "Requests server to get list of snapshot.",
		RunE:  getSnapshotNamesFunc,
		Args:  cobra.ExactArgs(0),
	}
}

func getSnapshotNamesFunc(cmd *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	snapshotNames, err := cli.GetSnapshotNames(ctx)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("Snapshots: %s"), snapshotNames)
	return nil
}

func newClient() (client.Client, error) {
	return client.New(client.Config{
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	}, log)
}

func getAsyncContext() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	// don't call since function using it is async
	// and the top-level context here "ctx" is passed
	// to all underlying function calls
	// just set the timeout to halt "Start" async ops
	// when the deadline is reached
	_ = cancel
	return ctx
}
