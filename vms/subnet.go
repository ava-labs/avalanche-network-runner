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

// CustomVM wraps data to create a custom VM
type CustomVM struct {
	Path     string // Path to the binary of the VM
	Genesis  string // Genesis string for the VM
	Name     string // Name for the VM
	SubnetID string // SubnetID this VM is running on
	ID       string // ids.ID representation of the VM
}

// most subfunctions share the same args...
type args struct {
	log            logging.Logger
	txPChainClient platformvm.Client
	fundedAddress  string
	userPass       api.UserPass
	allNodes       map[string]node.Node
	rSubnetID      ids.ID
}

// CreateSubnetAndBlockchain creates the necessary transactions to create a subnet for a given custom VM.
// It then also waits until all these transactions are confirmed on all nodes.
// Finally it creates a blockchain and makes sure all nodes of the given network
// are validating this blockchain.
// It requires a `privateKey` in order to issue the necessary transactions
func CreateSubnetAndBlockchain(
	ctx context.Context,
	log logging.Logger,
	vm CustomVM,
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
	if err := isSubnetInList(args.txPChainClient, args.rSubnetID); err != nil {
		return fmt.Errorf("failed to confirm subnet is in the node's subnet list")
	}

	// add all nodes as validators
	if err := addAllAsValidators(ctx, args, vm.SubnetID); err != nil {
		return fmt.Errorf("failed to add nodes as validators: %w", err)
	}

	// create the blockchain for this vm
	blockchainID, err := createBlockchain(ctx, args, vm)
	if err != nil {
		return fmt.Errorf("failed creating blockchain: %w", err)
	}

	// make sure all nodes are validating this new blockchain
	if err := finalizeBlockchain(ctx, args.log, args.allNodes, blockchainID); err != nil {
		return fmt.Errorf("error checking all nodes are validating subnet: %w", err)
	}

	return nil
}

// initialize shared args
func newArgs(
	log logging.Logger,
	vm CustomVM,
	network network.Network,
	fundedPChainPrivateKey string,
) (*args, error) {
	userPass := defaultUserPass

	allNodes, err := network.GetAllNodes()
	if err != nil {
		return nil, err
	}

	txNodeNames, err := network.GetNodeNames()
	if err != nil {
		return nil, err
	}

	if len(txNodeNames) == 0 {
		return nil, errors.New("the array of node names is empty! Can't get any nodes")
	}
	// txNode will be the node we issue all transactions on
	txNode := allNodes[txNodeNames[0]]
	txClient := txNode.GetAPIClient()
	// we need to create a user for platformvm calls
	ok, err := txClient.KeystoreAPI().CreateUser(userPass)
	if !ok || err != nil {
		return nil, fmt.Errorf("could not create user: %w", err)
	}

	txPChainClient := txClient.PChainAPI()
	// Import genesis key
	fundedAddress, err := txPChainClient.ImportKey(userPass, fundedPChainPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("unable to import genesis key: %w", err)
	}

	rSubnetID, err := ids.FromString(vm.SubnetID)
	if err != nil {
		return nil, fmt.Errorf("invalid subnetID string: %w", err)
	}
	return &args{
		log:            log,
		txPChainClient: txPChainClient,
		fundedAddress:  fundedAddress,
		userPass:       userPass,
		allNodes:       allNodes,
		rSubnetID:      rSubnetID,
	}, nil
}

// createSubnet issues the CreateSubnet transaction and waits for
// it to be accepted. It returns an error if the transaction failed
// or there was a timout.
func createSubnet(ctx context.Context, args *args) error {
	// Create a subnet
	subnetIDTx, err := args.txPChainClient.CreateSubnet(
		args.userPass,
		[]string{args.fundedAddress},
		args.fundedAddress,
		[]string{args.fundedAddress},
		defaultKeyThreshold,
	)
	if err != nil {
		return fmt.Errorf("unable to create subnet: %w", err)
	}

	// wait until all nodes have accepted the CreateSubnet transaction
	return utils.AwaitedAllNodesPChainTxAccepted(
		ctx,
		args.log,
		apiRetryFreq,
		args.allNodes,
		subnetIDTx)
}

// isSubnetInList returns an error if the given subnet is not in the client's list
func isSubnetInList(client platformvm.Client, rSubnetID ids.ID) error {
	// confirm created subnet appears in subnet list
	var subnetAPIs []platformvm.APISubnet
	var err error
	if subnetAPIs, err = client.GetSubnets([]ids.ID{rSubnetID}); err != nil {
		return fmt.Errorf("subnet not found: %w", err)
	}
	// we should assume that the array returned has length 0 because it is filtered;
	// but because it's an array let's be thorough
	found := false
	for _, api := range subnetAPIs {
		if api.ID == rSubnetID {
			found = true
		}
	}
	if !found {
		return errors.New("subnet not in returned list")
	}
	return nil
}

