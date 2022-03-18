// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package control

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner/client"
	"github.com/ava-labs/avalanche-network-runner/pkg/color"
	"github.com/ava-labs/avalanche-network-runner/pkg/logutil"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func init() {
	cobra.EnablePrefixMatching = true
}

var (
	logLevel       string
	endpoint       string
	dialTimeout    time.Duration
	requestTimeout time.Duration
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "control [options]",
		Short: "Start a network runner controller.",
	}

	cmd.PersistentFlags().StringVar(&logLevel, "log-level", logutil.DefaultLogLevel, "log level")
	cmd.PersistentFlags().StringVar(&endpoint, "endpoint", "0.0.0.0:8080", "server endpoint")
	cmd.PersistentFlags().DurationVar(&dialTimeout, "dial-timeout", 10*time.Second, "server dial timeout")
	cmd.PersistentFlags().DurationVar(&requestTimeout, "request-timeout", time.Minute, "client request timeout")

	cmd.AddCommand(
		newStartCommand(),
		newHealthCommand(),
		newURIsCommand(),
		newStatusCommand(),
		newStreamStatusCommand(),
		newRemoveNodeCommand(),
		newRestartNodeCommand(),
		newStopCommand(),
	)

	return cmd
}

var (
	avalancheGoBinPath string
	whitelistedSubnets string
	nodeConfigFilePath string
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
	cmd.PersistentFlags().StringVar(
		&whitelistedSubnets,
		"whitelisted-subnets",
		"",
		"whitelisted subnets (comma-separated)",
	)
	cmd.PersistentFlags().StringVar(
		&nodeConfigFilePath,
		"node-config-path",
		"",
		"Path to file with node configs. 1 file in path: config applied to all nodes. More than 1 file: number of files defines number of nodes",
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

	configFiles := []string{}
	if nodeConfigFilePath != "" {
		color.Outf("{{yellow}}WARNING: overriding node configs with custom provided config files{{/}} %+v\n", nodeConfigFilePath)

		files, err := os.ReadDir(nodeConfigFilePath)
		if err != nil {
			return err
		}

		for _, f := range files {
			fileBytes, err := os.ReadFile(f.Name())
			if err != nil {
				return fmt.Errorf("failed to read provided config file: %s", err)
			}
			// validate it's valid JSON
			var js json.RawMessage
			if err := json.Unmarshal(fileBytes, &js); err != nil {
				return fmt.Errorf("failed to validate JSON for provided config file: %s", err)
			}
			configFiles = append(configFiles, string(js))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	info, err := cli.Start(
		ctx,
		avalancheGoBinPath,
		client.WithWhitelistedSubnets(whitelistedSubnets),
		client.WithNodeConfig(configFiles),
	)
	cancel()
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
	info, err := cli.RestartNode(ctx, nodeName, avalancheGoBinPath, client.WithWhitelistedSubnets(whitelistedSubnets))
	cancel()
	if err != nil {
		return err
	}

	color.Outf("{{green}}restart node response:{{/}} %+v\n", info)
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
