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
	"path/filepath"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner/client"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanche-network-runner/ux"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	avagoConstants "github.com/ava-labs/avalanchego/utils/constants"
)

func init() {
	cobra.EnablePrefixMatching = true
}

const clientRootDirPrefix = "client"

var (
	logLevel       string
	logDir         string
	trackSubnets   string
	endpoint       string
	dialTimeout    time.Duration
	requestTimeout time.Duration
	log            logging.Logger
)

// NOTE: Naming convention for node names is currently `node` + number, i.e. `node1,node2,node3,...node101`

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "control [options]",
		Short: "Network runner control commands.",
	}

	cmd.PersistentFlags().StringVar(&logLevel, "log-level", logging.Info.String(), "log level")
	cmd.PersistentFlags().StringVar(&logDir, "log-dir", "", "log directory")
	cmd.PersistentFlags().StringVar(&endpoint, "endpoint", "localhost:8080", "server endpoint")
	cmd.PersistentFlags().DurationVar(&dialTimeout, "dial-timeout", 10*time.Second, "server dial timeout")
	cmd.PersistentFlags().DurationVar(&requestTimeout, "request-timeout", 5*time.Minute, "client request timeout")

	cmd.AddCommand(
		newRPCVersionCommand(),
		newStartCommand(),
		newCreateBlockchainsCommand(),
		newCreateSubnetsCommand(),
		newTransformElasticSubnetsCommand(),
		newAddPermissionlessValidatorCommand(),
		newAddPermissionlessDelegatorCommand(),
		newAddSubnetValidatorsCommand(),
		newRemoveSubnetValidatorCommand(),
		newHealthCommand(),
		newWaitForHealthyCommand(),
		newURIsCommand(),
		newStatusCommand(),
		newStreamStatusCommand(),
		newAddNodeCommand(),
		newRemoveNodeCommand(),
		newPauseNodeCommand(),
		newResumeNodeCommand(),
		newRestartNodeCommand(),
		newAttachPeerCommand(),
		newSendOutboundMessageCommand(),
		newStopCommand(),
		newSaveSnapshotCommand(),
		newLoadSnapshotCommand(),
		newRemoveSnapshotCommand(),
		newListSnapshotsCommand(),
		newVMIDCommand(),
		newListSubnetsCommand(),
		newListBlockchainsCommand(),
		newListRPCsCommand(),
	)

	return cmd
}

var (
	avalancheGoBinPath   string
	numNodes             uint32
	pluginDir            string
	globalNodeConfig     string
	addNodeConfig        string
	blockchainSpecsStr   string
	customNodeConfigs    string
	rootDataDir          string
	chainConfigs         string
	upgradeConfigs       string
	subnetConfigs        string
	reassignPortsIfUsed  bool
	dynamicPorts         bool
	networkID            uint32
	force                bool
	inPlace              bool
	fuji                 bool
	walletPrivateKey     string
	walletPrivateKeyPath string
)

func setLogs() error {
	var err error
	if logDir == "" {
		anrRootDir := filepath.Join(os.TempDir(), constants.RootDirPrefix)
		err = os.MkdirAll(anrRootDir, os.ModePerm)
		if err != nil {
			return err
		}
		clientRootDir := filepath.Join(anrRootDir, clientRootDirPrefix)
		logDir, err = utils.MkDirWithTimestamp(clientRootDir)
		if err != nil {
			return err
		}
	}
	lvl, err := logging.ToLevel(logLevel)
	if err != nil {
		return err
	}
	logFactory := logging.NewFactory(logging.Config{
		RotatingWriterConfig: logging.RotatingWriterConfig{
			Directory: logDir,
		},
		DisplayLevel: lvl,
		LogLevel:     lvl,
	})
	log, err = logFactory.Make(constants.LogNameControl)
	return err
}

func newRPCVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rpc_version",
		Short: "Gets RPC server version.",
		RunE:  RPCVersionFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func RPCVersionFunc(*cobra.Command, []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.RPCVersion(ctx)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("version response: %+v"), resp)
	return nil
}

func newStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start [options]",
		Short: "Starts a network.",
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
		&networkID,
		"network-id",
		0,
		"network id to assign to the network",
	)
	cmd.PersistentFlags().Uint32Var(
		&numNodes,
		"number-of-nodes",
		constants.DefaultNumNodes,
		"number of nodes of the network",
	)
	cmd.PersistentFlags().Uint32Var(
		&numNodes,
		"num-nodes",
		constants.DefaultNumNodes,
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
		&trackSubnets,
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
	cmd.PersistentFlags().BoolVar(
		&fuji,
		"fuji",
		false,
		"true to set all nodes to join fuji network",
	)
	cmd.PersistentFlags().StringVar(
		&walletPrivateKey,
		"wallet-private-key",
		"",
		"[optional] funding wallet private key. Please consider using `wallet-private-key-path` if security is a concern.",
	)
	cmd.PersistentFlags().StringVar(
		&walletPrivateKeyPath,
		"wallet-private-key-path",
		"",
		"[optional] funding wallet private key path",
	)
	return cmd
}

