package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanche-network-runner/vms"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	healthyTimeout = 2 * time.Minute
	binaryPathKey  = "binary-path"
	vmPathKey      = "vm-path"
	genesisPathKey = "genesis-path"
	subnetIDKey    = "subnet-ids"
	vmIDKey        = "vm-ids"
)

var (
	goPath            = os.ExpandEnv("$GOPATH")
	defaultBinaryPath = fmt.Sprintf("%s%s", goPath, "/src/github.com/ava-labs/avalanchego/build/avalanchego")
)

type customVMConfig struct {
	BinaryPath string
	VmPath     []string
	VmGenesis  []string
	VmSubnets  []string
	VmIDs      []string
}

// Shows example usage of the Avalanche Network Runner.
// Creates a local five node Avalanche network
// and waits for all nodes to become healthy.
// The network runs until the user provides a SIGINT or SIGTERM.
// Example of how to run this:
// go run programs/local/customvm/main.go --vm-path "/path/to/vm/binary" --genesis-path "/path/to/genesis/file" --subnet-ids "24tZhrm8j8GCJRE9PomW8FaeqbgGS4UAQjJnqqn8pq5NwYSYV1" --vm-ids "tGas3T58KzdjLHhBDMnH2TvrddhqTji5iZAMZ3RXs2NLpSnhH"
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

	v := viper.New()
	v.SetDefault(binaryPathKey, defaultBinaryPath)

	bp := pflag.String(binaryPathKey, defaultBinaryPath, "Path to avalanchego binary")
	vp := pflag.StringSlice(vmPathKey, []string{""}, "Comma-separated list of file paths to custom vms")
	gp := pflag.StringSlice(genesisPathKey, []string{""}, "Comma-separated list of file paths to genesis files")
	sp := pflag.StringSlice(subnetIDKey, []string{""}, "Comma-separated list of subnetIDs for whitelisting")
	ip := pflag.StringSlice(vmIDKey, []string{""}, "Comma-separated list of VM IDs")

	pflag.Parse()

	c := customVMConfig{
		BinaryPath: *bp,
		VmPath:     *vp,
		VmGenesis:  *gp,
		VmSubnets:  *sp,
		VmIDs:      *ip,
	}

	if err := run(log, c); err != nil {
		log.Fatal("%s", err)
	}
}

func run(log logging.Logger, config customVMConfig) error {
	// Create the network
	if len(config.VmPath) < 1 || len(config.VmPath) != len(config.VmGenesis) {
		return fmt.Errorf("Creating a network with VMs requires VmPath and VmGenesis args of equal length and > 0")
	}

	customVms := make([]vms.CustomVM, len(config.VmPath))
	for i, v := range config.VmPath {
		customVms[i] = vms.CustomVM{
			Path:     v,
			Genesis:  config.VmGenesis[i],
			Name:     filepath.Base(v),
			SubnetID: config.VmSubnets[i],
			ID:       config.VmIDs[i],
		}
	}

	log.SetDisplayLevel(logging.Debug)
	log.SetLogLevel(logging.Debug)

	nw, err := local.NewDefaultNetworkWithVm(log, config.BinaryPath, customVms)
	if err != nil {
		return err
	}
	defer func() { // Stop the network when this function returns
		if err := nw.Stop(context.Background()); err != nil {
			log.Debug("error stopping network: %w", err)
		}
	}()

	// Wait until the nodes in the network are ready
	ctx, cancel := context.WithTimeout(context.Background(), healthyTimeout)
	defer cancel()
	healthyChan := nw.Healthy(ctx)
	log.Info("waiting for all nodes to report healthy...")
	if err := <-healthyChan; err != nil {
		return err
	}

	// use a new timed context as we need to wait for the validators validation start time
	subnetCtx, subnetCancel := context.WithTimeout(ctx, healthyTimeout)
	defer subnetCancel()
	for _, v := range customVms {
		if err := vms.SetupSubnet(
			subnetCtx,
			log,
			v,
			nw,
			local.DefaultNetworkFundedPrivateKey); err != nil {
			return err
		}
	}
	<-utils.WatchShutdownSignals(log, nw.Stop)
	return nil
}
