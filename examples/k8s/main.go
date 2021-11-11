package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ava-labs/avalanche-network-runner/k8s"
	"github.com/ava-labs/avalanche-network-runner/runner"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	// defaultNetworkTimeout to wait network to come up until deemed failed
	defaultNetworkTimeout = 120 * time.Second
	// confFileName          = "minimal-conf.json"
	confFileName = "custom-conf.json"
)

// TODO: shouldn't we think of something like Viper for loading config file?
var confPath = os.ExpandEnv("$GOPATH")

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

	configDir := fmt.Sprintf("%s/src/github.com/ava-labs/avalanche-network-runner/examples/common/", confPath)
	if confPath == "" {
		configDir = "./examples/common/"
	}
	confFile, err := os.ReadFile(configDir + confFileName)
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}

	var rconfig runner.RunnerConfig
	err = json.Unmarshal(confFile, &rconfig)
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}

	// TODO maybe do config validation
	config, err := runner.LoadConfig(log, rconfig, configDir)
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}

	/*
		level, err := logging.ToLevel(config.LogLevel)
		if err != nil {
			log.Warn("Invalid log level configured: %s", err)
		}
		log.SetLogLevel(level)
	*/
	ctx, cancel := context.WithTimeout(context.Background(), defaultNetworkTimeout)
	defer cancel()

	adapter, err := k8s.NewNetwork(config, log)
	if err != nil {
		log.Fatal("Error creating network: %s", err)
		os.Exit(1)
	}
	defer func() {
		if err := adapter.Stop(ctx); err != nil {
			log.Error("Error stopping network (ignored): %s", err)
		}
	}()

	log.Info("Network created. Booting...")

	errCh := adapter.Healthy()

	select {
	case <-ctx.Done():
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
