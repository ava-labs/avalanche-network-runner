package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanche-network-runner/vms"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	healthyTimeout = 2 * time.Minute
	subnetTimeout  = 2 * time.Minute
	configPathKey  = "config-path"
)

var (
	goPath                 = os.ExpandEnv("$GOPATH")
	defaultAvalanchegoPath string
	defaultEVMPath         string
	defaultEVMGenesis      = []byte(`{
		"config": {
			"chainId": 43115,
			"homesteadBlock": 0,
			"daoForkBlock": 0,
			"daoForkSupport": true,
			"eip150Block": 0,
			"eip150Hash": "0x2086799aeebeae135c246c65021c82b4e15a2c451340993aacfd2751886514f0",
			"eip155Block": 0,
			"eip158Block": 0,
			"byzantiumBlock": 0,
			"constantinopleBlock": 0,
			"petersburgBlock": 0,
			"istanbulBlock": 0,
			"muirGlacierBlock": 0,
			"apricotPhase1BlockTimestamp": 0,
			"apricotPhase2BlockTimestamp": 0
		},
		"nonce": "0x0",
		"timestamp": "0x0",
		"extraData": "0x00",
		"gasLimit": "0x5f5e100",
		"difficulty": "0x0",
		"mixHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
		"coinbase": "0x0000000000000000000000000000000000000000",
		"alloc": {
			"8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC": {
				"balance": "0x295BE96E64066972000000"
			}
		},
		"number": "0x0",
		"gasUsed": "0x0",
		"parentHash": "0x0000000000000000000000000000000000000000000000000000000000000000"
	}`)
	defaultConfig = `{
		"logLevel":"INFO",
		"avalanchegoPath":"%s",
		"vmConfigs":[
			{
				"name":"tGas3T58KzdjLHhBDMnH2TvrddhqTji5iZAMZ3RXs2NLpSnhH",
				"vmPath":"%s",
				"genesis":"%s",
				"subnetID":"24tZhrm8j8GCJRE9PomW8FaeqbgGS4UAQjJnqqn8pq5NwYSYV1",
				"vmID":"tGas3T58KzdjLHhBDMnH2TvrddhqTji5iZAMZ3RXs2NLpSnhH"
			}
		]
	}`
)

// TODO find a better way to define a default config
func init() {
	defaultAvalanchegoPath = filepath.Join(goPath, "src", "github.com", "ava-labs", "avalanchego", "build", "avalanchego")
	defaultEVMPath = filepath.Join(goPath, "src", "github.com", "ava-labs", "avalanchego", "build", "plugins", "evm")
	defaultEVMGenesisBase64 := base64.StdEncoding.EncodeToString(defaultEVMGenesis)
	defaultConfig = fmt.Sprintf(defaultConfig, defaultAvalanchegoPath, defaultEVMPath, defaultEVMGenesisBase64)
	defaultConfig = strings.ReplaceAll(defaultConfig, "\n", "")
	defaultConfig = strings.ReplaceAll(defaultConfig, "\t", "")
}

type config struct {
	LogLevel string `json:"logLevel"`
	// Path to AvalancheGo binary
	AvalanchegoPath string                  `json:"avalanchegoPath"`
	VMConfigs       []vms.CustomChainConfig `json:"vmConfigs"`
}

// Execute the Avalanche Network Runner with custom VMs.
// Creates a local five node Avalanche network all starting an avalanchego binary
// and provided custom VM binaries.
// The binary supports multiple VMs but to date only one has been thoroughly tested.
// Waits for all nodes to become healthy.
// The network runs until the user provides a SIGINT or SIGTERM.
// Example of how to run this:
// go run programs/local/customvm/main.go
func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