// addAllAsValidators adds all nodes as validators to the given subnet
// and waits for each of the transactions to be accepted.
// Returns an error if any transaction failed or there was a timeout
func addAllAsValidators(ctx context.Context, args *args, subnetID string) error {
	// Add all validators to subnet with equal weight
	for _, node := range args.allNodes {
		nodeID := node.GetNodeID().PrefixedString(constants.NodeIDPrefix)
		txID, err := args.txPChainClient.AddSubnetValidator(
			args.userPass,
			[]string{args.fundedAddress},
			args.fundedAddress,
			subnetID,
			nodeID,
			validatorWeight,
			uint64(time.Now().Add(validatorStartDiff).Unix()),
			uint64(time.Now().Add(validatorEndDiff).Unix()),
		)
		if err != nil {
			return fmt.Errorf("unable to add subnet validator: %w", err)
		}

		// wait until all nodes have accepted the AddSubnetValidator transaction
		if err := utils.AwaitedAllNodesPChainTxAccepted(
			ctx,
			args.log,
			apiRetryFreq,
			args.allNodes,
			txID); err != nil {
			return fmt.Errorf("failed to get all nodes to accept transaction: %w", err)
		}

	}
	args.log.Info("all nodes added as subnet validators for subnet %s", subnetID)
	return nil
}

// createBlockchain performs the CreateBlockchain transaction and waits until
// the tx has been accepted or returns an error if this caused a problem
// or a timeout.
func createBlockchain(ctx context.Context, args *args, vm CustomVM) (ids.ID, error) {
	// Create blockchain
	genesis, err := os.ReadFile(vm.Genesis)
	if err != nil {
		return ids.Empty, fmt.Errorf("could not read genesis file (%s): %w", vm.Genesis, err)
	}
	txID, err := args.txPChainClient.CreateBlockchain(
		args.userPass,
		[]string{args.fundedAddress},
		args.fundedAddress,
		args.rSubnetID,
		vm.ID,
		[]string{},
		vm.Name,
		genesis,
	)
	if err != nil {
		return ids.Empty, fmt.Errorf("could not create blockchain: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ids.Empty, ctx.Err()
		case <-time.After(apiRetryFreq):
		}
		status, err := args.txPChainClient.GetTxStatus(txID, true)
		if err != nil {
			return ids.Empty, err
		}
		if status.Status == platformvm.Committed {
			args.log.Info("create blockchain tx (%s) accepted", txID)
			return txID, nil
		}
		args.log.Debug("waiting for create blockchain tx (%s) to be accepted", txID)
	}
}

// finalizeBlockchain is a checking function. It ensures that the given nodes
// are validating the blockchain, and that all nodes have the VM bootstrapped.
// If all is ok, it prints the endpoints to STDOUT, otherwise it returns an error.
func finalizeBlockchain(ctx context.Context, log logging.Logger, allNodes map[string]node.Node, blockchainID ids.ID) error {
	if err := ensureValidating(ctx, log, allNodes, blockchainID); err != nil {
		return fmt.Errorf("error checking all nodes are validating the blockchain: %w", err)
	}
	if err := ensureBootstrapped(ctx, log, allNodes, blockchainID); err != nil {
		return fmt.Errorf("error checking blockchain is bootstrapped: %w", err)
	}
	// Print endpoints where VM is accessible
	log.Info("Custom VM endpoints now accessible at:")
	for _, n := range allNodes {
		log.Info("%s: %s:%d/ext/bc/%s", n.GetNodeID(), n.GetURL(), n.GetAPIPort(), blockchainID.String())
	}
	return nil
}

// ensureValidating returns an error if not all of the nodes are validating this
// blockchain or if waiting for nodes to confirm validation status times out.
func ensureValidating(tctx context.Context, log logging.Logger, allNodes map[string]node.Node, blockchainID ids.ID) error {
	statusCheckTimeout := longTimeout
	// Ensure all nodes are validating subnet
	g, ctx := errgroup.WithContext(tctx)
	for _, node := range allNodes {
		node := node
		g.Go(func() error {
			nodeID := node.GetNodeID().PrefixedString(constants.NodeIDPrefix)
			nClient := node.GetAPIClient().PChainAPI()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(statusCheckTimeout):
				}
				status, err := nClient.GetBlockchainStatus(blockchainID.String())
				if err != nil {
					return fmt.Errorf("error querying blockchain status: %w", err)
				}
				if status == platformvm.Validating {
					// after the first acceptance, next nodes probably don't need to check that long anymore
					statusCheckTimeout = apiRetryFreq
					log.Info("%s validating blockchain %s", nodeID, blockchainID)
					return nil
				}
				log.Debug("waiting for validating status for blockchainID %s on %s", blockchainID.String(), nodeID)
			}
		})
	}

	return g.Wait()
}

// ensureBootstrapped returns an error if not all nodes report the
// given blockchain as bootstrapped or if waiting for nodes to confirm
// the bootstrap status times out.
func ensureBootstrapped(tctx context.Context, log logging.Logger, allNodes map[string]node.Node, blockchainID ids.ID) error {
	// Ensure network bootstrapped
	g, ctx := errgroup.WithContext(tctx)
	for _, node := range allNodes {
		node := node
		g.Go(func() error {
			nodeID := node.GetNodeID().PrefixedString(constants.NodeIDPrefix)
			nClient := node.GetAPIClient().InfoAPI()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(apiRetryFreq):
				}
				if bootstrapped, _ := nClient.IsBootstrapped(blockchainID.String()); bootstrapped {
					log.Info("%s bootstrapped %s", nodeID, blockchainID)
					return nil
				}
				log.Debug("waiting for %s to bootstrap %s", nodeID, blockchainID.String())
			}
		})
	}
	return g.Wait()
}
