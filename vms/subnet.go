package vms

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"golang.org/x/sync/errgroup"

	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/vms/platformvm"
)

// Config for a blockchain that will be created
type CustomChainConfig struct {
	// Name for the custom chain
	Name string
	// Path to VM binary
	VMPath string `json:"vmPath"`
	// Path to blockchain genesis
	GenesisPath string `json:"genesisPath"`
	// ID of subnet that the blockchain will run on as the string repr. of an ids.ID
	// TODO remove once dynamic whitelisting is supported by avalanchego
	SubnetID string `json:"subnetID"`
	// The VM's ID as the string repr. of an ids.ID
	VMID string `json:"vmID"`
}

// Contains arguments used in several functions in this file
type args struct {
	log           logging.Logger
	client        platformvm.Client
	fundedAddress string
	userPass      api.UserPass
	nodes         map[string]node.Node
	subnetID      ids.ID
}

// CreateSubnetAndBlockchain creates the necessary transactions to create a subnet for a given custom VM.
// It then also waits until all these transactions are confirmed on all nodes.
// Finally it creates a blockchain and makes sure all nodes of the given network
// are validating this blockchain.
// It requires a `privateKey` in order to issue the necessary transactions
func CreateSubnetAndBlockchain(
	ctx context.Context,
	log logging.Logger,
	vm CustomChainConfig,
	network network.Network,
	privateKey string,
) error {
	log.Info("creating subnet and blockchain")

	// initialize necessary args for the API calls
	args, err := newArgs(log, vm, network, privateKey)
	if err != nil {
		return fmt.Errorf("failed initializing subnet: %w", err)
	}

	// create the subnet
	if err := createSubnet(ctx, args); err != nil {
		return fmt.Errorf("failed creating subnet: %w", err)
	}
	args.log.Info("all nodes accepted subnet tx creation")

	// check the newly created subnet is in the subnet list
	if err := assertSubnetCreated(args.client, args.subnetID); err != nil {
		return fmt.Errorf("failed to confirm subnet is in the node's subnet list")
	}

	// add all nodes as validators to the subnet
	if err := addAllAsValidators(ctx, args, args.subnetID); err != nil {
		return fmt.Errorf("failed to add nodes as validators: %w", err)
	}

	// create the blockchain
	blockchainID, err := createBlockchain(ctx, args, vm)
	if err != nil {
		return fmt.Errorf("failed creating blockchain: %w", err)
	}

	// Make sure all nodes are validating the new chain
	if err := ensureValidating(ctx, log, args.nodes, blockchainID); err != nil {
		return fmt.Errorf("error checking all nodes are validating the blockchain: %w", err)
	}

	// Wait until all nodes finish bootstrapping the new chain
	if err := awaitBootstrapped(ctx, log, args.nodes, blockchainID); err != nil {
		return fmt.Errorf("error checking blockchain is bootstrapped: %w", err)
	}

	// Print endpoints where VM is accessible
	log.Info("Custom VM endpoints now accessible at:")
	for _, n := range args.nodes {
		log.Info("%s: %s:%d/ext/bc/%s", n.GetNodeID(), n.GetURL(), n.GetAPIPort(), blockchainID.String())
	}
	return nil
}

// initialize shared args
func newArgs(
	log logging.Logger,
	vm CustomChainConfig,
	network network.Network,
	fundedPChainPrivateKey string,
) (*args, error) {
	nodes, err := network.GetAllNodes()
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, errors.New("there are no nodes in this network")
	}

	nodeNames, err := network.GetNodeNames()
	if err != nil {
		return nil, err
	}

	// issuer is the node we issue transactions from
	issuer := nodes[nodeNames[0]]
	client := issuer.GetAPIClient()
	// create a user and import a funded key
	if ok, err := client.KeystoreAPI().CreateUser(defaultUserPass); !ok || err != nil {
		return nil, fmt.Errorf("could not create user: %w", err)
	}
	pClient := client.PChainAPI()
	fundedAddress, err := pClient.ImportKey(defaultUserPass, fundedPChainPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("unable to import genesis key: %w", err)
	}

	subnetID, err := ids.FromString(vm.SubnetID)
	if err != nil {
		return nil, fmt.Errorf("invalid subnetID string: %w", err)
	}
	return &args{
		log:           log,
		client:        pClient,
		fundedAddress: fundedAddress,
		userPass:      defaultUserPass,
		nodes:         nodes,
		subnetID:      subnetID,
	}, nil
}

// createSubnet creates a subnet and waits for all
// nodes in [args.nodes] to accept the transaction.
func createSubnet(ctx context.Context, args *args) error {
	// Create a subnet
	subnetIDTx, err := args.client.CreateSubnet(
		args.userPass,
		[]string{args.fundedAddress},
		args.fundedAddress,
		[]string{args.fundedAddress},
		defaultKeyThreshold,
	)
	if err != nil {
		return fmt.Errorf("unable to create subnet: %w", err)
	}
	return utils.AwaitAllNodesPChainTxAccepted(
		ctx,
		args.log,
		args.nodes,
		subnetIDTx,
	)
}

