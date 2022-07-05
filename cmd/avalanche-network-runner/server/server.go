// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner/pkg/logutil"
	"github.com/ava-labs/avalanche-network-runner/server"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func init() {
	cobra.EnablePrefixMatching = true
}

var (
	logLevel           string
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

	cmd.PersistentFlags().StringVar(&logLevel, "log-level", logutil.DefaultLogLevel, "log level")
	cmd.PersistentFlags().StringVar(&port, "port", ":8080", "server port")
	cmd.PersistentFlags().StringVar(&gwPort, "grpc-gateway-port", ":8081", "grpc-gateway server port")
	cmd.PersistentFlags().BoolVar(&gwDisabled, "disable-grpc-gateway", false, "true to disable grpc-gateway server (overrides --grpc-gateway-port)")
	cmd.PersistentFlags().DurationVar(&dialTimeout, "dial-timeout", 10*time.Second, "server dial timeout")
	cmd.PersistentFlags().BoolVar(&disableNodesOutput, "disable-nodes-output", false, "true to disable nodes stdout/stderr")
	cmd.PersistentFlags().StringVar(&snapshotsDir, "snapshots-dir", "", "directory for snapshots")

	return cmd
}

func serverFunc(cmd *cobra.Command, args []string) (err error) {
	lcfg := logutil.GetDefaultZapLoggerConfig()
	lcfg.Level = zap.NewAtomicLevelAt(logutil.ConvertToZapLevel(logLevel))
	logger, err := lcfg.Build()
	if err != nil {
		log.Fatalf("failed to build global logger, %v", err)
	}
	_ = zap.ReplaceGlobals(logger)

	s, err := server.New(server.Config{
		Port:                port,
		GwPort:              gwPort,
		GwDisabled:          gwDisabled,
		DialTimeout:         dialTimeout,
		RedirectNodesOutput: !disableNodesOutput,
		SnapshotsDir:        snapshotsDir,
	})
	if err != nil {
		return err
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	errc := make(chan error)
	go func() {
		errc <- s.Run(rootCtx)
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigc:
		zap.L().Warn("signal received; closing server", zap.String("signal", sig.String()))
		rootCancel()
		// wait for server stop
		waitForServerStop := <-errc
		zap.L().Warn("closed server", zap.Error(waitForServerStop))
	case serverClosed := <-errc:
		// server already stopped here
		_ = rootCancel
		zap.L().Warn("server closed", zap.Error(serverClosed))
	}
	return err
}