func startFunc(*cobra.Command, []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	if fuji {
		networkID = avagoConstants.FujiID
	}
	opts := []client.OpOption{
		client.WithNumNodes(numNodes),
		client.WithPluginDir(pluginDir),
		client.WithTrackSubnets(trackSubnets),
		client.WithRootDataDir(rootDataDir),
		client.WithReassignPortsIfUsed(reassignPortsIfUsed),
		client.WithDynamicPorts(dynamicPorts),
		client.WithNetworkID(networkID),
	}

	if walletPrivateKeyPath != "" && walletPrivateKey != "" {
		return fmt.Errorf("only one of wallet-private-key and wallet-private-key-path can be provided")
	}
	if walletPrivateKey != "" {
		opts = append(opts, client.WithWalletPrivateKey(walletPrivateKey))
	}
	if walletPrivateKeyPath != "" {
		ux.Print(log, logging.Yellow.Wrap("funding wallet private key path provided: %s"), walletPrivateKeyPath)
		// validate if it's a valid private key
		if _, err := os.Stat(walletPrivateKey); err != nil {
			return fmt.Errorf("wallet private key doesn't exist: %w", err)
		}
		// read the private key
		keyBytes, err := os.ReadFile(walletPrivateKey)
		if err != nil {
			return fmt.Errorf("failed to read  wallet private key: %w", err)
		}
		opts = append(opts, client.WithWalletPrivateKey(string(keyBytes)))
	}

	if globalNodeConfig != "" {
		ux.Print(log, logging.Yellow.Wrap("global node config provided, will be applied to all nodes: %s"), globalNodeConfig)
		// validate it's valid JSON
		var js json.RawMessage
		if err := json.Unmarshal([]byte(globalNodeConfig), &js); err != nil {
			return fmt.Errorf("failed to validate JSON for provided config file: %w", err)
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
		Short: "Creates blockchains.",
		RunE:  createBlockchainsFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func createBlockchainsFunc(_ *cobra.Command, args []string) error {
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

	ctx := getAsyncContext()

	info, err := cli.CreateBlockchains(
		ctx,
		blockchainSpecs,
	)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("create-blockchains response: %+v"), info)
	return nil
}

func newCreateSubnetsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create-subnets [options]",
		Short: "Creates subnets.",
		RunE:  createSubnetsFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func createSubnetsFunc(_ *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	subnetSpecsStr := args[0]

	subnetSpecs := []*rpcpb.SubnetSpec{}
	if err := json.Unmarshal([]byte(subnetSpecsStr), &subnetSpecs); err != nil {
		return err
	}

	ctx := getAsyncContext()

	info, err := cli.CreateSubnets(
		ctx,
		subnetSpecs,
	)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("create-subnets response: %+v"), info)
	return nil
}

func newTransformElasticSubnetsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "elastic-subnets elastic_subnets_specs [options]",
		Short: "Transforms subnets to elastic subnets.",
		RunE:  transformElasticSubnetsFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func newAddPermissionlessDelegatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-permissionless-delegator permissionlessValidatorSpecs [options]",
		Short: "Delegates to a permissionless validator in an elastic subnet",
		RunE:  addPermissionlessDelegatorFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func newAddPermissionlessValidatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-permissionless-validator permissionlessValidatorSpecs [options]",
		Short: "Adds a permissionless validator to elastic subnets.",
		RunE:  addPermissionlessValidatorFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func newAddSubnetValidatorsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-subnet-validators validatorsSpec [options]",
		Short: "Adds subnet validators",
		RunE:  addSubnetValidatorsFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func newRemoveSubnetValidatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove-subnet-validator removeValidatorSpec [options]",
		Short: "Removes a subnet validator",
		RunE:  removeSubnetValidatorFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func transformElasticSubnetsFunc(_ *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	elasticSubnetSpecsStr := args[0]

	elasticSubnetSpecs := []*rpcpb.ElasticSubnetSpec{}
	if err := json.Unmarshal([]byte(elasticSubnetSpecsStr), &elasticSubnetSpecs); err != nil {
		return err
	}

	ctx := getAsyncContext()

	info, err := cli.TransformElasticSubnets(
		ctx,
		elasticSubnetSpecs,
	)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("elastic-subnets response: %+v"), info)
	return nil
}

