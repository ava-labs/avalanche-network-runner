package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/local"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/units"
)

var goPath = os.ExpandEnv("$GOPATH")

// Start a network with 1 node which connects to mainnet.
// Uses default configs.
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

	// Read node configs, staking keys, staking certs
	networkConfig := network.Config{}

	for i := 0; i < 5; i++ {
		log.Info("reading config %d", i)
		configDir := fmt.Sprintf("%s/src/github.com/ava-labs/avalanche-network-runner-local/example/configs", goPath)
		nodeConfigDir := fmt.Sprintf("%s/node%d", configDir, i)
		configFile, err := os.ReadFile(fmt.Sprintf("%s/config.json", nodeConfigDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		stakingCert, stakingKey, err := staking.NewCertAndKeyBytes()
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		// Derive the node ID.
		// TODO add helper for this?
		cert, err := tls.X509KeyPair(stakingCert, stakingKey)
		if err != nil {
			log.Fatal("%s", err)
		}
		cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			os.Exit(1)
		}
		nodeID := ids.ShortID(
			hashing.ComputeHash160Array(
				hashing.ComputeHash256(cert.Leaf.Raw),
			),
		)
		networkConfig.NodeConfigs = append(
			networkConfig.NodeConfigs,
			node.Config{
				Type:        local.AVALANCHEGO,
				ConfigFile:  configFile,
				StakingKey:  stakingKey,
				StakingCert: stakingCert,
				NodeID:      nodeID,
			},
		)
		if i == 0 {
			networkConfig.NodeConfigs[0].IsBeacon = true
		}
	}

	networkConfig.Genesis, err = network.NewAvalancheGoGenesis(
		log,
		uint32(1337),
		[]network.AddrAndBalance{
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 1,
			},
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 2,
			},
		},
		[]network.AddrAndBalance{
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 3,
			},
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 4,
			},
		},
		[]ids.ShortID{
			networkConfig.NodeConfigs[0].NodeID, // TODO change
			networkConfig.NodeConfigs[1].NodeID, // TODO change
		},
	)
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}
	log.Info("%s", networkConfig.Genesis)

	// Uncomment this line to print the first node's logs to stdout
	// networkConfig.NodeConfigs[0].Stdout = os.Stdout

	// Create the network
	nw, err := local.NewNetwork(
		log,
		networkConfig,
		map[local.NodeType]string{
			local.AVALANCHEGO: fmt.Sprintf("%s%s", goPath, "/src/github.com/ava-labs/avalanchego/build/avalanchego"),
		},
	)
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}

	// register signals to kill the network
	signalsCh := make(chan os.Signal, 1)
	signal.Notify(signalsCh, syscall.SIGINT)
	signal.Notify(signalsCh, syscall.SIGTERM)
	// start up a new go routine to handle attempts to kill the application
	go func() {
		// When we get a SIGINT or SIGTERM, stop the network.
		sig := <-signalsCh
		log.Info("got OS signal %s", sig)
		if err := nw.Stop(); err != nil {
			log.Warn("error while stopping network: %s", err)
		}
	}()

	// Wait until the nodes in the network are ready
	healthyChan := nw.Healthy()
	fmt.Println("waiting for all nodes to report healthy...")
	err, gotErr := <-healthyChan
	if gotErr {
		log.Fatal("network never became healthy: %s\n", err)
		handleError(log, nw)
	}
	log.Info("this network's nodes: %s\n", nw.GetNodesNames())
	time.Sleep(time.Minute)
	if err := nw.Stop(); err != nil {
		log.Warn("error while stopping network: %s", err)
	}
}

func handleError(log logging.Logger, nw network.Network) {
	if err := nw.Stop(); err != nil {
		log.Warn("error while stopping network: %s", err)
	}
	os.Exit(1)
}
