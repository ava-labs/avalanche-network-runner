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

func serverFunc(cmd *cobra.Command, args []string) (err error) {
	if logDir == "" {
		logDir, err = os.MkdirTemp("", fmt.Sprintf("anr-server-logs-%d", time.Now().Unix()))
		if err != nil {
			return err
		}
	}
	lvl, err := logging.ToLevel(logLevel)
	if err != nil {
		return err
	}
	lcfg := logging.Config{
		DisplayLevel: lvl,
		LogLevel:     lvl,
	}
	lcfg.Directory = logDir
	logFactory := logging.NewFactory(lcfg)
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
		LogLevel:            lvl,
	}, log)
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
		log.Warn("signal received; closing server: %s", sig.String())
		rootCancel()
		// wait for server stop
		waitForServerStop := <-errc
		log.Warn("closed server; %s", waitForServerStop)
	case serverClosed := <-errc:
		// server already stopped here
		_ = rootCancel
		log.Warn("server closed: %s", serverClosed)
	}
	return err
}
