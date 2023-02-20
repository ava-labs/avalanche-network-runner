// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner/server"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func init() {
	cobra.EnablePrefixMatching = true
}

var (
	logLevel           string
	logDir             string
	port               string
	gwPort             string
	gwDisabled         bool
	dialTimeout        time.Duration
	disableNodesOutput bool
	snapshotsDir       string
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server [options]",
		Short: "Start a network runner server.",
		RunE:  serverFunc,
		Args:  cobra.ExactArgs(0),
	}

	cmd.PersistentFlags().StringVar(&logLevel, "log-level", logging.Info.String(), "log level for server logs")
	cmd.PersistentFlags().StringVar(&logDir, "log-dir", "", "log directory")
	cmd.PersistentFlags().StringVar(&port, "port", ":8080", "server port")
	cmd.PersistentFlags().StringVar(&gwPort, "grpc-gateway-port", ":8081", "grpc-gateway server port")
	cmd.PersistentFlags().BoolVar(&gwDisabled, "disable-grpc-gateway", false, "true to disable grpc-gateway server (overrides --grpc-gateway-port)")
	cmd.PersistentFlags().DurationVar(&dialTimeout, "dial-timeout", 10*time.Second, "server dial timeout")
	cmd.PersistentFlags().BoolVar(&disableNodesOutput, "disable-nodes-output", false, "true to disable nodes stdout/stderr")
	cmd.PersistentFlags().StringVar(&snapshotsDir, "snapshots-dir", "", "directory for snapshots")

	return cmd
}

func serverFunc(*cobra.Command, []string) (err error) {
	if logDir == "" {
		logDir, err = os.MkdirTemp("", fmt.Sprintf("anr-server-logs-%d", time.Now().Unix()))
		if err != nil {
			return err
		}
	}

	logLevel, err := logging.ToLevel(logLevel)
	if err != nil {
		return err
	}

	logFactory := logging.NewFactory(logging.Config{
		RotatingWriterConfig: logging.RotatingWriterConfig{
			Directory: logDir,
		},
		DisplayLevel: logLevel,
		LogLevel:     logLevel,
	})
	log, err := logFactory.Make(constants.LogNameMain)
	if err != nil {
		return err
	}

	s, err := server.New(server.Config{
		Port:                port,
		GwPort:              gwPort,
		GwDisabled:          gwDisabled,
		DialTimeout:         dialTimeout,
		RedirectNodesOutput: !disableNodesOutput,
		SnapshotsDir:        snapshotsDir,
		LogLevel:            logLevel,
	}, log)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errChan := make(chan error)
	go func() {
		err := s.Run(ctx)
		if err != nil {
			log.Error("server Run error", zap.Error(err))
		}
		errChan <- err
	}()

	// Relay SIGINT and SIGTERM to [sigChan]
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		// Got a SIGINT or SIGTERM; stop the server and wait for it to finish.
		log.Warn("signal received: closing server", zap.String("signal", sig.String()))
		cancel()
		waitForServerStop := <-errChan
		log.Warn("closed server", zap.Error(waitForServerStop))
	case serverClosed := <-errChan:
		// The server stopped.
		log.Warn("server closed", zap.Error(serverClosed))
	}
	return nil
}
