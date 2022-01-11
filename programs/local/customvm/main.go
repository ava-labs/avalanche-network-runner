package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	binaryPathKey  = "binary-path"
	vmPathKey      = "vm-path"
	genesisPathKey = "genesis-path"
	subnetIDKey    = "subnet-id"
	vmIDKey        = "vm-id"
)

var (
	goPath            = os.ExpandEnv("$GOPATH")
	defaultBinaryPath = fmt.Sprintf("%s%s", goPath, "/src/github.com/ava-labs/avalanchego/build/avalanchego")
)

type customVMConfig struct {
	BinaryPath string
	VMPath     []string
	VMGenesis  []string
	VMSubnets  []string
	VMIDs      []string
}

// Shows example usage of the Avalanche Network Runner with custom VMs.
// Creates a local five node Avalanche network all starting an avalanchego binary
// and a provided custom VM binary. Waits for all nodes to become healthy.
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
		VMPath:     *vp,
		VMGenesis:  *gp,
		VMSubnets:  *sp,
		VMIDs:      *ip,
	}

	if err := checkSliceEqualLen(c.VMPath, c.VMGenesis, c.VMSubnets, c.VMIDs); err != nil {
		pflag.Usage()
		os.Exit(1)
	}

	if err := run(log, c); err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}
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

func run(log logging.Logger, config customVMConfig) error {
	customVms := make([]vms.CustomVM, len(config.VMPath))
	for i, v := range config.VMPath {
		customVms[i] = vms.CustomVM{
			Path:     v,
			Genesis:  config.VMGenesis[i],
			Name:     filepath.Base(v),
			SubnetID: config.VMSubnets[i],
			ID:       config.VMIDs[i],
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
			if err != network.ErrStopped {
				log.Debug("error stopping network: %w", err)
			}
		}
	}()

	// Wait until the nodes in the network are ready
	ctx, cancel := context.WithTimeout(context.Background(), healthyTimeout)
	defer cancel()

	watchShutdown := utils.WatchShutdownSignals(log, nw.Stop)

	healthyChan := nw.Healthy(ctx)
	log.Info("waiting for all nodes to report healthy...")
	if err := <-healthyChan; err != nil {
		return err
	}
	// use a new timed context as we need to wait for the validators validation start time
	subnetCtx, subnetCancel := context.WithTimeout(ctx, subnetTimeout)
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
	// Wait until done shutting down network after SIGINT/SIGTERM
	log.Info("All nodes healthy, subnet created. Network will run until you CTRL + C to exit...")

	<-watchShutdown
	return nil
}
