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

	"github.com/ava-labs/avalanche-network-runner/grpc/server"
	"github.com/ava-labs/avalanche-network-runner/localbinary"
	"github.com/ava-labs/avalanche-network-runner/pkg/logutil"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func init() {
	cobra.EnablePrefixMatching = true
}

var (
	logLevel              string
	port                  string
	gwPort                string
	dialTimeout           time.Duration
	orchestratorBaseDir   string
	teardownOnExit        bool
	avalancheGoBinaryPath string
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server [options]",
		Short: "Start a network runner server.",
		RunE:  serverFunc,
	}

	cmd.PersistentFlags().StringVar(&logLevel, "log-level", logutil.DefaultLogLevel.String(), "log level")
	cmd.PersistentFlags().StringVar(&port, "port", ":8080", "server port")
	cmd.PersistentFlags().StringVar(&gwPort, "grpc-gateway-port", ":8081", "grpc-gateway server port")
	cmd.PersistentFlags().DurationVar(&dialTimeout, "dial-timeout", 10*time.Second, "server dial timeout")
	cmd.PersistentFlags().StringVar(&orchestratorBaseDir, "base-directory", constants.BaseDataDir, "Set the base directory for the orchestrator running behind the server.")
	cmd.PersistentFlags().BoolVar(&teardownOnExit, "destroy-on-teardown", false, "Set boolean on whether or not all data associated with the orchestrator should be destroyed on shutdown.")
	cmd.PersistentFlags().StringVar(&avalancheGoBinaryPath, "avalanchego-binary-path", constants.AvalancheGoBinary, "Sets the path to use for the AvalancheGo binary.")

	return cmd
}

func serverFunc(cmd *cobra.Command, args []string) (err error) {
	lcfg := logutil.GetDefaultZapLoggerConfig()
	level, err := zapcore.ParseLevel(logLevel)
	if err != nil {
		return err
	}
	lcfg.Level = zap.NewAtomicLevelAt(level)
	logger, err := lcfg.Build()
	if err != nil {
		log.Fatalf("failed to build global logger, %v", err)
	}
	_ = zap.ReplaceGlobals(logger)

	orchestrator := localbinary.NewNetworkOrchestrator(&localbinary.OrchestratorConfig{
		BaseDir: orchestratorBaseDir,
		Registry: map[string]string{
			constants.NormalExecution: avalancheGoBinaryPath,
		},
		DestroyOnTeardown: teardownOnExit,
	})

	s, err := server.New(server.Config{
		Port:        port,
		GwPort:      gwPort,
		DialTimeout: dialTimeout,
	}, orchestrator)
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
		zap.L().Warn("closed server", zap.Error(<-errc))
	case err = <-errc:
		zap.L().Warn("server closed", zap.Error(err))
		rootCancel()
	}
	return err
}
