package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ava-labs/avalanche-network-runner/localbinary/runner"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		// register signals to kill the application
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT)
		signal.Notify(signals, syscall.SIGTERM)
		// Wait to receive one of these signals to terminate
		<-signals
		cancel()
	}()

	if err := runner.RunNetwork(ctx, os.Args[1:], nil); err != nil {
		fmt.Printf("network exited due to: %s\n", err)
	}

	fmt.Printf("Shutdown network.\n")
}
