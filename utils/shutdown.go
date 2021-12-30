package utils

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ava-labs/avalanchego/utils/logging"
)

type ShutdownFunc func(ctx context.Context) error

func WatchShutdownSignals(log logging.Logger, shutDownFunc ShutdownFunc) chan struct{} {
	// When we get a SIGINT or SIGTERM, stop the network and close [closedOnShutdownCh]
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT)
	signal.Notify(signalsChan, syscall.SIGTERM)
	closedOnShutdownCh := make(chan struct{})
	go func() {
		shutdownOnSignal(log, shutDownFunc, signalsChan, closedOnShutdownCh)
	}()

	return closedOnShutdownCh
}

// Blocks until a signal is received on [signalChan], upon which
// [n.Stop()] is called. If [signalChan] is closed, does nothing.
// Closes [closedOnShutdownChan] amd [signalChan] when done shutting down network.
// This function should only be called once.
func shutdownOnSignal(
	log logging.Logger,
	shutDownFunc ShutdownFunc,
	signalChan chan os.Signal,
	closedOnShutdownChan chan struct{},
) {
	sig := <-signalChan
	log.Info("got OS signal %s", sig)
	if err := shutDownFunc(context.Background()); err != nil {
		log.Debug("error while stopping network: %s", err)
	}
	signal.Reset()
	close(signalChan)
	close(closedOnShutdownChan)
}
