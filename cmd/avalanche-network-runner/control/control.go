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
	"github.com/ava-labs/avalanche-network-runner/pkg/color"
	"github.com/ava-labs/avalanche-network-runner/pkg/logutil"
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
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "control [options]",
		Short: "Start a network runner controller.",
	}

	cmd.PersistentFlags().StringVar(&logLevel, "log-level", logutil.DefaultLogLevel, "log level")
	cmd.PersistentFlags().StringVar(&endpoint, "endpoint", "0.0.0.0:8080", "server endpoint")
	cmd.PersistentFlags().DurationVar(&dialTimeout, "dial-timeout", 10*time.Second, "server dial timeout")
	cmd.PersistentFlags().DurationVar(&requestTimeout, "request-timeout", 3*time.Minute, "client request timeout")

	cmd.AddCommand(
		newStartCommand(),
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
	)

	return cmd
}

var (
	avalancheGoBinPath        string
	numNodes                  uint32
	pluginDir                 string
	globalNodeConfig          string
	customVMNameToGenesisPath string
	customNodeConfigs         string
)

func newStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start [options]",
		Short: "Starts the server.",
		RunE:  startFunc,
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
		&customVMNameToGenesisPath,
		"custom-vms",
		"",
		"[optional] JSON string of map that maps from VM to its genesis file path",
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
		"[optional] custom node configs as JSON string of map, for each node individually. Common entries override `global-node-config`, but can be combined. Invalidates `number-of-nodes` (provide all node configs if uses).",
	)
	return cmd
}

func startFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	opts := []client.OpOption{
		client.WithNumNodes(numNodes),
		client.WithPluginDir(pluginDir),
		client.WithWhitelistedSubnets(whitelistedSubnets),
	}

	if globalNodeConfig != "" {
		color.Outf("{{yellow}} global node config provided, will be applied to all nodes{{/}} %+v\n", globalNodeConfig)

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

	if customVMNameToGenesisPath != "" {
		customVMs := make(map[string]string)
		if err := json.Unmarshal([]byte(customVMNameToGenesisPath), &customVMs); err != nil {
			return err
		}
		opts = append(opts, client.WithCustomVMs(customVMs))
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	// don't call since "start" is async
	// and the top-level context here "ctx" is passed
	// to all underlying function calls
	// just set the timeout to halt "Start" async ops
	// when the deadline is reached
	_ = cancel

	info, err := cli.Start(
		ctx,
		avalancheGoBinPath,
		opts...,
	)
	if err != nil {
		return err
	}

	color.Outf("{{green}}start response:{{/}} %+v\n", info)
	return nil
}

func newHealthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health [options]",
		Short: "Requests server health.",
		RunE:  healthFunc,
	}
	return cmd
}

func healthFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
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

	color.Outf("{{green}}health response:{{/}} %+v\n", resp)
	return nil
}

func newURIsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uris [options]",
		Short: "Requests server uris.",
		RunE:  urisFunc,
	}
	return cmd
}

func urisFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
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

	color.Outf("{{green}}URIs:{{/}} %q\n", uris)
	return nil
}

func newStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [options]",
		Short: "Requests server status.",
		RunE:  statusFunc,
	}
	return cmd
}

func statusFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
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

	color.Outf("{{green}}status response:{{/}} %+v\n", resp)
	return nil
}

var pushInterval time.Duration

func newStreamStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stream-status [options]",
		Short: "Requests server bootstrap status.",
		RunE:  streamStatusFunc,
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
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
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
			zap.L().Warn("received signal", zap.String("signal", sig.String()))
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
		color.Outf("{{cyan}}cluster info:{{/}} %+v\n", info)
	}
	cancel() // receiver channel is closed, so cancel goroutine
	<-donec
	return nil
}

var nodeName string

func newRemoveNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove-node [options]",
		Short: "Removes a node.",
		RunE:  removeNodeFunc,
	}
	cmd.PersistentFlags().StringVar(&nodeName, "node-name", "", "node name to remove")
	return cmd
}

func removeNodeFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
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

	color.Outf("{{green}}remove node response:{{/}} %+v\n", info)
	return nil
}

func newAddNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-node [options]",
		Short: "Add a new node to the network",
		RunE:  addNodeFunc,
	}
	cmd.PersistentFlags().StringVar(
		&nodeName,
		"node-name",
		"",
		"node name to add",
	)
	cmd.PersistentFlags().StringVar(
		&avalancheGoBinPath,
		"avalanchego-path",
		"",
		"avalanchego binary path",
	)
	cmd.PersistentFlags().StringVar(
		&logLevel,
		"log-level",
		"",
		"log level",
	)
	cmd.PersistentFlags().StringVar(
		&customVMNameToGenesisPath,
		"custom-vms",
		"",
		"[optional] JSON string of map that maps from VM to its genesis file path",
	)
	cmd.PersistentFlags().StringVar(
		&globalNodeConfig,
		"node-config",
		"",
		"node config as string",
	)
	return cmd
}

func addNodeFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	opts := []client.OpOption{
		client.WithLogLevel(logLevel),
	}

	if globalNodeConfig != "" {
		color.Outf("{{yellow}}WARNING: overriding node configs with custom provided config {{/}} %+v\n", globalNodeConfig)
		// validate it's valid JSON
		var js json.RawMessage
		if err := json.Unmarshal([]byte(globalNodeConfig), &js); err != nil {
			return fmt.Errorf("failed to validate JSON for provided config file: %s", err)
		}
		opts = append(opts, client.WithGlobalNodeConfig(globalNodeConfig))
	}

	if customVMNameToGenesisPath != "" {
		customVMs := make(map[string]string)
		err = json.Unmarshal([]byte(customVMNameToGenesisPath), &customVMs)
		if err != nil {
			return err
		}
		opts = append(opts, client.WithCustomVMs(customVMs))
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

	color.Outf("{{green}}add node response:{{/}} %+v\n", info)
	return nil
}

func newRestartNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart-node [options]",
		Short: "Restarts the server.",
		RunE:  restartNodeFunc,
	}
	cmd.PersistentFlags().StringVar(
		&nodeName,
		"node-name",
		"",
		"node name to restart",
	)
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
	return cmd
}

func restartNodeFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.RestartNode(ctx, nodeName, client.WithExecPath(avalancheGoBinPath), client.WithWhitelistedSubnets(whitelistedSubnets))
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}restart node response:{{/}} %+v\n", info)
	return nil
}

func newAttachPeerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach-peer [options]",
		Short: "Attaches a peer to the node.",
		RunE:  attachPeerFunc,
	}
	cmd.PersistentFlags().StringVar(
		&nodeName,
		"node-name",
		"",
		"node name to attach a peer to",
	)
	return cmd
}

func attachPeerFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
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

	color.Outf("{{green}}attach peer response:{{/}} %+v\n", resp)
	return nil
}

var (
	peerID      string
	msgOp       uint32
	msgBytesB64 string
)

func newSendOutboundMessageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send-outbound-message [options]",
		Short: "Sends an outbound message to an attached peer.",
		RunE:  sendOutboundMessageFunc,
	}
	cmd.PersistentFlags().StringVar(
		&nodeName,
		"node-name",
		"",
		"node name that has an attached peer",
	)
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
	return cmd
}

func sendOutboundMessageFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
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

	color.Outf("{{green}}send outbound message response:{{/}} %+v\n", resp)
	return nil
}

func newStopCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop [options]",
		Short: "Requests server stop.",
		RunE:  stopFunc,
	}
	return cmd
}

func stopFunc(cmd *cobra.Command, args []string) error {
	cli, err := client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    endpoint,
		DialTimeout: dialTimeout,
	})
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

	color.Outf("{{green}}stop response:{{/}} %+v\n", info)
	return nil
}