// All application logic error handling is done in this function.
// If a non-nil error is returned, main exits with code 1.
func run() error {
	// Create the logger
	loggingConfig, err := logging.DefaultConfig()
	if err != nil {
		fmt.Printf("couldn't create logging config: %s\n", err)
		return err
	}
	logFactory := logging.NewFactory(loggingConfig)
	defer logFactory.Close()
	log, err := logFactory.Make("main")
	if err != nil {
		fmt.Printf("couldn't create log factory: %s\n", err)
		return err
	}

	// read config
	config, err := readConfig(log)
	if err != nil {
		log.Fatal("couldn't read config: %s", err)
		return err
	}

	whiteListedSubnets := []string{}
	for _, chainConfig := range config.VMConfigs {
		whiteListedSubnets = append(whiteListedSubnets, chainConfig.SubnetID)
	}
	nw, err := local.NewDefaultNetworkWithCustomChains(log, config.AvalanchegoPath, config.VMConfigs, whiteListedSubnets)
	if err != nil {
		log.Fatal("failed to start the network: %s", err)
		return err
	}
	defer func() { // Stop the network when this function returns
		if err := nw.Stop(context.Background()); err != nil && !errors.Is(err, network.ErrStopped) {
			log.Debug("error stopping network: %w", err)
		}
	}()

	closedOnShutDownChan := utils.WatchShutdownSignals(log, nw.Stop)

	log.Info("waiting for all nodes to report healthy...")

	// Wait until the nodes in the network are ready
	ctx, cancel := context.WithTimeout(context.Background(), healthyTimeout)
	defer cancel()

	if err := <-nw.Healthy(ctx); err != nil {
		log.Fatal("error waiting for network to become healthy: %s", err)
		return err
	}
	log.Info("network healthy")
	// use a new timed context as we need to wait for the validators validation start time
	subnetCtx, subnetCancel := context.WithTimeout(ctx, subnetTimeout)
	defer subnetCancel()
	for _, v := range config.VMConfigs {
		if err := vms.CreateSubnetAndBlockchain(
			subnetCtx,
			log,
			v,
			nw,
			local.DefaultNetworkFundedPrivateKey,
		); err != nil {
			log.Fatal("failed running the subnet: %s", err)
			return err
		}
	}
	log.Info("Subnet and blockchains created. Network will run until you CTRL + C to exit...")

	// Wait until done shutting down network after SIGINT/SIGTERM
	<-closedOnShutDownChan
	return nil
}

// readConfig all configuration options (cli flags, env vars, config file) with viper
func readConfig(log logging.Logger) (config, error) {
	flagSet := pflag.NewFlagSet("customVMConfig", pflag.ContinueOnError)
	configPath := flagSet.String(configPathKey, "", "Path to config.json")
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		return config{}, fmt.Errorf("couldn't parse pflags: %s", err)
	}

	v := viper.New()
	// By default, look for config file named "config" in this directory
	v.SetConfigName("config")
	v.AddConfigPath(".")
	v.SetDefault("avalanchegoPath", defaultAvalanchegoPath)
	v.SetDefault("logLevel", logging.Info.String())
	if len(*configPath) > 0 {
		// If config file path given, use that.
		// Note that SetConfigName and AddConfigPath are ignored in this case.
		v.SetConfigFile(*configPath)
	}
	v.AutomaticEnv()

	var c config
	// Read the config file
	if err := v.ReadInConfig(); err != nil {
		log.Warn("no config file provided. Using default, which creates an evm instance.")
		if err := json.Unmarshal([]byte(defaultConfig), &c); err != nil {
			return config{}, fmt.Errorf("couldn't unmarshal default config: %w", err)
		}
	} else {
		if err := v.Unmarshal(&c); err != nil {
			return config{}, fmt.Errorf("couldn't unmarshal config: %s", err)
		}
	}
	logLevel, err := logging.ToLevel(c.LogLevel)
	if err != nil {
		log.Warn("got invalid log level %q. Ignoring and using log level INFO", logLevel)
	} else {
		log.SetDisplayLevel(logLevel)
		log.SetLogLevel(logLevel)
	}
	log.Debug("using config: %+v", c)
	return c, nil
}
