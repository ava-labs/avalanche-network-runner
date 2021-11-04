package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/k8s"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const DefaultNetworkTimeout = 120 * time.Second

func main() {
	// Create the logger
	loggingConfig, err := logging.DefaultConfig()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	logFactory := logging.NewFactory(loggingConfig)
	log, err := logFactory.Make("main")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	config := network.Config{
		NodeCount: 4,
		LogLevel:  "debug",
		Name:      "test-val",
	}
	opts := k8s.Opts{
		Namespace:      "dev",
		DeploymentSpec: "avalanchego-test-validator",
		Kind:           "Avalanchego",
		APIVersion:     "chain.avax.network/v1alpha1",
		Image:          "avaplatform/avalanchego",
		Tag:            "v1.6.4",
		LogLevelKey:    "AVAGO_LOG_LEVEL",
		Log:            log,
	}

	timeout, cancel := context.WithTimeout(context.Background(), DefaultNetworkTimeout)
	defer cancel()

	adapter := k8s.NewAdapter(opts)
	_, err = adapter.NewNetwork(config)
	defer adapter.Stop(timeout)
	if err != nil {
		log.Error("Error creating network: %v", err)
		return
	}

	log.Info("Network created. Booting...")

	errc := adapter.Healthy()

	select {
	case <-timeout.Done():
		log.Error("Timed out waiting for network to boot. Exiting.")
		return
	case err := <-errc:
		if err == nil {
			log.Info("Network created!!!")
			return
		}
		log.Error("Error booting network: %v", err)
		return
	}
}
