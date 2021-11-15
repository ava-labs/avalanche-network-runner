package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ava-labs/avalanche-network-runner/k8s"
	"github.com/ava-labs/avalanche-network-runner/network"
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
		NodeCount: 5,
		LogLevel:  "debug",
		Name:      "test-val",
		ImplSpecificConfig: k8s.Config{
			Namespace:      "dev",
			DeploymentSpec: "avalanchego-test-validator",
			Kind:           "Avalanchego",
			APIVersion:     "chain.avax.network/v1alpha1",
			Image:          "avaplatform/avalanchego",
			Tag:            "v1.6.4",
			LogLevelKey:    "AVAGO_LOG_LEVEL",
		},
	}

	timeout, cancel := context.WithTimeout(context.Background(), DefaultNetworkTimeout)
	defer cancel()

	adapter, err := k8s.NewNetwork(config, log)
	if err != nil {
		log.Fatal("Error creating network: %s", err)
		os.Exit(1)
	}
	defer adapter.Stop(timeout)

	log.Info("Network created. Booting...")

	errCh := adapter.Healthy(timeout)

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatal("Error booting network: %s", err)
			os.Exit(1)
		}
	}
	log.Info("Network created!!!")
}