func addPermissionlessDelegatorFunc(_ *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	delegatorSpecsStr := args[0]

	delegatorSpecs := []*rpcpb.PermissionlessStakerSpec{}
	if err := json.Unmarshal([]byte(delegatorSpecsStr), &delegatorSpecs); err != nil {
		return err
	}

	ctx := getAsyncContext()

	info, err := cli.AddPermissionlessDelegator(
		ctx,
		delegatorSpecs,
	)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("add-permissionless-delegator response: %+v"), info)
	return nil
}

func addPermissionlessValidatorFunc(_ *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	validatorSpecsStr := args[0]

	validatorSpecs := []*rpcpb.PermissionlessStakerSpec{}
	if err := json.Unmarshal([]byte(validatorSpecsStr), &validatorSpecs); err != nil {
		return err
	}

	ctx := getAsyncContext()

	info, err := cli.AddPermissionlessValidator(
		ctx,
		validatorSpecs,
	)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("add-permissionless-validator response: %+v"), info)
	return nil
}

func addSubnetValidatorsFunc(_ *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	validatorSpecsStr := args[0]

	validatorSpecs := []*rpcpb.SubnetValidatorsSpec{}
	if err := json.Unmarshal([]byte(validatorSpecsStr), &validatorSpecs); err != nil {
		return err
	}

	ctx := getAsyncContext()

	info, err := cli.AddSubnetValidators(
		ctx,
		validatorSpecs,
	)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("add-subnet-validators response: %+v"), info)
	return nil
}

func removeSubnetValidatorFunc(_ *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	validatorSpecsStr := args[0]

	validatorSpecs := []*rpcpb.RemoveSubnetValidatorSpec{}
	if err := json.Unmarshal([]byte(validatorSpecsStr), &validatorSpecs); err != nil {
		return err
	}

	ctx := getAsyncContext()

	info, err := cli.RemoveSubnetValidator(
		ctx,
		validatorSpecs,
	)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("remove-subnet-validator response: %+v"), info)
	return nil
}

func newHealthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health [options]",
		Short: "Waits until local cluster is ready.",
		RunE:  healthFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func healthFunc(*cobra.Command, []string) error {
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

func newWaitForHealthyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait-for-healthy [options]",
		Short: "Waits until local cluster and custom vms are ready.",
		RunE:  waitForHealthy,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func waitForHealthy(*cobra.Command, []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	resp, err := cli.WaitForHealthy(ctx)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("wait for healthy response: %+v"), resp)
	return nil
}

func newURIsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uris [options]",
		Short: "Lists network uris.",
		RunE:  urisFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func urisFunc(*cobra.Command, []string) error {
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
		Short: "Gets network status.",
		RunE:  statusFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func statusFunc(*cobra.Command, []string) error {
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
		Short: "Gets a stream of network status.",
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

func streamStatusFunc(*cobra.Command, []string) error {
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

func removeNodeFunc(_ *cobra.Command, args []string) error {
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

func newPauseNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause-node node-name [options]",
		Short: "Pauses a node.",
		RunE:  pauseNodeFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func pauseNodeFunc(_ *cobra.Command, args []string) error {
	// no validation for empty string required, as covered by `cobra.ExactArgs`
	nodeName := args[0]
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.PauseNode(ctx, nodeName)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("pause node response: %+v"), info)
	return nil
}

func newResumeNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume-node node-name [options]",
		Short: "Resumes a node.",
		RunE:  resumeNodeFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func resumeNodeFunc(_ *cobra.Command, args []string) error {
	// no validation for empty string required, as covered by `cobra.ExactArgs`
	nodeName := args[0]
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.ResumeNode(ctx, nodeName)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("resume node response: %+v"), info)
	return nil
}

func newAddNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-node node-name [options]",
		Short: "Adds a new node to the network",
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
		&pluginDir,
		"plugin-dir",
		"",
		"[optional] plugin directory",
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

func addNodeFunc(_ *cobra.Command, args []string) error {
	// no validation for empty string required, as covered by `cobra.ExactArgs`
	nodeName := args[0]
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	opts := []client.OpOption{
		client.WithPluginDir(pluginDir),
	}

	if addNodeConfig != "" {
		ux.Print(log, logging.Yellow.Wrap("WARNING: overriding node configs with custom provided config %s"), addNodeConfig)
		// validate it's valid JSON
		var js json.RawMessage
		if err := json.Unmarshal([]byte(addNodeConfig), &js); err != nil {
			return fmt.Errorf("failed to validate JSON for provided config file: %w", err)
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
		&trackSubnets,
		"whitelisted-subnets",
		"",
		"[optional] whitelisted subnets (comma-separated)",
	)
	cmd.PersistentFlags().StringVar(
		&pluginDir,
		"plugin-dir",
		"",
		"[optional] plugin directory",
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

func restartNodeFunc(_ *cobra.Command, args []string) error {
	// no validation for empty string required, as covered by `cobra.ExactArgs`
	nodeName := args[0]
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	opts := []client.OpOption{
		client.WithExecPath(avalancheGoBinPath),
		client.WithPluginDir(pluginDir),
		client.WithTrackSubnets(trackSubnets),
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

func attachPeerFunc(_ *cobra.Command, args []string) error {
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

func sendOutboundMessageFunc(_ *cobra.Command, args []string) error {
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
		Short: "Stops the network.",
		RunE:  stopFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func stopFunc(*cobra.Command, []string) error {
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
		Short: "Saves a network snapshot.",
		RunE:  saveSnapshotFunc,
		Args:  cobra.ExactArgs(1),
	}
	cmd.PersistentFlags().BoolVar(
		&force,
		"force",
		false,
		"overwrite snapshot if it already exists",
	)
	return cmd
}

func saveSnapshotFunc(_ *cobra.Command, args []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.SaveSnapshot(ctx, args[0], force)
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
		Short: "Loads a network snapshot.",
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
	cmd.PersistentFlags().BoolVar(
		&inPlace,
		"in-place",
		false,
		"load snapshot in place, so as it always auto save",
	)
	return cmd
}

func loadSnapshotFunc(_ *cobra.Command, args []string) error {
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
			return fmt.Errorf("failed to validate JSON for provided config file: %w", err)
		}
		opts = append(opts, client.WithGlobalNodeConfig(globalNodeConfig))
	}

	ctx := getAsyncContext()

	resp, err := cli.LoadSnapshot(ctx, args[0], inPlace, opts...)
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("load-snapshot response: %+v"), resp)
	return nil
}

func newRemoveSnapshotCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove-snapshot snapshot-name",
		Short: "Removes a network snapshot.",
		RunE:  removeSnapshotFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func removeSnapshotFunc(_ *cobra.Command, args []string) error {
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

func newListSnapshotsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list-snapshots [options]",
		Short: "Lists available snapshots.",
		RunE:  listSnapshotsFunc,
		Args:  cobra.ExactArgs(0),
	}
}

func listSnapshotsFunc(*cobra.Command, []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	snapshotNames, err := cli.ListSnapshots(ctx)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("Snapshots: %s"), snapshotNames)
	return nil
}

func newVMIDCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vmid vm-name",
		Short: "Returns the vm id associated to the given vm name.",
		RunE:  VMIDFunc,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func VMIDFunc(_ *cobra.Command, args []string) error {
	vmName := args[0]
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	vmID, err := cli.VMID(ctx, vmName)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, logging.Green.Wrap("VMID: %s"), vmID)
	return nil
}

func newListSubnetsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-subnets",
		Short: "Lists all subnet ids of the network.",
		RunE:  listSubnetsFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func listSubnetsFunc(*cobra.Command, []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	subnetIDs, err := cli.ListSubnets(ctx)
	cancel()
	if err != nil {
		return err
	}

	for _, subnetID := range subnetIDs {
		ux.Print(log, logging.Green.Wrap("Subnet ID: %s"), subnetID)
	}

	return nil
}

func newListBlockchainsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-blockchains",
		Short: "Lists all blockchain ids of the network.",
		RunE:  listBlockchainsFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func listBlockchainsFunc(*cobra.Command, []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.ListBlockchains(ctx)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, "")
	for _, blockchain := range resp {
		ux.Print(log, logging.Green.Wrap("Blockchain ID: %s"), blockchain.ChainId)
		ux.Print(log, logging.Green.Wrap("    VM Name: %s"), blockchain.ChainName)
		ux.Print(log, logging.Green.Wrap("    VM ID: %s"), blockchain.VmId)
		ux.Print(log, logging.Green.Wrap("    Subnet ID: %s"), blockchain.SubnetId)
		ux.Print(log, "")
	}

	return nil
}

func newListRPCsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-rpcs",
		Short: "Lists rpcs for all blockchain of the network.",
		RunE:  listRPCsFunc,
		Args:  cobra.ExactArgs(0),
	}
	return cmd
}

func listRPCsFunc(*cobra.Command, []string) error {
	cli, err := newClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	resp, err := cli.ListRpcs(ctx)
	cancel()
	if err != nil {
		return err
	}

	ux.Print(log, "")
	for _, blockchainRpcs := range resp {
		ux.Print(log, logging.Green.Wrap("Blockchain ID: %s"), blockchainRpcs.BlockchainId)
		for _, rpc := range blockchainRpcs.Rpcs {
			ux.Print(log, logging.Green.Wrap("    %s: %s"), rpc.NodeName, rpc.Rpc)
		}
		ux.Print(log, "")
	}
	return nil
}

func newClient() (client.Client, error) {
	if err := setLogs(); err != nil {
		return nil, err
	}
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
