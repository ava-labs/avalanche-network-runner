package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanche-network-runner/vms"
	avagoconfig "github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	healthyTimeout = 2 * time.Minute
	subnetTimeout  = 2 * time.Minute
	binaryPathKey  = "binaryPath"
	vmPathKey      = "vmPaths"
	genesisPathKey = "genesisPaths"
	subnetIDKey    = "subnetIds"
	vmIDKey        = "vmIds"
	loglevelKey    = "logLevel"
)

var (
	goPath            = os.ExpandEnv("$GOPATH")
	defaultBinaryPath = fmt.Sprintf("%s%s", goPath, "/src/github.com/ava-labs/avalanchego/build/avalanchego")
)

type customVMConfig struct {
	BinaryPath   string   `json:"binaryPath"`
	VMPaths      []string `json:"vmPaths"`
	GenesisPaths []string `json:"genesisPaths"`
	SubnetIDs    []string `json:"subnetIds"`
	VMIDs        []string `json:"vmIds"`
}

// Shows example usage of the Avalanche Network Runner with custom VMs.
// Creates a local five node Avalanche network all starting an avalanchego binary
// and a provided custom VM binary. Waits for all nodes to become healthy.
// The network runs until the user provides a SIGINT or SIGTERM.
// Example of how to run this:
// go run programs/local/customvm/main.go --vm-path "/path/to/vm/binary" --genesis-path "/path/to/genesis/file" --subnet-ids "24tZhrm8j8GCJRE9PomW8FaeqbgGS4UAQjJnqqn8pq5NwYSYV1" --vm-ids "tGas3T58KzdjLHhBDMnH2TvrddhqTji5iZAMZ3RXs2NLpSnhH"
func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// Create the logger
	loggingConfig, err := logging.DefaultConfig()
	if err != nil {
		return err
	}
	logFactory := logging.NewFactory(loggingConfig)
	defer logFactory.Close()
	log, err := logFactory.Make("main")
	if err != nil {
		return err
	}

	// setup all configs
	config, pviper, err := setup(log)
	if err != nil {
		log.Fatal("%s", err)
		return err
	}

	if err := checkSliceEqualLen(config.VMPaths, config.GenesisPaths, config.SubnetIDs, config.VMIDs); err != nil {
		log.Fatal("error checking supplied params: %s", err)
		return err
	}

	customVms := make([]vms.CustomVM, len(config.VMPaths))
	for i, v := range config.VMPaths {
		customVms[i] = vms.CustomVM{
			Path:     v,
			Genesis:  config.GenesisPaths[i],
			Name:     filepath.Base(v),
			SubnetID: config.SubnetIDs[i],
			ID:       config.VMIDs[i],
		}
	}

	var level logging.Level
	if slevel := pviper.GetString(loglevelKey); slevel != "" {
		level, err = logging.ToLevel(slevel)
		if err != nil {
			log.Warn("invalid log level string provided: %s. Ignoring and setting level to INFO", slevel)
		}
	} else {
		level = logging.Info
	}
	log.SetDisplayLevel(level)
	log.SetLogLevel(level)

	nw, err := local.NewDefaultNetworkWithVm(log, config.BinaryPath, customVms, pviper.GetString(avagoconfig.WhitelistedSubnetsKey))
	if err != nil {
		log.Error("failed to start the network: %s", err)
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
		log.Error("error waiting for network to become healthy: %s", err)
		return err
	}
	log.Info("network healthy")
	// use a new timed context as we need to wait for the validators validation start time
	subnetCtx, subnetCancel := context.WithTimeout(ctx, subnetTimeout)
	defer subnetCancel()
	for _, v := range customVms {
		if err := vms.CreateSubnetAndBlockchain(
			subnetCtx,
			log,
			v,
			nw,
			local.DefaultNetworkFundedPrivateKey); err != nil {
			log.Error("failed running the subnet: %s", err)
			return err
		}
	}
	log.Info("Subnet and blockchains created. Network will run until you CTRL + C to exit...")

	// Wait until done shutting down network after SIGINT/SIGTERM
	<-closedOnShutDownChan
	return nil
}

// setup all configuration options (cli flags, env vars, config file) with viper
func setup(log logging.Logger) (customVMConfig, *viper.Viper, error) {
	v := viper.New()
	viper.SetConfigName("config") // name of config file (without extension)
	viper.AddConfigPath(".")      // path to look for the config file in
	v.SetDefault(binaryPathKey, defaultBinaryPath)

	flagSet := pflag.NewFlagSet("customVMConfig", pflag.ExitOnError)
	flagSet.String(binaryPathKey, defaultBinaryPath, "Path to avalanchego binary")
	flagSet.StringSlice(vmPathKey, []string{""}, "Comma-separated list of file paths to custom vms")
	flagSet.StringSlice(genesisPathKey, []string{""}, "Comma-separated list of file paths to genesis files")
	flagSet.StringSlice(subnetIDKey, []string{""}, "Comma-separated list of subnetIDs for whitelisting")
	flagSet.StringSlice(vmIDKey, []string{""}, "Comma-separated list of VM IDs")
	flagSet.String(avagoconfig.WhitelistedSubnetsKey, "", "List of whitelisted subnets")
	flagSet.String(loglevelKey, "info", "Log level for this program")

	if err := v.BindPFlags(flagSet); err != nil {
		return customVMConfig{}, v, fmt.Errorf("couldn't bind pflags: %s", err)
	}
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		return customVMConfig{}, v, fmt.Errorf("couldn't parse pflags: %s", err)
	}

	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		log.Warn("no config file provided")
	}

	var c customVMConfig
	if err := v.Unmarshal(&c); err != nil {
		return customVMConfig{}, v, fmt.Errorf("couldn't unmarshal config: %s", err)
	}

	return c, v, nil
}

// checkSliceEqualLen compares the list of provided string arrays for its length
// It returns an error if all the provided arrays are not of equal length.
// It also returns an error if the arg list empty or if the arrays don't have
// at least one element
func checkSliceEqualLen(ss ...[]string) error {
	if len(ss) < 1 {
		return fmt.Errorf("no arguments")
	}
	checkLen := len(ss[0])
	if checkLen < 1 {
		return fmt.Errorf("one or more arguments missing")
	}
	for _, s := range ss {
		if s[0] == "" {
			return fmt.Errorf("empty argument")
		}
		if len(s) != checkLen {
			return fmt.Errorf("unequal length")
		}
	}
	return nil
}
