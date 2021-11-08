package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/k8s"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	// DefaultNetworkTimeout to wait network to come up until deemed failed
	DefaultNetworkTimeout = 120 * time.Second

	confFileName = "/conf.json"
)

// TODO: shouldn't we think of something like Viper for loading config file?
var confPath = os.ExpandEnv("$GOPATH")

type allConfig struct {
	NetworkConfig network.Config `json:"network_config"`
	K8sConfig     k8s.Config     `json:"k8s_config"`
}

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

	configDir := fmt.Sprintf("%s/src/github.com/ava-labs/avalanche-network-runner-local/examples/k8s", confPath)
	if confPath == "" {
		configDir = "./examples/k8s"
	}
	confFile, err := ioutil.ReadFile(configDir + confFileName)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	var rconfig allConfig
	err = json.Unmarshal(confFile, &rconfig)
	if err != nil {
		os.Exit(1)
	}

	// TODO maybe do config validation

	config := rconfig.NetworkConfig
	config.ImplSpecificConfig = rconfig.K8sConfig

	timeout, cancel := context.WithTimeout(context.Background(), DefaultNetworkTimeout)
	defer cancel()

	adapter, err := k8s.NewNetwork(config, log)
	if err != nil {
		log.Fatal("Error creating network: %s", err)
		os.Exit(1)
	}
	defer func() {
		if err := adapter.Stop(timeout); err != nil {
			log.Error("Error stopping network (ignored): %s", err)
		}
	}()

	log.Info("Network created. Booting...")

	errCh := adapter.Healthy()

	select {
	case <-timeout.Done():
		log.Fatal("Timed out waiting for network to boot. Exiting.")
		os.Exit(1)
	case err := <-errCh:
		if err != nil {
			log.Fatal("Error booting network: %s", err)
			os.Exit(1)
		}
	}
	log.Info("Network created!!!")
}
