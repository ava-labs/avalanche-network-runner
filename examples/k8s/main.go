package main

import (
	"context"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/k8s"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/sirupsen/logrus"
)

const DefaultNetworkTimeout = 120 * time.Second

func main() {
	logrus.SetLevel(logrus.DebugLevel)
	config := network.Config{
		NodeCount: 4,
		LogLevel:  "debug",
		Name:      "test-val",
	}
	opts := k8s.Opts{
		Namespace:      "default",
		DeploymentSpec: "avalanchego-test-validator",
	}

	timeout, cancel := context.WithTimeout(context.Background(), DefaultNetworkTimeout)
	defer cancel()

	adapter := k8s.NewAdapter(opts)
	_, err := adapter.NewNetwork(config)
	defer adapter.Stop(timeout)
	if err != nil {
		logrus.Errorf("Error creating network: %v", err)
		return
	}

	logrus.Info("Network created. Booting...")

	errc := adapter.Healthy()

	select {
	case <-timeout.Done():
		logrus.Error("Timed out waiting for network to boot. Exiting.")
		return
	case err := <-errc:
		if err == nil {
			logrus.Info("Network created!!!")
			return
		}
		logrus.Errorf("Error booting network: %v", err)
		return
	}
}