// assertSubnetCreated returns an error if subnet [subnetID] doesn't exist
func assertSubnetCreated(client platformvm.Client, subnetID ids.ID) error {
	subnets, err := client.GetSubnets([]ids.ID{subnetID})
	if err != nil {
		return fmt.Errorf("couldn't get subnets: %w", err)
	}
	if len(subnets) != 1 {
		return fmt.Errorf("subnet %s not found", subnetID)
	}
	return nil
}

// addAllAsValidators adds all nodes in [args.nodes] as validators of [args.subnetID].
func addAllAsValidators(ctx context.Context, args *args, subnetID ids.ID) error {
	// Add all validators to subnet with equal weight
	txIDs := []ids.ID{}
	for _, node := range args.nodes {
		nodeID := node.GetNodeID().PrefixedString(constants.NodeIDPrefix)
		txID, err := args.client.AddSubnetValidator(
			args.userPass,
			[]string{args.fundedAddress},
			args.fundedAddress,
			subnetID.String(),
			nodeID,
			validatorWeight,
			uint64(time.Now().Add(validatorStartDiff).Unix()),
			uint64(time.Now().Add(validatorEndDiff).Unix()),
		)
		if err != nil {
			return fmt.Errorf("unable to add subnet validator: %w", err)
		}
		txIDs = append(txIDs, txID)
	}

	// wait until all nodes have accepted all AddSubnetValidator transactions
	// TODO do this in a goroutine to check statuses in parallel
	for _, txID := range txIDs {
		if err := utils.AwaitAllNodesPChainTxAccepted(
			ctx,
			args.log,
			args.nodes,
			txID,
		); err != nil {
			return fmt.Errorf("failed to get all nodes to accept transaction: %w", err)
		}
	}
	args.log.Info("all nodes added as subnet validators for subnet %s", subnetID)
	return nil
}

// createBlockchain creates a new blockchain with ID [vm.VMID]
// and the genesis at [vm.GenesisPath].
func createBlockchain(ctx context.Context, args *args, vm CustomChainConfig) (ids.ID, error) {
	// Read genesis
	genesis, err := os.ReadFile(vm.GenesisPath)
	if err != nil {
		return ids.Empty, fmt.Errorf("could not read genesis file (%s): %w", vm.GenesisPath, err)
	}
	// Create blockchain
	txID, err := args.client.CreateBlockchain(
		args.userPass,
		[]string{args.fundedAddress},
		args.fundedAddress,
		args.subnetID,
		vm.VMID,
		[]string{},
		vm.Name,
		genesis,
	)
	if err != nil {
		return ids.Empty, fmt.Errorf("could not create blockchain: %w", err)
	}
	// Wait until all nodes create the blockchain
	return txID, utils.AwaitAllNodesPChainTxAccepted(
		ctx,
		args.log,
		args.nodes,
		txID,
	)
}

// ensureValidating returns an error if not all of the nodes are validating this
// blockchain or if waiting for nodes to confirm validation status times out.
func ensureValidating(
	ctx context.Context,
	log logging.Logger,
	nodes map[string]node.Node,
	blockchainID ids.ID,
) error {
	// Ensure all nodes are validating subnet
	g, ctx := errgroup.WithContext(ctx)
	for _, node := range nodes {
		node := node
		g.Go(func() error {
			nodeName := node.GetName()
			client := node.GetAPIClient().PChainAPI()
			// TODO I don't think we need to do this in a loop.
			// GetBlockchainStatus should immediately return "validating"
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(apiRetryFreq):
				}
				status, err := client.GetBlockchainStatus(blockchainID.String())
				if err != nil {
					return fmt.Errorf("error querying blockchain status: %w", err)
				}
				if status == platformvm.Validating {
					log.Debug("%s is validating blockchain %s", nodeName, blockchainID)
					return nil
				}
				log.Debug("waiting for validating status for blockchainID %s on %s", blockchainID, nodeName)
			}
		})
	}
	return g.Wait()
}

// awaitBootstrapped returns when all nodes in [nodes]
// have finished bootstrapping [blockchainID].
// Waits at most [bootstrapTimeout] for a node to finish bootstrapping.
func awaitBootstrapped(
	ctx context.Context,
	log logging.Logger,
	nodes map[string]node.Node,
	blockchainID ids.ID,
) error {
	ctx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()
	g, ctx := errgroup.WithContext(ctx)
	for _, node := range nodes {
		node := node
		g.Go(func() error {
			nodeName := node.GetName()
			client := node.GetAPIClient().InfoAPI()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(apiRetryFreq):
				}
				bootstrapped, err := client.IsBootstrapped(blockchainID.String())
				if err != nil {
					return fmt.Errorf("%s IsBootstrapped call failed: %w", nodeName, err)
				}
				if bootstrapped {
					log.Info("%s finished bootstrapping %s", nodeName, blockchainID)
					return nil
				}
				log.Debug("waiting for %s to finish bootstrapping %s", nodeName, blockchainID)
			}
		})
	}
	return g.Wait()
}
