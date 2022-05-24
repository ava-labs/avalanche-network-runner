package main

import (
	"context"
	"fmt"
	"go/build"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	healthyTimeout = 2 * time.Minute
)

var goPath = os.ExpandEnv("$GOPATH")

// Blocks until a signal is received on [signalChan], upon which
// [n.Stop()] is called. If [signalChan] is closed, does nothing.
// Closes [closedOnShutdownChan] amd [signalChan] when done shutting down network.
// This function should only be called once.
func shutdownOnSignal(
	log logging.Logger,
	n network.Network,
	signalChan chan os.Signal,
	closedOnShutdownChan chan struct{},
) {
	sig := <-signalChan
	log.Info("got OS signal %s", sig)
	if err := n.Stop(context.Background()); err != nil {
		log.Debug("error while stopping network: %s", err)
	}
	signal.Reset()
	close(signalChan)
	close(closedOnShutdownChan)
}

// Shows example usage of the Avalanche Network Runner.
// Creates a local five node Avalanche network
// and waits for all nodes to become healthy.
// The network runs until the user provides a SIGINT or SIGTERM.
func main() {
	// Create the logger
	logFactory := logging.NewFactory(logging.DefaultConfig)
	log, err := logFactory.Make("main")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if goPath == "" {
		goPath = build.Default.GOPATH
	}
	binaryPath := fmt.Sprintf("%s%s", goPath, "/src/github.com/ava-labs/avalanchego/build/avalanchego")
	if err := run(log, binaryPath); err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}
}

func run(log logging.Logger, binaryPath string) error {
	// Create the network
	nw, err := local.NewNetwork(
		log,
		"", // use ANR default rootDir for locating logs/dbs/configs
		"", // use ANR defalts snapshotsDir for locating snapshots
	)
	if err != nil {
		return err
	}
	if err := nw.StartFromConfig(context.Background(), local.NewDefaultConfig(binaryPath)); err != nil {
		return err
	}
	defer func() { // Stop the network when this function returns
		if err := nw.Stop(context.Background()); err != nil {
			log.Debug("error stopping network: %w", err)
		}
	}()

	// When we get a SIGINT or SIGTERM, stop the network and close [closedOnShutdownCh]
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT)
	signal.Notify(signalsChan, syscall.SIGTERM)
	closedOnShutdownCh := make(chan struct{})
	go func() {
		shutdownOnSignal(log, nw, signalsChan, closedOnShutdownCh)
	}()

	// Wait until the nodes in the network are ready
	ctx, cancel := context.WithTimeout(context.Background(), healthyTimeout)
	defer cancel()
	log.Info("waiting for all nodes to report healthy...")
	if err := nw.Healthy(ctx); err != nil {
		return err
	}

	log.Info("All nodes healthy. Network will run until you CTRL + C to exit...")
	// Wait until done shutting down network after SIGINT/SIGTERM
	<-closedOnShutdownCh
	return nil
}
